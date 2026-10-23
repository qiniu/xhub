"use client";

import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useT } from "@/i18n/I18nProvider";
import type { ProbeForm, ProbeKind } from "./probeForm";

/** 表单字段参数；change 仅改变内存草稿，disabled 在真实请求进行中防止错配配置。 */
interface ProbeFieldsProps {
  kind: ProbeKind;
  form: ProbeForm;
  change: (field: keyof ProbeForm, value: string) => void;
  disabled: boolean;
}

/** 渲染两类探测的明确配置字段；props 由目录页传入，无网络调用；示例和用户内容不随语言改变。 */
export function ProbeFields({ kind, form, change, disabled }: ProbeFieldsProps) {
  const t = useT();
  return (
    <fieldset disabled={disabled} className="space-y-4">
      <label className="block space-y-1"><span>{t("probes.name")}</span>
        <Input value={form.name} onChange={(e) => change("name", e.target.value)} />
      </label>
      {kind === "prompts" ? <>
        <label className="block space-y-1"><span>{t("probes.model")}</span>
          <Input value={form.model} onChange={(e) => change("model", e.target.value)} />
        </label>
        <label className="block space-y-1"><span>{t(form.contentMode ? "probes.content" : "probes.messages")}</span>
          <Textarea rows={6} value={form.messages} onChange={(e) => change("messages", e.target.value)} />
        </label>
        <label className="block space-y-1"><span>{t("probes.variables")}</span>
          <Textarea rows={3} value={form.variables} onChange={(e) => change("variables", e.target.value)} />
        </label>
        {form.unsupported ? <p role="alert">{t("probes.unsupportedPrompt")}</p> : null}
      </> : <>
        <label className="block space-y-1"><span>{t("probes.provider")}</span>
          <select className="h-9 w-full rounded border bg-background px-3" value={form.provider} onChange={(e) => change("provider", e.target.value)}>
            {!["tavily", "perplexity", "serper"].includes(form.provider) ? <option value={form.provider}>{form.provider}</option> : null}
            <option value="tavily">Tavily</option><option value="perplexity">Perplexity</option><option value="serper">Serper</option>
          </select>
        </label>
        <label className="block space-y-1"><span>{t("probes.apiBase")}</span>
          <Input value={form.apiBase} onChange={(e) => change("apiBase", e.target.value)} aria-describedby="probe-api-base-help" />
        </label>
        <p id="probe-api-base-help" className="text-sm text-muted-foreground">{t("probes.apiBaseHelp")}</p>
        <label className="block space-y-1"><span>{t("probes.apiKey")}</span>
          <Input type="password" autoComplete="new-password" value={form.apiKey} onChange={(e) => change("apiKey", e.target.value)} aria-describedby="probe-api-key-help" />
        </label>
        <p id="probe-api-key-help" className="text-sm text-muted-foreground">{t("probes.apiKeyHelp")}</p>
        <label className="block space-y-1"><span>{t("probes.query")}</span>
          <Input value={form.query} onChange={(e) => change("query", e.target.value)} />
        </label>
      </>}
    </fieldset>
  );
}
