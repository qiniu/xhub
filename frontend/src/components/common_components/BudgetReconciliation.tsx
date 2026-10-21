import { useEffect, useState } from "react";
import { useT } from "@/i18n";
import { apiClient } from "../networking";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "../ui/dialog";

/** 管理会话可读的预占快照；fingerprint用于并发校验，金额是USD，不包含Key或供应商秘密。 */
export interface ReconciliationReservation {
  reservation_id: string; call_id: string; key_id: string; amount: number; state: string;
  started_at: string; fingerprint: string; model: string; supplier: string; upstream_model?: string;
  ledger_cost: number | null; can_reconcile: boolean; blocked_reason: string;
}
/** 持久化审计回执；保留后台核定差额、操作者及时间，不能用前端输入冒充成功。 */
interface Resolution {
  reservation_id: string; call_id: string; outcome: "no_charge" | "charged";
  verified_cost_usd: number; cost_delta_usd: number; previous_cost_usd: number; updated_cost_usd: number;
  evidence: string; actor_id: string; resolved_at: string;
}
interface ReservationPage { reservations: ReconciliationReservation[]; total: number; page: number; page_size: number }
const blockedReasons = new Set(["state_not_uncertain", "event_missing", "event_not_terminal", "active_attempt", "pending_usage", "identity_mismatch"]);

/** 在平台管理员Key详情中查询并人工核对未决预占；参数含当前Key、会话、权限及成功刷新回调。
 * 返回列表、分页和明确确认对话框；只有uncertain且后台允许的快照可提交，不自动退款，不修改其他Key。
 * 页面切换取消过期读请求；成功显示后台审计回执，失败保留证据，409要求重读指纹后再核对。 */
