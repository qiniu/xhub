import type { components, paths } from "../../src/lib/http/contracts/keys";

type S = components["schemas"];
const blank: S["CreateKey"] = {};
// 创建表单无团队时显式发送null；生成类型必须兼容实际请求。
const teamless: S["CreateKey"] = { team_id: null };
// 组织筛选为可省略的字符串，空值保持不过滤；生成类型必须与页面的查询参数一致。
const organizationQuery: paths["/key/list"]["get"]["parameters"]["query"] = { organization_id: "org-id", scope: "personal", size: 1 };
const emptyOrganizationQuery: paths["/key/list"]["get"]["parameters"]["query"] = { organization_id: "" };
// @ts-expect-error 组织ID必须是字符串，不能用布尔值代替。
const invalidOrganizationQuery: paths["/key/list"]["get"]["parameters"]["query"] = { organization_id: true };
void [organizationQuery, emptyOrganizationQuery, invalidOrganizationQuery];
const clear: S["UpdateKey"] = { key: "id", max_budget: null, route_template_id: null };
const zero: S["UpdateKey"] = { key: "id", max_budget: 0 };
const remove: S["DeleteKeys"] = { keys: ["id"] };
const response: paths["/key/delete"]["post"]["responses"][200]["content"]["application/json"] = { deleted: 1 };
// @ts-expect-error 更新必须指定Key，不允许裸预算。
const missing: S["UpdateKey"] = { max_budget: 0 };
// @ts-expect-error v2实际为单条查询，不能宣称批量支持。
const batch: S["KeySelector"] = { keys: ["id"] };
// @ts-expect-error 创建不支持管理Key。
const management: S["CreateKey"] = { key_type: "management" };
// @ts-expect-error 删除结果是数量，不是布尔假成功。
const wrongDelete: S["DeletedKeys"] = { deleted: true };
// @ts-expect-error 空目标不是合法删除请求。
const missingDelete: S["DeleteKeys"] = {};
// @ts-expect-error 不能声明自动轮换支持。
const rotating: S["CreateKey"] = { auto_rotate: true };
void [teamless, blank, clear, zero, remove, response, missing, batch, management, wrongDelete, missingDelete, rotating];
