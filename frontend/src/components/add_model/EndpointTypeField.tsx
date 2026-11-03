import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { ChevronDown } from "lucide-react";
import { useFormContext, useWatch } from "react-hook-form";
import { useT } from "@/i18n";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectLabel,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { MountedFormField, type MountedFormValues } from "../common_components/MountedFormField";
import { apiClient } from "../networking";
import { provider_map } from "../provider_info_helpers";
import {
  catalogTransports,
  defaultTransport,
  compatibleEndpoint,
  protocolLabel,
  endpointLabel,
  upstreamProtocolGroup,
  automaticEndpoint,
  defaultEndpoints,
  type EndpointDescriptor,
  type EndpointTransport,
} from "./endpointCatalog";

/** 公开目录仅声明已实现的上游操作和固定模型绑定，不用定价信息猜测能力。 */
interface EndpointPayload {
  endpoint_types?: EndpointDescriptor[];
  transports?: EndpointTransport[];
  models?: Record<string, string>;
  catalogs?: Record<string, Record<string, string[]>>;
}

/** 模型编辑先选择上游实际支持的调用方式，再展示由执行能力生成的固定 XHub 接口。
 * 参数 selectedProvider 为连接协议，catalogId 为已保存供应商目录，modelCostMap 为兼容旧调用方的可选价格目录（不用于推断协议）；
 * 返回上游选择与默认折叠的公开接口说明，供 ModelEditor 使用，展开后可设置可选直通入口。
 * 普通型号按连接默认选择协议；折叠只控制显示，不改变部署声明，专用型号不匹配时直接显示原因，目录失败保留草稿。 */