export function BudgetReconciliation({ keyId, accessToken, allowed, onReconciled }: {
  keyId: string; accessToken: string | null; allowed: boolean; onReconciled?: () => Promise<void>;
}) {
  const t = useT();
  const [page, setPage] = useState(1), [refresh, setRefresh] = useState(0);
  const [data, setData] = useState<ReservationPage | null>(null);
  const [loading, setLoading] = useState(false), [readError, setReadError] = useState(false);
  const [selected, setSelected] = useState<ReconciliationReservation | null>(null);
  const [outcome, setOutcome] = useState<"no_charge" | "charged">("no_charge");
  const [cost, setCost] = useState(""), [evidence, setEvidence] = useState("");
  const [saving, setSaving] = useState(false), [writeError, setWriteError] = useState("");
  const [receipt, setReceipt] = useState<{ resolution: Resolution; already_resolved: boolean } | null>(null);
  const [refreshFailed, setRefreshFailed] = useState(false);
  useEffect(() => {
    if (!allowed || !accessToken || !keyId) return;
    const controller = new AbortController();
    setLoading(true); setReadError(false);
    apiClient.get<ReservationPage>("/budget/reservations", { accessToken, signal: controller.signal, query: { key_id: keyId, state: "uncertain", page, page_size: 50 } })
      .then(result => {
        if (!Array.isArray(result.reservations) || !Number.isFinite(result.total)) throw new Error(t("uiErrors.reservationInvalid"));
        if (!controller.signal.aborted) setData(result);
      })
      .catch(() => { if (!controller.signal.aborted) { setData(null); setReadError(true); } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [allowed, accessToken, keyId, page, refresh]);
  if (!allowed || !accessToken || !keyId) return null;
  /** 打开指定快照的核对表单；清空上次输入及错误，指纹来自当前已授权列表，不发请求。 */
  const open = (row: ReconciliationReservation) => {
    setSelected(row); setOutcome("no_charge"); setCost(""); setEvidence(""); setWriteError("");
  };
  /** 校验人工结论并提交当前指纹；返回完成Promise，提交成功后刷新余额与列表，任何失败均不猜测退款。 */
  const reconcile = async () => {
    if (!selected || saving) return;
    const actual = outcome === "no_charge" ? 0 : Number(cost);
    if (!evidence.trim() || evidence.trim().length > 2000) { setWriteError("budgetReconciliation.invalidEvidence"); return; }
    if (!Number.isFinite(actual) || (outcome === "charged" && (!cost.trim() || actual <= 0))) { setWriteError("budgetReconciliation.invalidCost"); return; }
    setSaving(true); setWriteError(""); setRefreshFailed(false);
    try {
      const result = await apiClient.post<{ resolution: Resolution; already_resolved: boolean }>("/budget/reservations/" + encodeURIComponent(selected.reservation_id) + "/reconcile", {
        accessToken, body: { outcome, verified_cost_usd: actual, evidence: evidence.trim(), expected_fingerprint: selected.fingerprint },
      });
      if (!result.resolution?.reservation_id || result.resolution.reservation_id !== selected.reservation_id) throw new Error(t("uiErrors.resolutionInvalid"));
      setReceipt(result); setSelected(null); setPage(1); setRefresh(value => value + 1);
      try { await onReconciled?.(); } catch { setRefreshFailed(true); }
    } catch (error) {
      const status = (error as { status?: number })?.status;
      setWriteError(status === 409 ? "budgetReconciliation.conflict" : status === 403 ? "budgetReconciliation.forbidden" : status === 400 ? "budgetReconciliation.invalid" : "budgetReconciliation.failed");
    } finally { setSaving(false); }
  };
  /** 并发冲突或未知写结果后重新读取记录与余额；不提交对账，避免继续展示过期的冻结金额。 */
  const reloadCurrent = async () => {
    setSelected(null); setRefresh(value => value + 1); setRefreshFailed(false);
    try { await onReconciled?.(); } catch { setRefreshFailed(true); }
  };
  return <section aria-label={t("budgetReconciliation.title")} className="space-y-3 rounded-lg border p-4">
    <h3 className="font-medium">{t("budgetReconciliation.title")}</h3>
    <p className="text-muted-foreground">{t("budgetReconciliation.help")}</p>
    {receipt && <div role="status" className="space-y-1 rounded border p-3">
      <p>{t(receipt.already_resolved ? "budgetReconciliation.alreadyResolved" : "budgetReconciliation.completed")}</p>
      <p>{t("budgetReconciliation.receipt", { attempt: receipt.resolution.reservation_id, actor: receipt.resolution.actor_id, time: receipt.resolution.resolved_at })}</p>
      <p>{t("budgetReconciliation.costChange", { before: receipt.resolution.previous_cost_usd, after: receipt.resolution.updated_cost_usd, delta: receipt.resolution.cost_delta_usd })}</p>
      <p>{t("budgetReconciliation.evidenceSaved", { evidence: receipt.resolution.evidence })}</p>
    </div>}
    {refreshFailed && <p role="alert">{t("budgetReconciliation.refreshFailed")}</p>}
    {loading ? <p role="status">{t("budgetReconciliation.loading")}</p> : readError ? <div role="alert">
      <p>{t("budgetReconciliation.loadFailed")}</p><Button type="button" variant="outline" onClick={() => setRefresh(value => value + 1)}>{t("hardBudget.retry")}</Button>
    </div> : data && <>
      <p>{t("budgetReconciliation.total", { count: data.total })}</p>
      {data.reservations.length === 0 ? <p>{t("budgetReconciliation.empty")}</p> : <div className="overflow-x-auto"><table className="w-full text-left text-xs">
        <thead><tr>{["attempt", "call", "time", "model", "held", "ledger", "action"].map(key => <th key={key} className="p-2">{t("budgetReconciliation.columns." + key)}</th>)}</tr></thead>
        <tbody>{data.reservations.map(row => <tr key={row.reservation_id}>
          <td className="break-all p-2"><code>{row.reservation_id}</code></td><td className="break-all p-2"><code>{row.call_id}</code></td><td className="p-2">{row.started_at}</td><td className="p-2"><p>{row.model}</p><p>{t("budgetReconciliation.upstream", { model: row.upstream_model || t("budgetReconciliation.unknown") })}</p><p>{t("budgetReconciliation.supplier", { supplier: row.supplier || t("budgetReconciliation.unknown") })}</p></td>
          <td className="p-2">{t("budgetReconciliation.usd", { amount: row.amount })}</td><td className="p-2">{row.ledger_cost === null ? t("budgetReconciliation.unknown") : t("budgetReconciliation.usd", { amount: row.ledger_cost })}</td>
          <td className="p-2">{row.state === "uncertain" && row.can_reconcile ? <Button type="button" variant="outline" onClick={() => open(row)} aria-label={t("budgetReconciliation.openAttempt", { attempt: row.reservation_id })}>{t("budgetReconciliation.open")}</Button>
            : <span>{t("budgetReconciliation.blocked." + (blockedReasons.has(row.blocked_reason) ? row.blocked_reason : "unknown"))}</span>}</td>
        </tr>)}</tbody>
      </table></div>}
      <div className="flex items-center gap-3"><Button type="button" variant="outline" disabled={page <= 1} onClick={() => setPage(value => value - 1)}>{t("budgetReconciliation.previous")}</Button>
        <span>{t("budgetReconciliation.page", { page: data.page })}</span><Button type="button" variant="outline" disabled={data.page * data.page_size >= data.total} onClick={() => setPage(value => value + 1)}>{t("budgetReconciliation.next")}</Button></div>
    </>}
    <Dialog open={selected !== null} onOpenChange={open => { if (!open && !saving) setSelected(null); }}><DialogContent>
      <DialogHeader><DialogTitle>{t("budgetReconciliation.confirmTitle")}</DialogTitle></DialogHeader>
      <p>{t("budgetReconciliation.confirmHelp", { attempt: selected?.reservation_id ?? "" })}</p>
      <Label htmlFor="budget-resolution-outcome">{t("budgetReconciliation.outcome")}</Label>
      <select id="budget-resolution-outcome" className="h-10 rounded border bg-background px-3" value={outcome} disabled={saving} onChange={event => setOutcome(event.target.value as "no_charge" | "charged")}>
        <option value="no_charge">{t("budgetReconciliation.noCharge")}</option><option value="charged">{t("budgetReconciliation.charged")}</option>
      </select>
      {outcome === "charged" && <><Label htmlFor="budget-resolution-cost">{t("budgetReconciliation.cost")}</Label><Input id="budget-resolution-cost" type="number" min="0" step="any" disabled={saving} value={cost} onChange={event => setCost(event.target.value)} /></>}
      <p className="text-sm text-muted-foreground">{t("budgetReconciliation.deltaHelp")}</p>
      {selected && outcome === "charged" && Number(cost) > selected.amount && <p role="status">{t("budgetReconciliation.overHold", { cost, amount: selected.amount })}</p>}
      <Label htmlFor="budget-resolution-evidence">{t("budgetReconciliation.evidence")}</Label>
      <textarea id="budget-resolution-evidence" className="min-h-24 rounded border bg-background p-3" disabled={saving} maxLength={2000} value={evidence} onChange={event => setEvidence(event.target.value)} />
      {writeError && <p role="alert">{t(writeError)}</p>}
      {["budgetReconciliation.conflict", "budgetReconciliation.failed"].includes(writeError) && <Button type="button" variant="outline" onClick={() => void reloadCurrent()}>{t("budgetReconciliation.reload")}</Button>}
      <div className="flex justify-end gap-2"><Button type="button" variant="outline" disabled={saving} onClick={() => setSelected(null)}>{t("budgetReconciliation.cancel")}</Button><Button type="button" disabled={saving || ["budgetReconciliation.conflict", "budgetReconciliation.failed"].includes(writeError)} onClick={() => void reconcile()}>{t(saving ? "budgetReconciliation.saving" : "budgetReconciliation.confirm")}</Button></div>
    </DialogContent></Dialog>
  </section>;
}
