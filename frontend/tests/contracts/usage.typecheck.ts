import type { components, operations } from "../../src/lib/http/contracts/usage";
/** 编译期证明分页、正文以及可空指标采用契约；不冒充运行时HTTP验证。 */
const page: components["schemas"]["LogPage"] = {data:[],total:0,page:1,page_size:50,total_pages:0};
const result: operations["listUILogs"]["responses"][200]["content"]["application/json"] = page;
// @ts-expect-error 失败不能用缺少真实分页字段的空对象伪装成功
const invalid: components["schemas"]["LogPage"] = {};
void result; void invalid;
