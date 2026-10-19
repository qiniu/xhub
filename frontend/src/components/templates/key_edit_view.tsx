import { z } from "zod/v4";
import React, { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FieldGroup } from "@/components/ui/field";
import { FormField } from "@/components/shared/form/FormField";
import { UiLoadingSpinner } from "@/components/ui/ui-loading-spinner";
import { useZodForm } from "@/lib/forms/useZodForm";
import { useT, t } from "@/i18n";
import KeyLifecycleSettings from "../common_components/KeyLifecycleSettings";
import RouteTemplateSelect from "../route_templates/RouteTemplateSelect";
import { KeyResponse } from "../key_team_helpers/key_list";
import { excludeProxyWideSentinel, hasAllModelsSentinel } from "../key_team_helpers/fetch_available_models_team_key";
import { modelAvailableCall } from "../networking";
import { fetchTeamModels } from "../organisms/create_key_button";
import { MultiSelect } from "../shared/MultiSelect";
import NumericalInput from "../shared/numerical_input";
import { KeyTypeSelect } from "./KeyEditViewControls";
import { keyTypeFromRoutes, modelSentinelOptions, parseAllowedRoutes } from "./keyEditFieldNormalizers";
import { keyEditFormSchema, KeyEditFormValues } from "./keyEditFormValues";

/** 编辑器参数：密钥及用户上下文用于读取模型，回调负责保存或取消；保留团队及授权属性以兼容调用方。 */
interface KeyEditViewProps {
  /** 详情标题和折叠说明，随表单一起滚动；创建式独立编辑器可省略。 */
  header?: React.ReactNode;
  keyData: KeyResponse;
  onCancel: () => void;
  onSubmit: (values: Record<string, unknown>) => Promise<void>;
  teams?: any[] | null;
  accessToken: string | null;
  userID: string | null;
  userRole: string | null;
  premiumUser?: boolean;
}

/** 构建精简编辑器校验；无参数，返回当前语言的 schema，空期限保留现状，非法期限阻止提交。 */
function editorSchema() {
  const duration = z
    .custom<string | null | undefined>()
    .refine((value) => !value?.trim() || /^[1-9][0-9]*[smhd]$/.test(value.trim()), {
      error: () => t("keyEditor.invalidDuration"),
    });
  return keyEditFormSchema
    .pick({
      key_alias: true,
      models: true,
      allowed_routes: true,
      max_budget: true,
      rpm_limit: true,
      tpm_limit: true,
      route_template_id: true,
      auto_rotate: true,
    })
    .extend({
      duration,
      rotation_interval: z.custom<string | undefined>(),
      max_budget: z
        .custom<number | string | null | undefined>()
        .refine((value) => value == null || value === "" || (Number.isFinite(Number(value)) && Number(value) >= 0), {
          error: () => t("keyEditor.invalidBudget"),
        }),
    })
    .superRefine((value, context) => {
      if (value.auto_rotate && !/^[1-9][0-9]*[smhd]$/.test(value.rotation_interval ?? "")) {
        context.addIssue({ code: "custom", path: ["duration"], message: t("keyEditor.invalidDuration") });
      }
    });
}

/** 从服务端密钥快照初始化基础表单，返回可见字段；到期输入留空，避免普通保存延长有效期，无副作用。 */
function defaults(key: KeyResponse): KeyEditFormValues {
  return {
    key_alias: key.key_alias ?? "",
    models: key.models ?? [],
    allowed_routes: (key.allowed_routes ?? []).join(","),
    max_budget: key.max_budget,
    rpm_limit: key.rpm_limit,
    tpm_limit: key.tpm_limit,
    route_template_id: key.route_template_id ?? "",
    duration: "",
    auto_rotate: key.auto_rotate ?? false,
    rotation_interval: key.rotation_interval || "30d",
  };
}

/** 提供与创建密钥一致的基础表单；参数含密钥、身份及保存回调，返回界面。
 * 只提交可见设置；未修改的路由类型和空期限不提交，保留既有权限、期限及高级配置。保存失败由调用方展示。 */
