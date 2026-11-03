# 供应商管理契约试点

对应 Proposal #149、接口完整性路线图阶段 2。唯一源为 [OpenAPI 3.0.3](../../../contracts/suppliers/openapi.json)，[离线接口参考](supplier-api.generated.md) 从同一源生成。试点只增加开发期契约和验证，不安装网关中间件、不切换现有 handler 或前端客户端，不修改数据库或生产配置。

## 边界与证据

| 范围 | 本轮约定与验证 |
| --- | --- |
| 五个操作 | GET/POST `/credentials`、GET `/credentials/by_name/{credential_name}`、PATCH/DELETE `/credentials/{credential_name}`。真实 HTTP/PG 测试对应每个操作。 |
| 权限 | 每个操作验证无凭据 401、普通用户 403、管理员成功。OpenAPI 验证器不模拟身份认证，真实网关负责权限判断。 |
| 响应 | 创建/更新/读取 200；不存在详情 404；重复删除 200 且 `deleted=0`。错误信封的 `code` 必须等于 HTTP 状态。没有 default/任意状态放行。 |
| 失败 | 非法 JSON、尾随 JSON、REST 上的 JSON-RPC 为 400；真实 SQL 读/写/删除故障为 500，随后回读不变并恢复使用。503 记录 store 不可用分支，只有契约形状单测，本轮不把它算作真实 HTTP 故障覆盖。 |
| 用户流程 | 中英文页面创建、编辑、刷新、列表回读、空 Key 补丁、引用供应商部署模型、真实本地上游推理与计费、删除、再次读取 404。 |

这不是全量 API 文档。`by_model`、迁移加密、动态鉴权扩展、OAuth、JSON-RPC、模型创建和推理协议不属于五个操作的契约覆盖；E2E 中使用推理是为了验证供应商配置确实生效，不代表推理接口已迁移契约。

名称以普通资源名为范围；路径中的斜杠、`new/list/info/update/delete/register/runs`、版本前缀、集合别名以及含 `settings` 的名称受旧通用解析器影响，已在参数说明及 `x-unverified-name-cases` 中标记为未覆盖，不新增运行时限制。POST 仍可能保存此类名称；这属于未解决的历史命名边界，本轮不偷偷改变接口，也不宣称这些名称的 CRUD 已完整覆盖。`token` 是允许且由 E2E 验证的普通名称。

## 兼容行为

- 请求为 JSON 对象，没有新增必填供应商字段；空正文或空对象保留既有语义。扩展字段保留；顶层 `password` 忽略，`jsonrpc` 由后台业务检查拒绝。
- 同名 POST 是替换；PATCH 是合并，目标不存在时创建。非凭据顶层字段（包括 `credential_info`）直接替换，不是深层合并。
- 创建缺省或非字符串名称时可生成名称，也兼容旧 `id` 回退。详情总是包含 `id`、`credential_name`、`credential_info`；`created_at` 不被假写成严格时间字段，因为历史接口允许调用者覆盖。
- 对象形态的 `credential_values` 在创建响应中省略，读/更新时仅敏感字符串脱敏。其他类型的旧值仍可能存在，所以契约保留任意 JSON，而不是谎称后端已严格校验。脱敏不是递归的通用敏感信息过滤器，不应把额外秘密放进任意扩展字段。
- PATCH 的凭据对象只合并提供的字段。敏感字段为空、非字符串或包含 `**` 时保持原值；整个 `credential_values` 缺省、null 或非对象也保持原值；非敏感字段的 null/空字符串会覆盖。
- 列表 `data` 和 `credentials` 是相同数据的兼容别名；不支持分页的字段不写成分页功能。
- DELETE 无正文是页面常规路径；历史对象正文还能提供其他删除 ID，非事务批量删除失败不撤销已成功的项。本轮覆盖单资源删除，不声称验证了任意批量删除组合。

## 生成与门禁

```bash
npm --prefix frontend ci
npm --prefix frontend run gen:api:suppliers
npm --prefix frontend run check:api:suppliers
node --test frontend/scripts/gen-supplier-contract.test.mjs
node --test scripts/test_quality_workflow.cjs
go test -race ./contracts/suppliers
bash scripts/regression.sh '^TestSupplierContract'
E2E_RUN_DIR="$PWD/.e2e/supplier-contract" bash scripts/e2e.sh \
  scenarios/regression/catalog-persistence-en.test.ts \
  scenarios/regression/catalog-persistence-zh-CN.test.ts
```

Go 类型由固定版本 `oapi-codegen v2.5.0` 生成；TypeScript 类型由锁文件中的 `openapi-typescript` 生成；离线 Markdown 无 CDN 或运行时依赖。首次生成需下载 Go 工具及 npm 包，缓存后的文档阅读不需要网络。生成文件带标准生成器注释，说明维护在契约源中，不手工编辑生成物。

宽松且允许扩展的 Go 凭据对象生成 `map[string]json.RawMessage`：保留缺省、显式 null 与未知字段，避免自动结构体编解码把缺省变成 null。强约束在 OpenAPI 和真实 HTTP 校验中执行；不要误认为 Go 编译器能替代请求校验。TypeScript 类型对已知字段保留静态约束，另外有正反编译夹具。所有生成类型均未接入现有请求运行链。

`check:api:suppliers` 只读比较四个生成物并编译 TypeScript 夹具；缺失、漂移或错误均失败。生成器检查操作存在于路由清单，但路由清单不是 schema 来源。单元测试实际调用生成器，再改动每个临时生成物验证门禁确实拒绝，不修复文件后假报通过。

`Supplier API contract` CI 作业运行生成检查、验证器单元测试、真实 HTTP/PG 回归并上传证据。现有发布作业继续执行 E2E。旧 `gen:api` 仍拒绝从不完整来源生成全量客户端；本试点不取消这条保护。

真实数据库测试使用独立 schema，SQL 故障只注入该 schema；浏览器测试使用专属本地上游并检查实际请求和计费，不把静态页面或 mock 管理 API 当端到端成功。未测试真实供应商账户、SSO 或生产并发。合并仅提供开发期门禁，是否部署仍需单独决定；回退本试点不会涉及数据库回滚。
