import Papa from "papaparse";

export const USER_CSV_COLUMNS = ["user_email", "user_role", "password", "user_alias", "team_id", "max_budget"];
/** 提交给用户创建接口的白名单字段；密码仅用于本次请求，不得写入预览和导出。 */
export interface CsvUser {
  user_email: string;
  user_role: string;
  password: string;
  user_alias?: string;
  team_id?: string;
  max_budget?: number;
}
/** 导入记录及非敏感结果；错误保存翻译键，以支持语言实时切换。 */
export interface CsvRow {
  row: number;
  user: CsvUser;
  errors: string[];
  status?: "success" | "failed";
  user_id?: string;
}
/** 解析结果；结构错误时不返回可提交行，vars只包含表头名称。 */
export interface CsvParse {
  rows: CsvRow[];
  error?: string;
  vars?: Record<string, string>;
}

/** 解析用户导入文本；参数为CSV和当前可选团队ID，返回字段严格校验后的行或结构错误。
 * 供上传组件调用；不发请求，不翻译用户内容，不将密码放入错误信息。 */
export function parseUserCsv(text: string, teamIDs: string[]): CsvParse {
  const result = Papa.parse<string[]>(text.replace(/^\uFEFF/, ""), { skipEmptyLines: "greedy" });
  const [rawHeaders, ...data] = result.data;
  const headers = rawHeaders?.map((v) => v.trim()) ?? [];
  if (result.errors.length || data.length === 0 || data.length > 1000 || new Set(headers).size !== headers.length)
    return { rows: [], error: "structure" };
  const required = USER_CSV_COLUMNS.slice(0, 3).filter((c) => !headers.includes(c));
  const unknown = headers.filter((c) => !USER_CSV_COLUMNS.includes(c));
  if (required.length || unknown.length)
    return { rows: [], error: "columns", vars: { columns: required.join(", "), unknown: unknown.join(", ") } };
  const seen = new Set<string>();
  const rows = data.map((values, i): CsvRow => {
    const field = (key: string) => values[headers.indexOf(key)] ?? "";
    const user: CsvUser = {
      user_email: field("user_email").trim(),
      user_role: field("user_role").trim(),
      password: field("password"),
    };
    const errors: string[] = [];
    if (values.length !== headers.length) errors.push("structure");
    const email = user.user_email.toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || seen.has(email)) errors.push("emailError");
    seen.add(email);
    if (!["user", "admin"].includes(user.user_role)) errors.push("roleError");
    const bytes = new TextEncoder().encode(user.password).length;
    if (bytes < 8 || bytes > 72) errors.push("passwordError");
    const budget = field("max_budget").trim();
    if (budget) {
      if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(budget) || !Number.isFinite(Number(budget))) errors.push("budgetError");
      else user.max_budget = Number(budget);
    }
    if (field("user_alias").trim()) user.user_alias = field("user_alias").trim();
    if (field("team_id").trim()) {
      user.team_id = field("team_id").trim();
      if (!teamIDs.includes(user.team_id)) errors.push("teamError");
    }
    return { row: i + 2, user, errors };
  });
  return { rows };
}

/** 生成固定英文示例模板；无参数，返回CSV，下载时保持协议字段不翻译。 */
export function userCsvTemplate(): string {
  return Papa.unparse([
    USER_CSV_COLUMNS,
    ["new.user@example.com", "user", "Replace-before-import-123!", "New User", "", "10"],
  ]);
}

/** 下载CSV；参数为内容和文件名，无返回值。延后回收URL，确保浏览器完成下载导航。 */
export function downloadUserCsv(csv: string, filename: string): void {
  const url = URL.createObjectURL(new Blob(["\uFEFF", csv], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
