/** 独立参考页只接入第三阶段显式验收的领域，不从兼容目录推断支持范围。 */
export const referenceDomains = ["suppliers", "users", "keys", "models", "usage"];
/** 页面自有文案共用中英字典；协议字段、示例输入与 Swagger 原生控件不做 DOM 文本替换。 */
export const referenceCopy = {
  zh: {
    title: "API 参考", open: "打开独立 API 参考", guides: "协议使用指南", product: "产品文档", developer: "开发文档", language: "English", domain: "接口领域", download: "下载原始 OpenAPI", gateway: "网关地址", connect: "启用接口调用", disconnect: "恢复只读", readOnly: "只读模式：尚未设置调用目标。", connected: "调用目标：{target}", loading: "正在读取契约…", loadError: "契约加载失败，请刷新重试。", invalidTarget: "请输入完整 HTTP(S) 网关地址，不包含用户名、密码、查询参数或片段；HTTPS 页面不能调用 HTTP 网关。",
    scope: "此入口仅展示五个核心管理领域的 {count} 个已契约化操作，不是全量 API 支持清单。",
    boundary: "尚未纳入：Bypass、流式推理、SSO/SCIM、组织团队生命周期和其他外围接口。原有协议指南仍可阅读，但不代表这些接口已完成契约验收。",
    targetHelp: "仅填写你有权访问的网关。启用后在 Swagger Authorize 输入网关凭据，再手动执行请求；跨域需要目标网关允许 CORS。地址和凭据不写入浏览器存储，刷新后恢复只读。切换领域会清除授权。",
    vendor: "下方为 Swagger UI 原生接口浏览器，其标准控件使用英文；契约说明随页面语言切换，协议字段和示例值保持原样。",
    domains: { suppliers: "供应商", users: "用户", keys: "Key", models: "模型与端点", usage: "日志与用量" },
  },
  en: {
    title: "API reference", open: "Open API reference", guides: "Protocol guides", product: "Product docs", developer: "Developer docs", language: "简体中文", domain: "API domain", download: "Download original OpenAPI", gateway: "Gateway URL", connect: "Enable API requests", disconnect: "Return to read-only", readOnly: "Read-only: no request target configured.", connected: "Request target: {target}", loading: "Loading contract…", loadError: "Unable to load the contract. Refresh to retry.", invalidTarget: "Enter a complete HTTP(S) gateway URL without credentials, query or fragment. An HTTPS page cannot call an HTTP gateway.",
    scope: "This reference covers {count} contracted operations in five core management domains. It is not a complete API support inventory.",
    boundary: "Not covered: Bypass, streaming inference, SSO/SCIM, organization/team lifecycles and peripheral APIs. Existing protocol guides remain available but do not imply completed contract verification.",
    targetHelp: "Use a gateway you are authorized to access. After enabling requests, enter gateway credentials in Swagger Authorize and execute requests manually. Cross-origin requests require gateway CORS support. The URL and credentials are not stored; reload returns to read-only. Switching domains clears authorization.",
    vendor: "The Swagger UI browser below uses its native English controls. Contract descriptions follow the page language; protocol fields and example values remain unchanged.",
    domains: { suppliers: "Suppliers", users: "Users", keys: "Keys", models: "Models and endpoints", usage: "Logs and usage" },
  },
};
