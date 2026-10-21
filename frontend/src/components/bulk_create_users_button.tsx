import React, { useRef, useState } from "react";
import Papa from "papaparse";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useT } from "@/i18n";
import { userCreateCall } from "./networking";
import { downloadUserCsv, parseUserCsv, userCsvTemplate, type CsvParse, type CsvRow } from "./user_csv";

/** 用户页面传入的管理员凭据、团队范围和刷新回调；保留角色参数以兼容现有调用方。 */
interface BulkCreateUsersProps {
  accessToken: string;
  teams: { team_id: string }[] | null;
  possibleUIRoles: null | Record<string, Record<string, string>>;
  onUsersCreated?: () => void;
}

/** 用户CSV导入弹窗；接收管理员令牌、可见团队和刷新回调，返回可下载模板和逐行结果的表单。
 * 全表校验通过才提交；逐行创建允许部分失败，但同一轮不可重复提交。关闭/换文件清除密码和状态。 */
export default function BulkCreateUsersButton({ accessToken, teams, onUsersCreated }: BulkCreateUsersProps) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [parsed, setParsed] = useState<CsvParse>({ rows: [] });
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [page, setPage] = useState(0);
  const generation = useRef(0);
  const submitting = useRef(false);
  const inputID = React.useId();
  const tr = (key: string, vars?: Record<string, string | number>) => t(`userCsv.${key}`, vars);
  /** 重置导入状态并使未完成读取失效；无参数/返回值，仅清除当前弹窗数据。 */
  const reset = () => {
    generation.current++;
    setParsed({ rows: [] });
    setDone(false);
    setPage(0);
  };
  /** 读取文件并严格解析；参数为浏览器文件，无返回值；过期读取不覆盖较新的选择。 */
  const upload = async (file?: File) => {
    reset();
    if (!file) return;
    const version = generation.current;
    if (!/\.csv$/i.test(file.name) || file.size > 5 * 1024 * 1024) {
      setParsed({ rows: [], error: "file" });
      return;
    }
    try {
      const text = await file.text();
      if (version === generation.current) setParsed(parseUserCsv(text, teams?.map((team) => team.team_id) ?? []));
    } catch {
      if (version === generation.current) setParsed({ rows: [], error: "structure" });
    }
  };
  const invalid = parsed.rows.some((row) => row.errors.length);
  /** 提交校验后的整份列表；无参数/返回值，逐行持久化并刷新列表，不在日志或结果中暴露密码。 */
  const create = async () => {
    if (submitting.current || done || parsed.error || invalid || !parsed.rows.length) return;
    submitting.current = true;
    setBusy(true);
    const rows: CsvRow[] = parsed.rows.map((row) => ({ ...row, user: { ...row.user } }));
    try {
      for (const row of rows) {
        try {
          const response = await userCreateCall(accessToken, null, { ...row.user });
          if (!response?.user_id) throw new Error("invalid create response");
          row.user_id = response.user_id;
          row.status = "success";
        } catch {
          row.status = "failed";
        }
        // 已处理的密码立即丢弃，结果导出只包含白名单字段。
        row.user.password = "";
        setParsed({ rows: rows.map((item) => ({ ...item })) });
      }
      setDone(true);
      if (rows.some((row) => row.status === "success")) onUsersCreated?.();
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };
  /** 导出非敏感结果；无参数/返回值，对电子表格公式转义，绝不导出密码或虚构Key。 */
  const results = () =>
    downloadUserCsv(
      Papa.unparse(
        parsed.rows.map((row) => ({
          user_email: row.user.user_email,
          user_id: row.user_id ?? "",
          status: row.status,
          error: row.status === "failed" ? tr("requestError") : "",
        })),
        { escapeFormulae: true },
      ),
      "users-results.csv",
    );
  const pages = Math.max(1, Math.ceil(parsed.rows.length / 10));
  return (
    <>
      <Button
        onClick={() => {
          reset();
          setOpen(true);
        }}
      >
        {tr("title")}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(value) => {
          if (!busy) {
            reset();
            setOpen(value);
          }
        }}
      >
        <DialogContent className="max-w-4xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{tr("title")}</DialogTitle>
          </DialogHeader>
          <p>{tr("help")}</p>
          <p>{tr("privacy")}</p>
          <Button disabled={busy} onClick={() => downloadUserCsv(userCsvTemplate(), "users-template.csv")}>
            {tr("template")}
          </Button>
          <label
            htmlFor={inputID}
            onDragOver={(event) => event.preventDefault()}
            onDrop={(event) => {
              event.preventDefault();
              if (!busy) void upload(event.dataTransfer.files?.[0]);
            }}
          >
            {tr("upload")}
          </label>
          <input
            id={inputID}
            type="file"
            accept=".csv"
            disabled={busy}
            onChange={(event) => {
              void upload(event.target.files?.[0]);
              event.target.value = "";
            }}
          />
          {parsed.error && <p role="alert">{tr(parsed.error, parsed.vars)}</p>}
          {invalid && <p role="alert">{tr("blocked")}</p>}
          {parsed.rows.length > 0 && (
            <>
              <table className="w-full text-sm">
                <thead>
                  <tr>
                    {["row", "email", "role", "budget", "status"].map((key) => (
                      <th key={key}>{tr(key)}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {parsed.rows.slice(page * 10, page * 10 + 10).map((row) => (
                    <tr key={row.row}>
                      <td>{row.row}</td>
                      <td>{row.user.user_email}</td>
                      <td>{row.user.user_role}</td>
                      <td>{row.user.max_budget ?? "—"}</td>
                      <td>
                        {row.errors.length ? row.errors.map((key) => tr(key)).join("; ") : tr(row.status ?? "pending")}
                        {row.status === "failed" && <p>{tr("requestError")}</p>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <div className="flex gap-2">
                <Button disabled={page === 0} onClick={() => setPage(page - 1)}>
                  {tr("previous")}
                </Button>
                <span>{tr("page", { page: page + 1, total: pages })}</span>
                <Button disabled={page + 1 === pages} onClick={() => setPage(page + 1)}>
                  {tr("next")}
                </Button>
              </div>
              {!done && (
                <Button disabled={busy || invalid} onClick={create}>
                  {busy ? tr("busy") : tr("create", { count: parsed.rows.length })}
                </Button>
              )}
            </>
          )}
          {done && (
            <>
              <p role="status">
                {tr("summary", {
                  success: parsed.rows.filter((row) => row.status === "success").length,
                  failed: parsed.rows.filter((row) => row.status === "failed").length,
                })}
              </p>
              <Button onClick={results}>{tr("results")}</Button>
            </>
          )}
          <Button disabled={busy} onClick={reset}>
            {tr("reset")}
          </Button>
          <Button
            disabled={busy}
            onClick={() => {
              reset();
              setOpen(false);
            }}
          >
            {tr("close")}
          </Button>
        </DialogContent>
      </Dialog>
    </>
  );
}
