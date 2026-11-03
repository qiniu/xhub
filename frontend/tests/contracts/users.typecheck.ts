import type { components, paths } from "../../src/lib/http/contracts/users";

// 编译期验证真实更新信封、批量输入严格类型；不执行HTTP，不代替运行时校验，未产生数据无需清理。
type Schemas = components["schemas"];
const update: Schemas["UpdateUser"] = { user_id: "example", max_budget: null };
const legacy: Schemas["UpdateUser"] = { max_budget: "", budget_reset_day: 31 };
const bulk: Schemas["BulkBudgetInput"] = { users: [{ user_id: "example", max_budget: 0 }] };
const clear: Schemas["BulkBudgetInput"] = { all_users: true, user_updates: { max_budget: null } };
// @ts-expect-error 批量预算不能沿用单人数字字符串。
const wrongBudget: Schemas["BulkBudgetInput"] = { users: [{ user_id: "example", max_budget: "500" }] };
// @ts-expect-error 月度刷新日不能为null。
const wrongDay: Schemas["BudgetChanges"] = { budget_reset_day: null };
// @ts-expect-error 旧前端误写的data/user_id不是真实更新回复。
const wrongEnvelope: paths["/user/update"]["post"]["responses"][200]["content"]["application/json"] = { user_id: "example", data: {} };
// @ts-expect-error 用户必须包含真实快照字段。
const partialUser: Schemas["User"] = { user_id: "example" };
const err: Schemas["Error403"] = { error: { code: "403", message: "Denied", type: "access_denied", param: null } };
// @ts-expect-error 不完整错误不应因生成类型丢字段而被放过。
const partialError: Schemas["Error403"] = { error: { code: "403" } };
void [update, legacy, bulk, clear, wrongBudget, wrongDay, wrongEnvelope, partialUser, err, partialError];
