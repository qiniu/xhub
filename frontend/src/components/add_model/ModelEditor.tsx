"use client";
import { useT } from "@/i18n";
import { useState } from "react";
import { FormProvider, useForm, useWatch } from "react-hook-form";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCredentials } from "@/app/(dashboard)/hooks/credentials/useCredentials";
import { usePriceCatalog, priceCatalogKeys } from "@/app/(dashboard)/hooks/models/usePriceCatalog";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { apiClient, modelCreateCall, modelPatchUpdateCall, type CredentialItem } from "@/components/networking";
import { MountedFormProvider, useMountRegistry, type MountedFormValues } from "../common_components/MountedFormField";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import EndpointTypeField from "./EndpointTypeField";
import { BILLING_CATEGORIES, type BillingMode } from "./billing_categories";
import {
  buildEditorModel,
  catalogFieldValue,
  editorDefaults,
  hasPrice,
  billingModeOf,
  PRICE_FIELDS,
  type EditorModel,
  type CatalogRow,
  catalogRows,
  pricingRates,
  suggestedModelName,
} from "./modelEditorPricing";
import PricingTable from "./PricingTable";
import RateEditor from "./RateEditor";
import { ProviderModelDialog } from "../model_add/ProviderModelDialog";
import type { ListedModel } from "../model_add/providerCatalogImport";
import ModelAutocomplete from "./ModelAutocomplete";
import { toast } from "@/lib/toast";

const selectClass = "h-10 w-full rounded-md border border-input bg-background px-3 text-sm";
/** ModelEditor 管理供应商连接引用、上游模型、公开端点与独立价格绑定。
 * 参数 model：编辑的部署；initialSupplier/initialCatalogId：新建预选项；onSaved/onCancel：完成回调。
 * 返回：模型配置表单；连接凭据由供应商统一管理，表单不复制密钥。
 * 调用：模型管理页面。测试：ModelEditor.test.tsx、modelEditorPricing.test.ts。 */
