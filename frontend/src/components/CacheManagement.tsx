"use client";

import { useEffect, useState } from "react";
import { apiClient } from "@/components/networking";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";

/** 展示实际进程缓存并提供原有清理能力；accessToken为当前登录凭据，返回双语管理视图。
 * 不提供未接执行器的配置表单；接口失败或未知响应不得显示成功，卸载/切换身份忽略旧查询。
 */
export function CacheManagement({ accessToken }: { accessToken: string | null }) {
  const t = useT();
  const [generation, setGeneration] = useState(0);
  const [status, setStatus] = useState<{ token: string; ready: boolean } | null>(null);
  const [result, setResult] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    /** 读取运行状态；无参数返回Promise；失败保持不可清理，忽略卸载后的结果，不改配置。 */
    async function load() {
      if (!accessToken) return;
      try {
        const data = await apiClient.get<{ enabled: boolean; cache_type: string; scope: string; configurable: boolean }>("/cache/settings", { accessToken });
        const processMemory = data.cache_type === "memory" && data.scope === "process";
        const ready = processMemory && data.enabled === true && data.configurable === false;
        if (!cancelled) setStatus({ token: accessToken, ready });
      } catch {
        if (!cancelled) setStatus({ token: accessToken, ready: false });
      }
    }
    void load();
    return () => { cancelled = true; };
  }, [accessToken, generation]);

  /** 清理当前进程响应缓存；点击调用，无参数；只有原成功契约确认后显示成功，不改预算和用量。 */
  async function flush() {
    if (!accessToken || busy) return;
    setBusy(true);
    setResult("");
    try {
      const data = await apiClient.post<{ status: string; message: string }>("/flushall", { accessToken, body: {} });
      setResult(data.status === "ok" && data.message === "cache flushed" ? "flushed" : "flushFailed");
    } catch {
      setResult("flushFailed");
    } finally {
      setBusy(false);
    }
  }

  /** 手动重试状态查询；清除旧状态与提示后递增查询版本，无参数及返回，不执行清理。 */
  function refresh() {
    setStatus(null);
    setResult("");
    setGeneration((value) => value + 1);
  }

  const current = status?.token === accessToken ? status : null;
  let stateKey = "loading";
  if (current) stateKey = current.ready ? "ready" : "loadFailed";
  return <section className="space-y-4 p-6">
    <h1 className="text-2xl font-semibold">{t("cacheAdmin.title")}</h1>
    <p>{t("cacheAdmin.scope")}</p>
    <p>{t("cacheAdmin.unsupported")}</p>
    <p role={current && !current.ready ? "alert" : "status"}>{t(`cacheAdmin.${stateKey}`)}</p>
    <div className="flex gap-2">
      <Button onClick={refresh} disabled={busy}>{t("cacheAdmin.refresh")}</Button>
      <Button onClick={() => void flush()} disabled={!current?.ready || busy}>{t(`cacheAdmin.${busy ? "flushing" : "flush"}`)}</Button>
    </div>
    {result && <p role={result === "flushFailed" ? "alert" : "status"} data-testid="cache-result">{t(`cacheAdmin.${result}`)}</p>}
  </section>;
}