export function KeyEditView({
  header,
  keyData,
  onCancel,
  onSubmit,
  teams,
  accessToken,
  userID,
  userRole,
}: KeyEditViewProps) {
  const t = useT();
  const form = useZodForm<KeyEditFormValues, KeyEditFormValues>(editorSchema(), { defaultValues: defaults(keyData) });
  const [availableModels, setAvailableModels] = useState<string[]>([]);
  const [modelsError, setModelsError] = useState(false);
  const [saving, setSaving] = useState(false);
  const team = teams?.find((item) => item.team_id === keyData.team_id);
  const routes = parseAllowedRoutes(form.watch("allowed_routes"));
  const modelsDisabled = routes.includes("management_routes") || routes.includes("info_routes");
  const selectedModels = form.watch("models") ?? [];

  useEffect(() => {
    form.reset(defaults(keyData));
  }, [keyData, form]);
  useEffect(() => {
    let cancelled = false;
    /** 读取团队或个人模型；无参数/返回，卸载后不写状态，失败保留既有选择供用户继续编辑。 */
    async function loadModels() {
      if (!accessToken || !userID || !userRole) return;
      try {
        const models =
          keyData.team_id && team
            ? [...(team.models ?? []), ...(await fetchTeamModels(userID, userRole, accessToken, team.team_id))]
            : (await modelAvailableCall(accessToken, userID, userRole)).data.map((model: { id: string }) => model.id);
        if (!cancelled) {
          setAvailableModels(excludeProxyWideSentinel([...new Set<string>(models)]));
          setModelsError(false);
        }
      } catch {
        if (!cancelled) {
          setAvailableModels([]);
          setModelsError(true);
        }
      }
    }
    void loadModels();
    return () => {
      cancelled = true;
    };
  }, [accessToken, userID, userRole, keyData.team_id, team]);

  /** 保存已验证的基础字段，返回保存 Promise；省略未变更路由及空期限，异常仍释放保存锁。 */
  async function save(values: KeyEditFormValues) {
    setSaving(true);
    try {
      const payload: Record<string, unknown> = {
        token: keyData.token_id || keyData.token,
        key_alias: values.key_alias,
        models: values.models,
        max_budget: values.max_budget,
        rpm_limit: values.rpm_limit,
        tpm_limit: values.tpm_limit,
        route_template_id: values.route_template_id,
      };
      if (values.allowed_routes !== (keyData.allowed_routes ?? []).join(","))
        payload.allowed_routes = parseAllowedRoutes(values.allowed_routes);
      if (values.duration?.trim()) payload.duration = values.duration.trim();
      await onSubmit(payload);
    } finally {
      setSaving(false);
    }
  }

  /** 将可见类型写入表单；输入类型值，返回无，管理类型清除模型，供类型选择控件调用。 */
  function changeKeyType(type: string) {
    let allowedRoutes = "";
    if (type === "llm_api") allowedRoutes = "llm_api_routes";
    else if (type === "management") allowedRoutes = "management_routes";
    form.setValue("allowed_routes", allowedRoutes);
    if (type === "management") form.setValue("models", []);
  }

  const modelOptions = [
    ...modelSentinelOptions(keyData.team_id, !!team),
    ...availableModels.map((model) => ({
      value: model,
      label: model,
      disabled: hasAllModelsSentinel(selectedModels),
    })),
  ];

  return (
    <form
      noValidate
      className="flex h-full min-h-0 flex-col overflow-hidden rounded-xl border bg-background"
      onSubmit={form.handleSubmit(save)}
    >
      <div
        role="region"
        aria-label={t("Key Settings")}
        tabIndex={0}
        className="key-details-scrollbar min-h-0 flex-1 overflow-x-hidden overflow-y-scroll p-5 sm:p-6"
        style={{ scrollbarGutter: "stable" }}
      >
        {header}
        <div className="mt-6">
          <FieldGroup className="gap-8">
            <section className="space-y-5">
              <h2 className="text-base font-semibold">{t("keyEditor.basics")}</h2>
              <FormField control={form.control} name="key_alias" label={t("Key Alias")}>
                {(field) => <Input {...field} value={field.value ?? ""} />}
              </FormField>
              <FormField
                control={form.control}
                name="models"
                label={t("Models")}
                description={t(modelsDisabled ? "Models field is disabled for this key type" : "keyEditor.modelsHint")}
              >
                {({ value, onChange, id }) => (
                  <MultiSelect
                    id={id}
                    options={modelOptions}
                    value={modelsDisabled ? [] : (value ?? [])}
                    disabled={modelsDisabled}
                    placeholder={t("Select models")}
                    onValueChange={(next) => {
                      if (next.includes("all-team-models")) onChange(["all-team-models"]);
                      else if (next.includes("all-proxy-models")) onChange(["all-proxy-models"]);
                      else onChange(next);
                    }}
                  />
                )}
              </FormField>
              {modelsError && (
                <p role="alert" className="text-sm text-destructive">
                  {t("keyEditor.modelsError")}
                </p>
              )}
              <FormField control={form.control} name="allowed_routes" label={t("Key Type")}>
                {({ id }) => <KeyTypeSelect id={id} value={keyTypeFromRoutes(routes)} onChange={changeKeyType} />}
              </FormField>
            </section>
            <section className="space-y-5 border-t pt-6">
              <h2 className="text-base font-semibold">{t("Budget and Routing")}</h2>
              <p className="text-sm text-muted-foreground">{t("keyEditor.budgetHint")}</p>
              <div className="grid gap-5 sm:grid-cols-3">
                <FormField control={form.control} name="max_budget" label={t("Max Budget (USD)")}>
                  {({ ref: _ref, ...field }) => (
                    <NumericalInput
                      {...field}
                      value={field.value ?? ""}
                      min={0}
                      placeholder={t("keyEditor.budgetPlaceholder")}
                    />
                  )}
                </FormField>
                {(["rpm_limit", "tpm_limit"] as const).map((name) => (
                  <FormField
                    key={name}
                    control={form.control}
                    name={name}
                    label={t(name === "rpm_limit" ? "RPM Limit" : "TPM Limit")}
                  >
                    {({ ref: _ref, ...field }) => (
                      <NumericalInput
                        {...field}
                        value={field.value ?? ""}
                        min={0}
                        max={2147483647}
                        step={1}
                        placeholder={t("quotaGuide.rateBlank")}
                      />
                    )}
                  </FormField>
                ))}
                <FormField
                  control={form.control}
                  name="route_template_id"
                  label={t("pages.routeTemplates.title")}
                  className="sm:col-span-3"
                >
                  {({ value, onChange }) => (
                    <RouteTemplateSelect
                      accessToken={accessToken}
                      value={value ?? ""}
                      onChange={onChange}
                      scope="key"
                      scopeId={keyData.token_id || keyData.token}
                    />
                  )}
                </FormField>
              </div>
            </section>
            <section className="space-y-5 border-t pt-6">
              <h2 className="text-base font-semibold">{t("keyEditor.lifecycle")}</h2>
              <div>
                <FormField control={form.control} name="duration">
                  {({ value, onChange, id }) => (
                    <KeyLifecycleSettings
                      key={keyData.token_id || keyData.token}
                      id={id}
                      value={value ?? ""}
                      onChange={onChange}
                      rotationSupported={false}
                      autoRotationEnabled={false}
                      onAutoRotationChange={(enabled) => form.setValue("auto_rotate", enabled)}
                      rotationInterval={form.watch("rotation_interval") ?? "30d"}
                      onRotationIntervalChange={(interval) => form.setValue("rotation_interval", interval)}
                    />
                  )}
                </FormField>
              </div>
            </section>
          </FieldGroup>
        </div>
      </div>
      {/* 操作栏独立占据布局空间，不能叠在可滚动字段上方。 */}
      <div
        className="shrink-0 border-t border-border bg-background px-5 py-4 sm:px-6 flex justify-end gap-2"
        role="group"
        aria-label={t("keyEditor.actions")}
      >
        <Button type="button" variant="secondary" onClick={onCancel} disabled={saving}>
          {t("Cancel")}
        </Button>
        <Button type="submit" disabled={saving} aria-busy={saving}>
          {saving && <UiLoadingSpinner className="size-4" />}
          {t("Save Changes")}
        </Button>
      </div>
    </form>
  );
}