export default function ModelEditor({
  model,
  initialSupplier,
  initialCatalogId,
  onSaved,
  onCancel,
}: {
  model?: EditorModel;
  initialSupplier?: CredentialItem;
  initialCatalogId?: string;
  onSaved?: (model: unknown) => void;
  onCancel?: () => void;
}) {
  const t = useT();
  const { accessToken } = useAuthorized();
  const queryClient = useQueryClient();
  const { data: credentials, isLoading: loadingSuppliers } = useCredentials();
  const { data: catalog, error: catalogError } = usePriceCatalog(accessToken);
  const form = useForm<MountedFormValues>({
    defaultValues: {
      ...editorDefaults(model),
      ...(!model && initialCatalogId ? { pricing_model: initialCatalogId } : {}),
      ...(initialSupplier
        ? {
            supplier: initialSupplier.credential_name,
            custom_llm_provider:
              initialSupplier.credential_info?.custom_llm_provider ??
              initialSupplier.credential_values?.custom_llm_provider,
          }
        : {}),
    },
  });
  const values = useWatch({ control: form.control });
  const registry = useMountRegistry();
  const [saving, setSaving] = useState(false);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState("");
  const suppliers = credentials?.credentials ?? [];
  const supplier = suppliers.find((item) => item.credential_name === values.supplier);
  const rows = catalogRows(catalog?.models);
  const row = rows[String(values.pricing_model ?? "")];
  const provider = String(values.custom_llm_provider ?? "");
  const discovery = useQuery({
    queryKey: ["providerModelList", values.supplier],
    enabled: !!accessToken && !!supplier,
    queryFn: async ({ signal }) => {
      const body = await apiClient.post<{ models?: ListedModel[]; error?: string }>("/model/builtin/models", {
        accessToken: accessToken!,
        signal,
        body: { provider: provider, credential_name: supplier!.credential_name },
      });
      if (body.error && !body.models?.length) throw new Error(body.error);
      return { models: body.models ?? [], warning: body.error };
    },
    retry: false,
    staleTime: 60_000,
  });
  const modelOptions = (discovery.data?.models ?? []).map((item) => ({ value: item.id, label: item.id }));
  /** selectModel 更新上游模型并在公开名称仍为自动值时同步名称。
   * 参数 upstream：供应商模型 ID；返回：无。不会修改独立计价来源；端点字段自行校验新模型。 */
  const selectModel = (upstream: string) => {
    set("model", upstream);
    if (!values.model_name || values.model_name === suggestedModelName(String(values.model ?? ""), provider))
      set("model_name", suggestedModelName(upstream, provider));
  };
  /** changePricingSource 切换价格来源，并在首次改为手动时复制目录的当前价格。
   * 参数 event：价格来源选择事件；返回：无。保留规格、时段等费率维度。 */
  const changePricingSource = (event: React.ChangeEvent<HTMLSelectElement>) => {
    set("pricing_source", event.target.value);
    if (event.target.value === "manual" && values.pricing_source !== "manual") {
      seedPrices(row);
      set("manual_rates", pricingRates(row));
      set("pricing_format", Array.isArray(row?.rates) && row.rates.length ? "rates" : "flat");
    }
  };
  /** changeSupplier 切换命名供应商并清除依赖原供应商的模型与端点声明。
   * 参数 name：凭据名或保留现有连接选项；返回：无。独立价格绑定不跟随供应商自动改变。 */
  const changeSupplier = (name: string) => {
    if (name === values.supplier) return;
    if (name === "__existing__") {
      const existing = editorDefaults(model);
      for (const key of ["supplier", "custom_llm_provider", "model", "model_name", "transport", "endpoint_types", "web_search_support", "web_search_fallback", "messages_reasoning_mode"])
        set(key, existing[key]);
      return;
    }
    const next = suppliers.find((item) => item.credential_name === name);
    set("supplier", name);
    set(
      "custom_llm_provider",
      next?.credential_info?.custom_llm_provider ?? next?.credential_values?.custom_llm_provider ?? "",
    );
    set("model", "");
    set("catalog_model", "");
    set("transport", "");
    set("endpoint_types", []);
    set("web_search_support", "inherit");
    set("web_search_fallback", "reject");
    set("messages_reasoning_mode", "disabled");
    if (values.model_name === suggestedModelName(String(values.model ?? ""), provider)) set("model_name", "");
  };

  const categories = BILLING_CATEGORIES.map((category) => ({
    ...category,
    title: t(
      (
        {
          token_offpeak: "Off-peak",
          token_peak: "Peak",
          token_cache: "Cache",
          per_image: "Per image",
          per_second: "Per second",
          per_query: "Search",
        } as Record<string, string>
      )[category.id] ?? category.title,
    ),
    unit: t(
      category.id === "token_offpeak"
        ? "USD per 1M tokens · Mon–Fri outside 09:00–12:00 & 14:00–18:00 Beijing time, plus weekends and holidays"
        : category.id === "token_peak"
          ? "USD per 1M tokens · Mon–Fri 09:00–12:00 and 14:00–18:00 Beijing time (excl. public holidays)"
          : category.id === "per_image"
            ? "USD per image"
            : category.id === "per_second"
              ? "USD per second"
              : category.id === "per_query"
                ? "USD per search query"
                : "USD per 1M tokens",
    ),
    fields: category.fields.map((field) => ({
      ...field,
      label: t(
        (
          {
            input_cost_per_token: "Input",
            output_cost_per_token: "Output",
            input_cost_per_token_peak: "Input (peak)",
            output_cost_per_token_peak: "Output (peak)",
            cache_read_input_token_cost: "Cache read",
            cache_creation_input_token_cost: "Cache write",
            input_cost_per_image: "Input image",
            output_cost_per_image: "Output image",
            input_cost_per_second: "Input per second",
            output_cost_per_second: "Output per second",
            search_context_cost_per_query: "Per search",
          } as Record<string, string>
        )[field.name] ?? field.label,
      ),
    })),
  })).filter((category) => category.modes.includes(values.billing_mode as BillingMode));
  /** set 统一更新表单字段并标记需要保存。参数 key/value：字段和新值；返回：无。 */
  const set = (key: string, value: unknown) => form.setValue(key, value, { shouldDirty: true });
  /** seedPrices 将目录价格投影到手动基础单价字段。
   * 参数 selected：选中的价格行，可为空；返回：无。缺失单价保持未设置，避免误记免费。 */
  const seedPrices = (selected: CatalogRow | undefined) => {
    set("billing_mode", billingModeOf(selected));
    PRICE_FIELDS.forEach((field) => set(field.name, catalogFieldValue(selected, field.name)));
  };
  /** imported 在目录导入完成后刷新价格缓存与供应商模型发现结果。
   * 参数：无；返回 Promise<void>；刷新完成关闭导入弹窗。 */
  const imported = async () => {
    await queryClient.invalidateQueries({ queryKey: priceCatalogKeys.all });
    await queryClient.invalidateQueries({ queryKey: ["modelCostMap"] });
    await discovery.refetch();
    setImporting(false);
  };
  /** textField 创建受表单管理的必填文本字段。
   * 参数 key/label/placeholder：字段名、显示名称和提示；返回：带关联标签的输入控件。 */
  const textField = (key: string, label: string, placeholder?: string) => (
    <div className="space-y-2">
      <Label htmlFor={"editor-" + key}>{label}</Label>
      <Input
        id={"editor-" + key}
        value={String(values[key] ?? "")}
        onChange={(event) => set(key, event.target.value)}
        placeholder={placeholder}
        required
      />
    </div>
  );
  let submitLabel = model ? t("providerGuideUI.saveChanges") : t("providerGuideUI.addModel");
  if (saving) submitLabel = t("providerGuideUI.saving");
  /** save 校验连接引用和价格绑定后提交明确的模型部署契约。
   * 参数 submitted：当前表单值；返回：异步提交处理器，成功刷新列表并调用 onSaved。
   * 登录、字段或网络失败显示错误；finally 解除保存状态，防止表单永久禁用。 */
  const save = form.handleSubmit(async (submitted) => {
    setError("");
    try {
      if (!accessToken) throw new Error(t("providerGuideUI.sessionExpired"));
      if (submitted.pricing_source === "catalog" && !hasPrice(rows[String(submitted.pricing_model)]))
        throw new Error(t("providerGuideUI.unpricedCatalog"));
      if (
        submitted.supplier !== "__existing__" &&
        !suppliers.some((item) => item.credential_name === submitted.supplier)
      )
        throw new Error(t("providerGuideUI.chooseConfigured"));
      const payload = buildEditorModel(submitted, model);
      setSaving(true);
      const result = model
        ? await modelPatchUpdateCall(accessToken, payload, String(model.model_info.id))
        : await modelCreateCall(accessToken, payload);
      await queryClient.invalidateQueries({ queryKey: ["models"] });
      toast.success(model ? t("providerGuideUI.updated") : t("providerGuideUI.added"));
      if (!model) form.reset(editorDefaults());
      onSaved?.(result);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("providerGuideUI.saveFailed"));
    } finally {
      setSaving(false);
    }
  });
  return (
    <FormProvider {...form}>
      {importing && accessToken && supplier && (
        <ProviderModelDialog
          provider={String(provider)}
          initialCredentialName={supplier.credential_name}
          credentials={suppliers}
          accessToken={accessToken}
          onClose={() => setImporting(false)}
          onAdded={imported}
        />
      )}
      <MountedFormProvider value={{ control: form.control, registry }}>
        <form onSubmit={save} className="space-y-6">
          <div>
            <h2 className="text-xl font-semibold">
              {model ? t("providerGuideUI.editModel") : t("providerGuideUI.addModel")}
            </h2>
            <p className="mt-1 text-sm text-muted-foreground">{t("providerGuideUI.intro")}</p>
          </div>
          <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)]">
            <div className="space-y-6">
              <section className="space-y-5 rounded-xl border bg-card p-5 sm:p-6">
                <div>
                  <h3 className="font-semibold">{t("providerGuideUI.providerStep")}</h3>
                  <p className="mt-1 text-sm text-muted-foreground">{t("providerGuideUI.providerHelp")}</p>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="editor-supplier">{t("providerGuideUI.providerLabel")}</Label>
                  <select
                    id="editor-supplier"
                    className={selectClass}
                    value={String(values.supplier ?? "")}
                    required
                    disabled={loadingSuppliers || saving}
                    onChange={(event) => changeSupplier(event.target.value)}
                  >
                    <option value="">
                      {loadingSuppliers ? t("providerGuideUI.loadingProviders") : t("providerGuideUI.chooseProvider")}
                    </option>
                    {model && <option value="__existing__">{t("providerGuideUI.keepProvider")}</option>}
                    {model && values.supplier !== "__existing__" && values.supplier && !supplier && (
                      <option value={String(values.supplier)}>
                        {t("providerGuideUI.formerProvider", { name: values.supplier })}
                      </option>
                    )}
                    {suppliers.map((item) => (
                      <option key={item.credential_name} value={item.credential_name}>
                        {item.credential_name} · {item.credential_info?.custom_llm_provider}
                      </option>
                    ))}
                  </select>
                </div>
                {supplier && (
                  <p className="break-all text-sm text-muted-foreground">
                    {supplier.credential_info?.api_base ??
                      supplier.credential_values?.api_base ??
                      t("providerGuideUI.defaultAddress")}
                  </p>
                )}
                {supplier && (
                  <div className="space-y-2">
                    <div className="flex flex-wrap gap-2">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={discovery.isFetching || saving}
                        onClick={() => void discovery.refetch()}
                      >
                        {discovery.isFetching ? t("providerGuideUI.fetchingModels") : t("providerGuideUI.fetchModels")}
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        disabled={saving}
                        onClick={() => setImporting(true)}
                      >
                        {t("providerGuideUI.importModels")}
                      </Button>
                    </div>
                    {discovery.data && (
                      <p className="text-xs text-muted-foreground">
                        {t("providerGuideUI.fetched", { count: discovery.data.models.length })}
                      </p>
                    )}
                    {discovery.data?.warning && (
                      <p role="alert" className="text-xs text-destructive">
                        {t("providerGuideUI.discoveryWarning", { error: discovery.data.warning })}
                      </p>
                    )}
                    {discovery.error && (
                      <p role="alert" className="text-xs text-destructive">
                        {t("providerGuideUI.discoveryError", { error: discovery.error.message })}
                      </p>
                    )}
                  </div>
                )}
                {!loadingSuppliers && suppliers.length === 0 && (
                  <p className="text-sm text-muted-foreground">{t("providerGuideUI.noProviders")}</p>
                )}
              </section>
              <fieldset
                disabled={!values.supplier || saving}
                className="space-y-5 rounded-xl border bg-card p-5 sm:p-6 disabled:opacity-50"
              >
                <h3 className="font-semibold">{t("providerGuideUI.modelStep")}</h3>
                {!values.supplier && (
                  <p className="text-sm text-muted-foreground">{t("providerGuideUI.providerRequired")}</p>
                )}
                <div className="space-y-2">
                  <Label htmlFor="editor-model-select">{t("providerGuideUI.upstreamLabel")}</Label>
                  <ModelAutocomplete
                    id="editor-model-select"
                    value={String(values.model ?? "")}
                    onChange={(value) => selectModel(value)}
                    onSelect={selectModel}
                    options={modelOptions}
                    placeholder={t("providerGuideUI.upstreamPlaceholder")}
                    required
                  />
                  <p className="text-xs text-muted-foreground">{t("providerGuideUI.autocompleteHelp")}</p>
                  {catalogError && <p className="text-xs text-destructive">{t("providerGuideUI.marketFailed")}</p>}
                </div>
                {textField("model_name", t("providerGuideUI.publicName"), t("providerGuideUI.publicPlaceholder"))}
                <p className="text-xs leading-relaxed text-muted-foreground">{t("providerGuideUI.publicHelp")}</p>
                <EndpointTypeField
                  key={String(values.supplier)}
                  selectedProvider={String(values.custom_llm_provider || "")}
                  catalogId={
                    supplier
                      ? (supplier.credential_info?.catalog_id ?? "")
                      : String(model?.model_info?.catalog_id ?? "")
                  }
                  modelCostMap={rows}
                />
                <section aria-label={t("providerGuideUI.messagesReasoningTitle")} className="space-y-3 rounded-lg border p-4">
                  <h4 className="font-medium">{t("providerGuideUI.messagesReasoningTitle")}</h4>
                  <Label htmlFor="editor-messages-reasoning">{t("providerGuideUI.messagesReasoningMode")}</Label>
                  <select id="editor-messages-reasoning" className={selectClass}
                    value={String(values.messages_reasoning_mode ?? "disabled")}
                    onChange={event => set("messages_reasoning_mode", event.target.value)}>
                    <option value="disabled">{t("providerGuideUI.messagesReasoningDisabled")}</option>
                    <option value="adaptive" disabled={values.transport !== "bypass_anthropic_messages"}>{t("providerGuideUI.messagesReasoningAdaptive")}</option>
                  </select>
                  <p className="text-xs text-muted-foreground">{t("providerGuideUI.messagesReasoningHelp")}</p>
                  <p className="text-xs text-muted-foreground">{t("providerGuideUI.messagesReasoningHistory")}</p>
                </section>
                <section aria-label={t("providerGuideUI.searchTitle")} className="space-y-3 rounded-lg border p-4">
                  <h4 className="font-medium">{t("providerGuideUI.searchTitle")}</h4>
                  <p className="text-xs text-muted-foreground">{t("providerGuideUI.searchHelp")}</p>
                  <Label htmlFor="editor-search-support">{t("providerGuideUI.searchSupport")}</Label>
                  <select id="editor-search-support" className={selectClass} value={String(values.web_search_support ?? "inherit")} onChange={event => set("web_search_support", event.target.value)}>
                    <option value="inherit">{t("providerGuideUI.searchInherit")}</option>
                    <option value="supported">{t("providerGuideUI.searchSupported")}</option>
                    <option value="unsupported">{t("providerGuideUI.searchUnsupported")}</option>
                  </select>
                  <Label htmlFor="editor-search-fallback">{t("providerGuideUI.searchFallback")}</Label>
                  <select id="editor-search-fallback" className={selectClass} value={String(values.web_search_fallback ?? "reject")} onChange={event => set("web_search_fallback", event.target.value)}>
                    <option value="reject">{t("providerGuideUI.searchReject")}</option>
                    <option value="drop_optional">{t("providerGuideUI.searchDrop")}</option>
                  </select>
                  <p className="text-xs text-muted-foreground">{t("providerGuideUI.searchDropHelp")}</p>
                </section>
                <div className="flex items-center justify-between gap-4 rounded-lg border p-4">
                  <div>
                    <Label htmlFor="editor-enabled">{t("providerGuideUI.enable")}</Label>
                    <p className="mt-1 text-xs text-muted-foreground">{t("providerGuideUI.enableHelp")}</p>
                  </div>
                  <Switch
                    id="editor-enabled"
                    checked={values.disabled !== true}
                    onCheckedChange={(checked) => set("disabled", !checked)}
                  />
                </div>
              </fieldset>
            </div>
            <fieldset
              disabled={!values.supplier || !values.model || saving}
              className="space-y-5 rounded-xl border bg-card p-5 sm:p-6"
            >
              <div>
                <h3 className="font-semibold">{t("providerGuideUI.pricingStep")}</h3>
                <p className="mt-1 text-sm text-muted-foreground">{t("providerGuideUI.pricingIntro")}</p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="editor-pricing-source">{t("providerGuideUI.priceSource")}</Label>
                <select
                  id="editor-pricing-source"
                  className={selectClass}
                  value={String(values.pricing_source)}
                  onChange={changePricingSource}
                >
                  <option value="catalog">{t("providerGuideUI.catalogPrice")}</option>
                  <option value="manual">{t("providerGuideUI.manualPrice")}</option>
                </select>
              </div>
              <div className="space-y-2">
                <Label htmlFor="editor-pricing-model">
                  {t("providerGuideUI.pricingModel", {
                    suffix: values.pricing_source === "catalog" ? " *" : t("providerGuideUI.reference"),
                  })}
                </Label>
                <select
                  className={selectClass}
                  id="editor-pricing-model"
                  value={String(values.pricing_model ?? "")}
                  onChange={(event) => {
                    set("pricing_model", event.target.value);
                  }}
                >
                  <option value="">{t("providerGuideUI.choosePrice")}</option>
                  {Object.entries(rows)
                    .filter(([, item]) => hasPrice(item))
                    .map(([id]) => (
                      <option key={id} value={id}>
                        {id}
                      </option>
                    ))}
                </select>
              </div>
              {values.pricing_source === "catalog" ? (
                <>
                  {!hasPrice(row) ? (
                    <p className="text-sm text-amber-600">{t("providerGuideUI.priceHelp")}</p>
                  ) : (
                    <PricingTable row={row} />
                  )}
                </>
              ) : (
                <>
                  <div className="space-y-2">
                    <Label htmlFor="editor-pricing-format">{t("providerGuideUI.rateFormat")}</Label>
                    <select
                      id="editor-pricing-format"
                      className={selectClass}
                      value={String(values.pricing_format ?? "flat")}
                      onChange={(event) => {
                        if (event.target.value === "rates" && values.pricing_format !== "rates") {
                          try {
                            const current = buildEditorModel({ ...values, pricing_format: "flat" });
                            set("manual_rates", pricingRates(current.litellm_params));
                          } catch {
                            set("manual_rates", []);
                          }
                        }
                        set("pricing_format", event.target.value);
                      }}
                    >
                      <option value="flat">{t("providerGuideUI.flat")}</option>
                      <option value="rates">{t("providerGuideUI.rates")}</option>
                    </select>
                  </div>
                  {values.pricing_format === "rates" ? (
                    <RateEditor
                      rates={Array.isArray(values.manual_rates) ? values.manual_rates : []}
                      onChange={(rates) => set("manual_rates", rates)}
                    />
                  ) : (
                    <>
                      <div className="space-y-2">
                        <Label htmlFor="editor-billing-mode">{t("providerGuideUI.billingMode")}</Label>
                        <select
                          id="editor-billing-mode"
                          className={selectClass}
                          value={String(values.billing_mode)}
                          onChange={(event) => set("billing_mode", event.target.value)}
                        >
                          <option value="token">{t("providerGuideUI.tokens")}</option>
                          <option value="image">{t("providerGuideUI.images")}</option>
                          <option value="second">{t("providerGuideUI.seconds")}</option>
                        </select>
                      </div>
                      {categories.map((category) => (
                        <fieldset key={category.id} className="space-y-3 rounded-lg border p-4">
                          <legend className="px-1 text-sm font-medium">{category.title}</legend>
                          <div className="grid gap-4 sm:grid-cols-2">
                            {category.fields.map((field) => (
                              <div key={field.name} className="space-y-2">
                                <Label htmlFor={"editor-" + field.name}>{field.label}</Label>
                                <Input
                                  id={"editor-" + field.name}
                                  type="number"
                                  min="0"
                                  step="any"
                                  value={String(values[field.name] ?? "")}
                                  onChange={(event) => set(field.name, event.target.value)}
                                  placeholder={t("providerGuideUI.unset")}
                                />
                              </div>
                            ))}
                          </div>
                          <p className="text-xs text-muted-foreground">{category.unit}</p>
                        </fieldset>
                      ))}
                      <p className="text-xs text-muted-foreground">{t("providerGuideUI.rateHelp")}</p>
                    </>
                  )}
                </>
              )}
            </fieldset>
          </div>
          {error && (
            <p
              role="alert"
              className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
            >
              {error}
            </p>
          )}
          <div className="flex justify-end gap-3 border-t pt-5">
            {onCancel && (
              <Button type="button" variant="outline" disabled={saving} onClick={onCancel}>
                {t("providerGuideUI.cancel")}
              </Button>
            )}
            <Button type="submit" disabled={saving || loadingSuppliers || !values.supplier || !values.model}>
              {submitLabel}
            </Button>
          </div>
        </form>
      </MountedFormProvider>
    </FormProvider>
  );
}
