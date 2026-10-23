"use client";

import { useCallback, useEffect, useState } from "react";
import useAuthorized from "@/app/(dashboard)/hooks/useAuthorized";
import { apiClient } from "@/components/networking";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n/I18nProvider";
import { ProbeFields } from "./ProbeFields";
import { emptyProbeForm, formFromRecord, probeConfiguration, probeObject, probeSaveBody, probeTestBody, validatedProbeResult, type ProbeForm, type ProbeKind } from "./probeForm";
import { probeErrorFeedback, type ProbeFeedback } from "./probeError";

/** 提示词/搜索测试目录；kind 选择已有接口。保存、读回、删除和测试均走共享 HTTP 客户端，不制造探测成功结果。 */
export function ProbeCatalog({ kind }: { kind: ProbeKind }) {
  const { accessToken } = useAuthorized();
  const t = useT();
  const [form, setForm] = useState<ProbeForm>(emptyProbeForm);
  const [rows, setRows] = useState<Record<string, unknown>[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saved, setSaved] = useState<unknown>();
  const [savedConfiguration, setSavedConfiguration] = useState("");
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<ProbeFeedback | null>(null);
  const [result, setResult] = useState<Record<string, unknown> | null>(null);
  const path = `/${kind}`;
  const dirty = !form.id || savedConfiguration !== probeConfiguration(form);

  /** 显示解析或接口错误；不记录包含凭据的请求正文，翻译键与原始服务端错误分开处理。 */
  const showError = useCallback((error: unknown) => {
    setFeedback(probeErrorFeedback(kind, error));
  }, [kind]);

  /** 读取真实目录列表；缺登录或响应格式错误明确失败，不把不可读目录显示成空列表。 */
  const load = useCallback(async () => {
    if (!accessToken) throw new Error("probes.noSession");
    const data: unknown = await apiClient.get(`${path}/list`, { accessToken });
    const list = probeObject(data)?.data;
    if (!Array.isArray(list) || list.some((row) => !probeObject(row))) throw new Error("probes.invalidRecord");
    return list as Record<string, unknown>[];
  }, [accessToken, path]);

  useEffect(() => {
    if (!accessToken) return;
    let active = true;
    void load().then((list) => {
      if (active) { setRows(list); setLoaded(true); }
    }).catch((error: unknown) => { if (active) showError(error); });
    return () => { active = false; };
  }, [accessToken, load, showError]);

  /** 用户操作后刷新列表；仅在服务端返回有效列表后更新页面，读取故障交由调用操作显示。 */
  const refresh = async () => {
    const list = await load();
    setRows(list); setLoaded(true);
  };

  /** 包裹用户触发的异步操作；失败只显示失败信息，finally 释放按钮，避免并发重复执行测试或保存。 */
  const run = async (task: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setFeedback(null);
    setResult(null);
    try {
      if (!accessToken) throw new Error("probes.noSession");
      await task();
    } catch (error) { showError(error); } finally { setBusy(false); }
  };

  /** 从真实详情填充草稿；record 必须带有效 ID，密钥永不回填，保存指纹用于判断当前测试对象。 */
  const acceptRecord = (record: unknown) => {
    const next = formFromRecord(kind, record);
    setForm(next);
    setSaved(record);
    setSavedConfiguration(probeConfiguration(next));
  };

  /** 切换已保存配置；空 ID 只新建内存草稿，其他 ID 编码后读取详情，不以列表内容冒充最新配置。 */
  const selectRecord = async (id: string) => {
    if (!id) {
      setForm(emptyProbeForm()); setSaved(undefined); setSavedConfiguration(""); setFeedback(null); setResult(null);
      return;
    }
    await run(async () => { acceptRecord(await apiClient.get(`${path}/${encodeURIComponent(id)}`, { accessToken })); });
  };

  /** 保存完整可执行配置；新建走 POST，编辑保留既有 PATCH/PUT 契约，成功刷新列表但不宣称测试成功。 */
  const saveRecord = async () => run(async () => {
    const body = probeSaveBody(kind, form, saved);
    const updateMethod = kind === "prompts" ? "PATCH" : "PUT";
    const method = form.id ? updateMethod : "POST";
    const url = form.id ? `${path}/${encodeURIComponent(form.id)}` : path;
    const record: unknown = await apiClient.request(method, url, { accessToken, body });
    acceptRecord(record);
    await refresh();
    setFeedback({ key: "probes.saved" });
  });

  /** 删除当前保存对象；仅真实 DELETE 成功后清空表单，随后刷新；测试记录不写入用户配置。 */
  const deleteRecord = async () => run(async () => {
    await apiClient.delete(`${path}/${encodeURIComponent(form.id)}`, { accessToken });
    setForm(emptyProbeForm()); setSaved(undefined); setSavedConfiguration("");
    await refresh();
    setFeedback({ key: "probes.deleted" });
  });

  /** 调用真实探测接口并验证结果结构；未保存草稿、错误响应或旧占位响应均不能进入成功状态。 */
  const testRecord = async () => run(async () => {
    if (dirty) throw new Error("probes.dirty");
    const suffix = kind === "prompts" ? "test" : "test_connection";
    const data: unknown = await apiClient.post(`${path}/${suffix}`, { accessToken, body: probeTestBody(kind, form) });
    setResult(validatedProbeResult(kind, data));
    setFeedback({ key: "probes.completed" });
  });

  /** 更新单个草稿字段并使上一次测试结果失效；用户输入原文保留，避免旧成功状态误指向新内容。 */
  const change = (field: keyof ProbeForm, value: string) => {
    setForm((current) => ({ ...current, [field]: value }));
    setResult(null); setFeedback(null);
  };

  const usage = probeObject(result?.usage);
  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <h1 className="text-2xl font-semibold">{t(kind === "prompts" ? "probes.promptsTitle" : "probes.searchTitle")}</h1>
      <p>{t(kind === "prompts" ? "probes.promptHelp" : "probes.searchHelp")}</p>
      <label className="block space-y-1"><span>{t("probes.record")}</span>
        <select className="h-9 w-full rounded border bg-background px-3" value={form.id} disabled={busy} onChange={(e) => void selectRecord(e.target.value)}>
          <option value="">{t("probes.new")}</option>
          {rows.map((row) => {
            const id = String(row[kind === "prompts" ? "prompt_id" : "search_tool_id"] ?? row.id ?? "");
            return <option key={id} value={id}>{String(row.name ?? row.search_tool_name ?? id)}</option>;
          })}
        </select>
      </label>
      <Button type="button" variant="outline" disabled={busy} onClick={() => void run(refresh)}>{t("probes.refresh")}</Button>
      {loaded && rows.length === 0 ? <p>{t("probes.noRecords")}</p> : null}
      <ProbeFields kind={kind} form={form} change={change} disabled={busy} />
      <div className="flex flex-wrap gap-2">
        <Button type="button" disabled={busy || form.unsupported} onClick={() => void saveRecord()}>{t(form.id ? "probes.save" : "probes.create")}</Button>
        <Button type="button" disabled={busy || !form.id} onClick={() => void testRecord()}>{t("probes.test")}</Button>
        <Button type="button" variant="outline" disabled={busy || !form.id} onClick={() => void deleteRecord()}>{t("probes.delete")}</Button>
      </div>
      {dirty && form.id ? <p>{t("probes.dirty")}</p> : null}
      {busy ? <p role="status">{t("probes.working")}</p> : null}
      {feedback ? <p role={feedback.failed ? "alert" : "status"}>{t(feedback.key, feedback.vars)}</p> : null}
      {result ? <section aria-label={t("probes.result")} className="space-y-2">
        <h2 className="font-semibold">{t("probes.result")}</h2>
        {kind === "search_tools" ? <p>{t("probes.resultsCount", { count: result.results_count })}</p> : null}
        {usage ? <p>{t("probes.usage", { input: usage.prompt_tokens, output: usage.completion_tokens })}</p> : null}
        <pre data-testid="probe-result" className="max-h-96 overflow-auto whitespace-pre-wrap rounded bg-muted p-3 text-xs">{JSON.stringify(result, null, 2)}</pre>
      </section> : null}
    </div>
  );
}
