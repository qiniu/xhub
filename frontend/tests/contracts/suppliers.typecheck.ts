import type { components, paths } from "../../src/lib/http/contracts/suppliers";

// 编译期夹具：验证生成类型保留扩展/null，并要求响应关键字段；无执行、网络或清理，不替代真实 HTTP 测试。
const record: components["schemas"]["Credential"] = { id: "example", credential_name: "example", credential_info: null, extension: false };
const input: components["schemas"]["CredentialInput"] = { credential_values: null, note: "Example provider" };
const list: paths["/credentials"]["get"]["responses"][200]["content"]["application/json"] = { object: "list", data: [record], credentials: [] };
// @ts-expect-error 响应必须包含 id 和 credential_info，不能凭空把半个对象当完整供应商。
const missing: components["schemas"]["Credential"] = { credential_name: "example" };
// @ts-expect-error 删除数必须是数值，不能接受字符串。
const wrongCount: components["schemas"]["Deletion"] = { id: "example", credential_name: "example", deleted: "1" };
// @ts-expect-error 创建对象不能变成字符串。
const scalar: components["schemas"]["CredentialInput"] = "invalid";
void [input, list, missing, wrongCount, scalar];