const EndpointTypeField: React.FC<{
  selectedProvider: string | null;
  catalogId?: string;
  modelCostMap?: Record<string, { endpoint_id?: string }> | null;
}> = ({ selectedProvider, catalogId = "" }) => {
  const t = useT();
  const { control, getValues, setValue } = useFormContext<MountedFormValues>();
  const lastModel = useRef<unknown>(undefined);
  const [payload, setPayload] = useState<EndpointPayload>({});
  const [state, setState] = useState<"loading" | "ready" | "failed">("loading");
  const [endpointsExpanded, setEndpointsExpanded] = useState(false);
  const endpointsPanelId = useId();
  const transportId = useWatch({ control, name: "transport" }) as string | undefined;
  const entries = useWatch({ control, name: "endpoint_types" }) as string[] | undefined;
  const modelValue = useWatch({ control, name: "model" });
  const picked = Array.isArray(modelValue) ? modelValue[0] : modelValue;
  const slug = selectedProvider ? (provider_map[selectedProvider] ?? selectedProvider.toLowerCase()) : "";
  useEffect(() => {
    let canceled = false;
    apiClient
      .get<EndpointPayload>("/public/endpoints")
      .then((body) => {
        if (!canceled && body) {
          setPayload(body);
          setState("ready");
        }
      })
      .catch(() => {
        if (!canceled) setState("failed");
      });
    return () => {
      canceled = true;
    };
  }, []);
  const transports = useMemo(
    () =>
      catalogTransports(
        payload.transports ?? [],
        payload.catalogs ?? {},
        catalogId,
        typeof picked === "string" ? picked : "",
        slug,
      ),
    [payload.transports, payload.catalogs, catalogId, picked, slug],
  );
  // 选择菜单展示连接可用的全部已实现协议；型号限制在选择后明确解释，并由保存接口再次校验。
  const choices = useMemo(
    () => (payload.transports ?? []).filter((item) => !item.providers?.length || item.providers.includes(slug)),
    [payload.transports, slug],
  );
  const binding = choices.find((item) => item.id === transportId);
  const supported = !binding || transports.some((item) => item.id === binding.id);
  const bareModel =
    typeof picked === "string" && catalogId && picked.startsWith(catalogId + "/")
      ? picked.slice(catalogId.length + 1)
      : String(picked ?? "");
  const declaredTransports = payload.catalogs?.[catalogId]?.[bareModel];
  const unavailableReason = declaredTransports?.length
    ? t(
        "The current connection type {protocol} does not support this model's registered upstream protocol, so it cannot be saved. Check the provider connection type.",
        { protocol: slug },
      )
    : t(
        "The catalog does not provide an available upstream protocol, so this model cannot be saved. Check the upstream model ID and provider connection type.",
      );
  const available = (payload.endpoint_types ?? []).filter((entry) => compatibleEndpoint(entry, binding));
  const automatic = available.filter(automaticEndpoint);
  const bypass = available.filter((entry) => !automaticEndpoint(entry));
  /** 写入上游执行及可用公开接口；参数为注册执行 ID，返回无。
   * 自动开放标准及匹配的三种对话透传接口，其他 Bypass 保留显式勾选；空值清空旧声明，供选择和模型切换调用。 */
  const selectTransport = useCallback(
    (id: string, dirty = true) => {
      const execution = choices.find((item) => item.id === id);
      const endpoints = defaultEndpoints(payload.endpoint_types ?? [], execution);
      if (getValues("transport") !== id) setValue("transport", id, { shouldDirty: dirty });
      if (JSON.stringify(getValues("endpoint_types")) !== JSON.stringify(endpoints))
        setValue("endpoint_types", endpoints, { shouldDirty: dirty });
    },
    [getValues, setValue, choices, payload.endpoint_types],
  );
  useEffect(() => {
    if (state !== "ready") return;
    const changed = lastModel.current !== undefined && lastModel.current !== picked;
    lastModel.current = picked;
    const current = getValues("transport") as string;
    if (picked && !changed && choices.some((item) => item.id === current)) return;
    const bare =
      typeof picked === "string" && picked.startsWith(catalogId + "/") ? picked.slice(catalogId.length + 1) : picked;
    const declared = catalogId
      ? payload.catalogs?.[catalogId]?.[String(bare)]?.[0]
      : typeof picked === "string"
        ? (payload.models?.[picked] ?? payload.models?.[slug + "/" + picked])
        : undefined;
    selectTransport(picked ? defaultTransport(transports, slug, declared) : "", false);
  }, [
    state,
    picked,
    slug,
    catalogId,
    transports,
    choices,
    payload.models,
    payload.catalogs,
    getValues,
    selectTransport,
  ]);
  return (
    <>
      <MountedFormField label={t("Upstream protocol")} name="transport" className="mb-1">
        {(field) => (
          <Select
            items={choices.map((item) => ({ value: item.id, label: protocolLabel(item) }))}
            value={transportId ?? ""}
            disabled={!picked || state !== "ready" || !choices.length}
            onValueChange={(value) => selectTransport(value ?? "")}
          >
            <SelectTrigger id={field.id} aria-label={t("Upstream protocol")}>
              <SelectValue
                placeholder={
                  state === "loading"
                    ? t("Loading endpoint catalog…")
                    : t("Select a call method supported by the upstream provider")
                }
              />
            </SelectTrigger>
            <SelectContent>
              {(["openai", "vertex", "claude", "bypass"] as const).map((group) => (
                <SelectGroup key={group}>
                  <SelectLabel>
                    {
                      {
                        openai: "OpenAI",
                        vertex: "Vertex / Gemini",
                        claude: "Claude / Anthropic",
                        bypass: t("Bypass forwarding (including Fal)"),
                      }[group]
                    }
                  </SelectLabel>
                  {choices
                    .filter((item) => upstreamProtocolGroup(item) === group)
                    .map((item) => (
                      <SelectItem key={item.id} value={item.id}>
                        {protocolLabel(item)}
                      </SelectItem>
                    ))}
                </SelectGroup>
              ))}
            </SelectContent>
          </Select>
        )}
      </MountedFormField>
      <p className="text-sm text-muted-foreground mb-5">
        {t("Preselected for the provider. Switch it to match upstream support.")}
      </p>
      {state === "failed" && (
        <p role="alert">
          {t("Failed to load the endpoint catalog. Reopen the editor. Saved endpoint declarations were preserved.")}
        </p>
      )}
      {state === "ready" && picked && !binding && (
        <p role="status" className="mb-5">
          {choices.length
            ? t(
                "Select a call protocol supported by the upstream provider. Ordinary models do not require dedicated capabilities.",
              )
            : unavailableReason}
        </p>
      )}
      {binding && !supported && (
        <p role="alert" className="mb-5 text-destructive">
          {t(
            "The selected {protocol} protocol does not support the current model or catalog. Enter an upstream model path supported by this protocol, or select a standard protocol such as OpenAI or Vertex / Gemini. This dedicated capability is validated when saving.",
            { protocol: protocolLabel(binding) },
          )}
        </p>
      )}
      {state === "ready" && binding?.protocol === "fal" && (
        <p className="text-sm text-muted-foreground mb-5">
          {t("Enter a FAL task path as the upstream model, for example byteplus/seedance-2.0/text-to-video.")}
        </p>
      )}
      {binding && (
        <section aria-label={t("XHub public endpoints")} className="mb-5 min-w-0 rounded-lg border bg-muted/20">
          <button
            type="button"
            aria-expanded={endpointsExpanded}
            aria-controls={endpointsPanelId}
            className="flex w-full items-center justify-between gap-3 rounded-lg px-4 py-3 text-left text-sm font-medium hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => setEndpointsExpanded((expanded) => !expanded)}
          >
            {t("XHub public endpoints")}
            <ChevronDown
              aria-hidden="true"
              className={`size-4 shrink-0 transition-transform ${endpointsExpanded ? "rotate-180" : ""}`}
            />
          </button>
          {/* 保留已挂载的接口选项，折叠期间仍保存自动开放和用户勾选的声明；hidden 同时移出键盘和辅助技术导航。 */}
          <div id={endpointsPanelId} hidden={!endpointsExpanded} className="space-y-4 px-4 pb-4">
            <p className="text-xs leading-relaxed text-muted-foreground">
              {automatic.length
                ? t("modelEndpoints.automaticDescription")
                : t(
                    "This model uses a provider-specific protocol. The passthrough endpoints below are enabled from registered capabilities.",
                  )}
            </p>
            <ul aria-label={t("Enabled endpoints")} className="space-y-3">
              {automatic.map((entry) => (
                <li key={entry.id} aria-label={endpointLabel(entry)} className="min-w-0 rounded-md border bg-background p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="text-sm font-medium">{endpointLabel(entry)}</span>
                    <span className="rounded bg-primary/10 px-2 py-0.5 text-xs text-primary">{t("Enabled")}</span>
                  </div>
                  <div className="mt-2 space-y-1.5">
                    {entry.paths?.map((path) => (
                      <code
                        key={path}
                        className="block break-all rounded bg-muted/60 px-2 py-1 font-mono text-xs leading-relaxed text-muted-foreground"
                      >
                        {path}
                      </code>
                    ))}
                  </div>
                </li>
              ))}
            </ul>
            {bypass.length > 0 && (
              <div className="space-y-2 border-t pt-3">
                <p className="text-xs font-medium text-muted-foreground">{t("Optional passthrough endpoints")}</p>
                {bypass.map((entry) => (
                  <label
                    key={entry.id}
                    className="flex cursor-pointer items-start gap-3 rounded-md border bg-background p-3 text-sm"
                  >
                    <input
                      type="checkbox"
                      className="mt-0.5 size-4 shrink-0 accent-primary"
                      aria-label={endpointLabel(entry)}
                      checked={entries?.includes(entry.id) ?? false}
                      onChange={(event) => {
                        const next = event.target.checked
                          ? [...new Set([...(entries ?? []), entry.id])]
                          : (entries ?? []).filter((id) => id !== entry.id);
                        // 仅有 Bypass 的供应商必须保留入口，否则部署不能调用。
                        if (next.length) setValue("endpoint_types", next, { shouldDirty: true });
                      }}
                    />
                    <span className="min-w-0 flex-1 space-y-1">
                      <span className="block font-medium">{endpointLabel(entry)}</span>
                      {entry.paths?.map((path) => (
                        <code
                          key={path}
                          className="block break-all font-mono text-xs leading-relaxed text-muted-foreground"
                        >
                          {path}
                        </code>
                      ))}
                    </span>
                  </label>
                ))}
              </div>
            )}
          </div>
        </section>
      )}
    </>
  );
};
export default EndpointTypeField;
