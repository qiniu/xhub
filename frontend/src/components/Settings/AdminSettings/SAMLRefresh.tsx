"use client";
import React, { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/i18n";
import { readEnterprise, saveEnterprise, refreshSSOMetadata, type SAMLRefreshStatus } from "@/lib/http/enterprise";

/** 已保存 SAML 连接的刷新设置；id 为后台连接标识，独立保存避免覆盖表单中的身份材料。
 * 后台持久化并执行调度，浏览器只显示快照；读取失败不提供假默认值，秘密不进入浏览器存储。
 */
export default function SAMLRefresh({ id }: { id: string }) {
  const t = useT();
  const path = "/admin/enterprise/sso/" + encodeURIComponent(id) + "/refresh";
  const [status, setStatus] = useState<SAMLRefreshStatus | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [source, setSource] = useState("");
  const [hours, setHours] = useState("6");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  /** 将服务端已确认设置和状态同步到表单；仅显式加载/保存调用，无写入副作用。 */
  function apply(next: SAMLRefreshStatus) {
    setStatus(next);
    setEnabled(next.enabled);
    setSource(next.source_url);
    setHours(String(next.interval_hours));
  }
  useEffect(() => {
    let active = true;
    readEnterprise<SAMLRefreshStatus>(path)
      .then((next) => {
        if (active) apply(next);
      })
      .catch(() => {
        if (active) setMessage("error");
      });
    return () => {
      active = false;
    };
  }, [path]);
  /** 读取当前调度快照；按钮触发，失败保留旧数据显示错误，不自动重复写入。 */
  async function load() {
    setBusy(true);
    setMessage("");
    try {
      apply(await readEnterprise<SAMLRefreshStatus>(path));
    } catch {
      setMessage("error");
    } finally {
      setBusy(false);
    }
  }
  /** 保存独立设置或手动刷新；成功回读，刷新失败也回读具体原因，仍显示失败状态。 */
  async function run(action: "save" | "refresh") {
    setBusy(true);
    setMessage("");
    try {
      if (action === "save") await saveEnterprise(path, { enabled, source_url: source, interval_hours: Number(hours) });
      else await refreshSSOMetadata(id);
      apply(await readEnterprise<SAMLRefreshStatus>(path));
      setMessage(action === "save" ? "saved" : "refreshed");
    } catch {
      setMessage("error");
      try {
        setStatus(await readEnterprise<SAMLRefreshStatus>(path));
      } catch {
        /* 保留已取得的快照，错误提示不被覆盖。 */
      }
    } finally {
      setBusy(false);
    }
  }
  /** 格式化服务器 Unix 秒；零表示尚未执行，真实时间按浏览器本地时区显示。 */
  function time(value: number) {
    return value ? new Date(value * 1000).toLocaleString() : t("samlRefresh.never");
  }
  const sameSource = status?.source_url === source && String(status?.interval_hours) === hours;
  const unchanged = !!status && status.enabled === enabled && sameSource;
  const invalidHours = !Number.isInteger(Number(hours)) || Number(hours) < 1 || Number(hours) > 24;
  const missingSource = enabled && !source;
  const saveDisabled = busy || invalidHours || missingSource;
  const knownErrors = [
    "invalid_baseline",
    "invalid_metadata",
    "invalid_expiry",
    "trust_changed",
    "fetch_failed",
    "invalid_configuration",
  ];
  return (
    <section className="mt-5 space-y-3 rounded-md border p-4" aria-label={t("samlRefresh.title")}>
      <h4>{t("samlRefresh.title")}</h4>
      <p className="text-sm text-muted-foreground">{t("samlRefresh.hint")}</p>
      <Button type="button" variant="outline" disabled={busy} onClick={load}>
        {t("samlRefresh.load")}
      </Button>
      {status && (
        <>
          <div className="flex items-center gap-2">
            <Switch
              aria-label={t("samlRefresh.enabled")}
              disabled={busy}
              checked={enabled}
              onCheckedChange={setEnabled}
            />
            <span>{t("samlRefresh.enabled")}</span>
          </div>
          <label>
            {t("samlRefresh.source")}
            <Input
              type="url"
              disabled={busy}
              value={source}
              onChange={(e) => setSource(e.target.value)}
              placeholder="https://idp.example.com/metadata"
            />
          </label>
          <label>
            {t("samlRefresh.interval")}
            <Input
              type="number"
              min={1}
              max={24}
              step={1}
              disabled={busy}
              value={hours}
              onChange={(e) => setHours(e.target.value)}
            />
          </label>
          <div className="flex gap-2">
            <Button type="button" disabled={saveDisabled} onClick={() => run("save")}>
              {t("samlRefresh.save")}
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={busy || !unchanged || !status.source_url}
              onClick={() => run("refresh")}
            >
              {t("samlRefresh.now")}
            </Button>
          </div>
          <dl>
            {(["last_attempt", "last_success", "next_attempt", "expires_at"] as const).map((key) => (
              <div key={key}>
                <dt>{t("samlRefresh." + key)}</dt>
                <dd>{key === "next_attempt" && !status.enabled ? t("samlRefresh.off") : time(status[key])}</dd>
              </div>
            ))}
          </dl>
          {!!status.error_code && (
            <p role="alert">
              {t("samlRefresh.errors." + (knownErrors.includes(status.error_code) ? status.error_code : "unknown"))}
            </p>
          )}
          <p>{t("samlRefresh.snapshot")}</p>
        </>
      )}
      {!!message && <p role={message === "error" ? "alert" : "status"}>{t("samlRefresh." + message)}</p>}
    </section>
  );
}
