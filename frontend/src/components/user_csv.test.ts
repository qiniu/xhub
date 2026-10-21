import { describe, it, expect } from "vitest";
import { parseUserCsv, userCsvTemplate } from "./user_csv";

// 纯解析回归：无需网络，覆盖合法模板、零额度、用户原文、错误输入；不产生持久化数据。
describe("user CSV contract", () => {
  it("parses the downloadable template and preserves user values", () => {
    expect(parseUserCsv(userCsvTemplate(), []).rows[0].errors).toEqual([]);
    const row = parseUserCsv(
      "user_email,user_role,password,user_alias,max_budget\nuser@example.com,user, password123 ,中文名字,0",
      [],
    ).rows[0];
    expect(row.errors).toEqual([]);
    expect(row.user.password).toBe(" password123 ");
    expect(row.user.user_alias).toBe("中文名字");
    expect(row.user.max_budget).toBe(0);
  });
  // 异常结构必须整表拒绝，包括旧版被后台忽略的字段，无需清理。
  it.each([
    "",
    "user_email,user_role,password",
    "team_name,key_name\nA,B",
    "user_email,user_email,password\na,b,c",
    "user_email,user_role,password,models\na,b,c,d",
    '"unterminated',
  ])("rejects malformed structure %s", (text) => {
    expect(parseUserCsv(text, []).error).toBeTruthy();
  });
  // 无效行保留错误供预览，禁止截断数字、重复邮箱、未知团队和短密码，无副作用。
  it.each(["10abc", "-1", "Infinity", "NaN", "0x10", "1e309"])("rejects budget %s", (budget) => {
    expect(
      parseUserCsv(`user_email,user_role,password,max_budget\na@example.com,user,password1,${budget}`, []).rows[0]
        .errors,
    ).toContain("budgetError");
  });
  it("checks duplicate identities, passwords, roles and team scope", () => {
    const parsed = parseUserCsv(
      "user_email,user_role,password,team_id\na@example.com,user,password1,t1\nA@example.com,owner,short,missing",
      ["t1"],
    );
    expect(parsed.rows[0].errors).toEqual([]);
    expect(parsed.rows[1].errors).toEqual(["emailError", "roleError", "passwordError", "teamError"]);
  });
});
