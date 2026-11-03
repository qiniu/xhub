# 接口与测试完整性基线

[English](../en/interface-integrity-baseline.md) · [实施计划](interface-integrity-roadmap.md) · [开发指南](README.md)

本基线记录 2026-10-24 对源码 `185726e37e8f456fa62b8df33560cda153b959d8` 的审计，区分已确认实现、测试证据和待补能力。它不是线上巡检结果，也不是全量接口可用性证明。首批修复限定为通用管理目录的请求解析、持久化失败和不存在资源处理；不改变现有功能集合、供应商协议、会话、预算或生产配置。

## 清单来源与统计口径

不能把历史路由目录、当前注册入口和真实可用能力视为同一集合。

| 来源 | 源码基线中的数量 | 能证明什么 |
| --- | --- | --- |
| [`internal/catalog/routes.json`](../../../internal/catalog/routes.json) | 755 行，755 个唯一 `method + path`，其中 226 个路径含参数 | 内嵌目录的全部声明；包含已移除入口与 8 个 `WEBSOCKET` 标签，不能直接视为 755 个可用 HTTP 接口 |
| `internal/gateway` 非测试 Go 文件中的字面量 `reg.Handle("METHOD /path", ...)` | 216 个唯一声明，79 个不在内嵌目录 | 专用 handler 的直接注册声明；与内嵌目录并集为 834 个唯一声明 |
| [`directoryModule`](../../../internal/gateway/directory.go) 中的拼接注册 | 17 处，公共前缀为 `/scim/v2/connections/{connection_id}` | 上述字面量统计未包含这 17 处；需按实际注册结果展开 |
| [`docs/testdata/catalog.json`](../../testdata/catalog.json) | 777 个唯一 `method + path`，其中 356 个唯一 GET 路径 | 历史测试目录；与运行目录存在差异，不能用其 GET 数量推导完整覆盖率 |
| [`Server.GinRoutes()`](../../../internal/gateway/engine.go) | 本批尚未导出运行时清单 | 已应用移除规则、注册顺序和参数归一化后的 Gin 路由；仍不包含 Gin 前的 Bypass 分发 |
| [`registry.Transports()`](../../../internal/provider/registry/registry.go) | 本批尚未导出运行时清单 | 供应商登记的动作集合；需单列 `KindBypass`，按 `Method + PublicPath` 去重并记录同路径多个 transport |

HTTP 入口还会处理 CORS OPTIONS，且 Bypass 在 Gin 前匹配。路径参数表示模板，不枚举真实 ID。静态声明并集 **834** 是一个可复核的来源分母，不是最终在线接口数，也不是本 PR 已验收接口数。后续契约覆盖率必须以“运行时路由 + Bypass + 显式拒绝入口”的分项清单计算，并保留未评审项；不能把目录中的已移除路径恢复为成功接口来凑覆盖率。

注册优先级来自 [`routes.go`](../../../internal/gateway/routes.go)、[`ingress.go`](../../../internal/gateway/ingress.go) 和 [`removed.go`](../../../internal/gateway/removed.go)：专用模块先注册，目录补齐剩余路径，重复模式不覆盖已有 handler，已移除路径不挂载。故单看目录或 `handlerFor` 不能判断最终业务实现。

以下只读命令复核静态分母，不会启动服务或访问数据库。固定提交保证后续代码变化不会悄悄改写基线。

~~~bash
python3 - <<'PY'
import json, re, subprocess
base = '185726e37e8f456fa62b8df33560cda153b959d8'
def source(path):
    return subprocess.check_output(['git', 'show', base + ':' + path], text=True)
rows = json.loads(source('internal/catalog/routes.json'))
catalog = {(r['m'], r['p']) for r in rows}
files = subprocess.check_output(
    ['git', 'ls-tree', '-r', '--name-only', base, 'internal/gateway'], text=True
).splitlines()
literal = set()
for path in files:
    if path.endswith('.go') and not path.endswith('_test.go'):
        literal.update(re.findall(r'\breg\.Handle\("([A-Z]+) (/[^"\n]*)"\s*,', source(path)))
test_rows = json.loads(source('docs/testdata/catalog.json'))['http_routes']
print('catalog:', len(rows), len(catalog))
print('literal registrations:', len(literal))
print('literal-only:', len(literal - catalog), 'union:', len(catalog | literal))
print('test catalog:', len({(r['method'], r['path']) for r in test_rows}))
print('test GET paths:', len({r['path'] for r in test_rows if r['method'] == 'GET'}))
PY
~~~

## 首批业务链与既有证据

下表是优先深查集合，不替代上面的全量来源分母。除特别说明，错误行为描述均指基线提交；本 PR 的新验证结果单独记录，不能把旧缺陷表当作修复后行为。

| 接口或入口 | 实际实现与调用方 | 基线行为与已有证据 | 缺口及处理范围 |
| --- | --- | --- | --- |
| `POST /credentials`、`GET /credentials`、`GET /credentials/by_name/{credential_name}`、`PATCH /credentials/{credential_name}`、`DELETE /credentials/{credential_name}` | `serveFamilyRoute → family.ServeMgmt → writeCatalogPersist`；前端供应商页面通过 `networking.tsx` 调用 | 正常写入 KV，读取时脱敏；[`provider_forms_test.go`](../../../cmd/regression/provider_forms_test.go) 已验证实际保存、地址更新保留密钥、权限、列表脱敏和删除；[`supplier_catalog_test.go`](../../../cmd/regression/supplier_catalog_test.go) 已验证凭据用于本地上游调用 | **本 PR 优先修复**：读写数据库错误不能返回成功；缺失资源不能伪造；非法 JSON 不能变成空对象写入。已有正常链路必须保留 |
| `POST /prompts`、`GET /prompts/list`、`GET/PATCH/PUT/DELETE /prompts/{prompt_id}`、`GET /prompts/{prompt_id}/info` | 同一通用目录处理器；当前 [`prompts/page.tsx`](../../../frontend/src/app/(dashboard)/prompts/page.tsx) 使用 `ColumnWriteSurface` | 创建、更新和删除场景经真实页面与后台执行；其存储缺陷共享通用处理器 | **本 PR 共用修复范围**：严格解析、错误传播及读回；提示词执行不因此自动实现 |
| `POST /search_tools`、`GET /search_tools/list`、`GET/PUT/DELETE /search_tools/{search_tool_id}` | 同一通用目录处理器；当前 [`search-tools/page.tsx`](../../../frontend/src/app/(dashboard)/search-tools/page.tsx) | 配置对象存入 KV，页面可列出；不代表供应商搜索请求已执行 | **本 PR 共用修复范围**；搜索服务执行与连接探测留作独立修复 |
| `POST /prompts/test` | [`theme_settings.go:promptTest`](../../../internal/gateway/theme_settings.go)；当前页面发送 `prompt_id` | 只判断 ID 是否为空，返回 HTTP 200 和成功或失败字符串；不读取提示词版本、不渲染变量、不调用模型 | **确认缺陷，后续修复**。不能把测试按钮的成功字符串当作模型执行证据 |
| `POST /search_tools/test_connection` | [`theme_settings.go:searchToolTest`](../../../internal/gateway/theme_settings.go)；当前页面发送顶层工具名和 ID | 只检查名称或 ID，返回 HTTP 200；不发外部搜索请求、不验证凭据 | **确认缺陷，后续修复**。需真实本地 HTTP fixture 验证请求发生和失败传播 |
| 通用管理接口请求体包含 `jsonrpc` | `writeCatalogPersist` 在资源处理前检测字段 | 已鉴权管理请求可直接得到 `{"jsonrpc":"2.0","result":{"tools":[]}}`，跳过预期资源操作；没有真实 JSON-RPC 方法执行 | **确认可达分支，独立跟踪**。它不是匿名鉴权旁路；不得以该结果证明 MCP 可用 |
| `POST /v1/mcp/server/oauth/{server_id}/token` | [`routes.go`](../../../internal/gateway/routes.go) 专用注册；[`onboarding.go:mcpOAuthToken`](../../../internal/gateway/onboarding.go)；`removed.go` 特例保留 | 不检查身份、授权码或服务器配置，返回随机 `at-...` 字符串；没有真实令牌交换 | **确认缺陷，后续修复**。未证明该字符串能通过任何鉴权，不能声称已确认权限提升；也不能把已移除的其他 MCP 路径作为该入口不可达的依据 |

目录中其他 `/credentials/*`、提示词版本、搜索供应商目录等声明仍需逐项核对具体语义。修复通用 KV 失败传播并不证明“按模型查询凭据”“凭据加密迁移”“提示词版本执行”等动作已经实现。

## 已确认的假成功模式

基线 [`writeCatalogPersist`](../../../internal/gateway/family/handlers.go) 存在四类独立问题：

1. `ListKV` 错误被丢弃，可返回空列表，混淆“没有数据”和“读取失败”。
2. `GetKV` 任意错误可被转换成带 ID 的合成对象；更新路径也会把读取失败当作首次创建。
3. `PutKV` 错误被丢弃，响应返回刚组装的内存对象；调用方收到成功但数据库未保存。
4. 删除循环只累加成功次数，单个删除失败仍返回 HTTP 200；需要明确批量原子性或失败语义，不能隐藏中途失败。

[`family/codec.go:readMap`](../../../internal/gateway/family/codec.go) 同时忽略读正文和 JSON 解析错误，将非法正文归为 `{}`。这会放大上面的假成功。网关下有 10 个不同包的 `readMap` 实现；本 PR 只收紧明确触及的目录边界，不全局替换所有解析器，以免误改原生载荷或既有空正文语义。

`writeCatalogPersist` 还有按路径后缀生成 token 的分支。该通用分支的实际可达路由需要独立核对；已经确认可达的 OAuth 假成功是上表中的专用 `mcpOAuthToken`。不能仅凭死代码或移除目录推断线上暴露。

## 提示词与搜索测试还缺哪些环节

提示词测试至少需要明确：按 ID/版本读取还是提交内联内容、变量与历史如何渲染、使用哪个模型和执行协议、权限/预算如何生效、JSON 或 SSE 输出契约、用量和失败如何记录。源码保留的 [`useConversation.ts`](../../../frontend/src/app/(dashboard)/prompts/_components/prompt_editor_view/conversation_panel/useConversation.ts) 发送 `dotprompt_content`、变量或历史并期待 SSE；后端目前只识别 `prompt_id` 且返回 JSON。这个编辑器调用点不等于当前路由页面实际挂载它，应分别登记“现用简易页面”和“保留组件”的契约差异。

搜索连接测试至少需要读取或接收合法的供应商配置、验证鉴权、发送限定的测试查询、解释真实响应、设置超时并避免泄漏凭据。保留的 [`SearchConnectionTest`](../../../frontend/src/app/(dashboard)/search-tools/_components/SearchConnectionTest.tsx) 通过 `networking.tsx` 发送 `litellm_params`，期待 `status`、`message`、`results_count`；后端却读取顶层名称/ID，并不返回 `status`。当前路由简易页面发送另一种形状。修复前先统一真实使用场景的契约，不凭猜测兼容全部旧组件。

这两项不能仅改成“结构校验成功”便计为完整实现。若先消除假成功或明确不支持，业务完整性仍标记为未完成，后续补齐真正执行。

## 测试证据不能混用

| 已有入口 | 已有断言范围 | 不足以证明的内容 |
| --- | --- | --- |
| [`TestCatalogReads`](../../../internal/gateway/catalog_reads_test.go) | 遍历测试目录的 GET 路径；验证 JSON、若干状态码与错误包络 | 资源真实存在、数据库失败传播、写入生效、所有运行时路由覆盖 |
| [`column-scenario.ts`](../../../frontend/e2e/column-scenario.ts) | 真实页面创建、更新、删除与清理；测试操作只匹配 `succeeded` 或 `flushed` | 提示词执行或搜索服务连接；这是断言不足，不能称该 E2E 完成了这些功能的验收 |
| [`SearchConnectionTest.test.tsx`](../../../frontend/src/app/(dashboard)/search-tools/_components/SearchConnectionTest.test.tsx) | mock 网络返回后的组件呈现、loading、错误与提示行为 | 后台供应商协议、真实网络或实际搜索结果；合理组件 mock 本身不是“虚假测试” |
| [`provider_forms_test.go`](../../../cmd/regression/provider_forms_test.go) 与 [`supplier_catalog_test.go`](../../../cmd/regression/supplier_catalog_test.go) | 真实 HTTP、隔离数据库、凭据读回和本地上游协议 | 未注入的存储故障、真实商业供应商或生产反向代理 |
| [`scripts/regression.sh`](../../../scripts/regression.sh) | PostgreSQL 前置检查、严格数据库模式和匹配用例检查 | 所有测试辅助函数都正确失败；基线 `testsupport.Postgres` 非法 DSN 仍可 skip，清理失败仍被忽略，需另行修复 |
| [`vitest.config.ts`](../../../frontend/vitest.config.ts) | 按 unit/component/integration/types 分层；质量门禁有 8 个历史排除文件 | 被排除文件通过；不得新增排除或以重跑隐藏首次失败 |

API 类型生成入口 [`gen-api-types.mjs`](../../../frontend/scripts/gen-api-types.mjs) 当前明确拒绝在没有完整 Go/OpenAPI 来源时生成。应保留这个失败；路由表不能替代请求、响应和错误契约，也不能为了绿色构建伪造 schema。

## 本 PR 验收边界与后续队列

首批应同时具备下列证据；是否完成必须由实际报告填写，不能从文档勾选推断：

- 单元层验证解析的正常对象、空正文策略、非法/截断/非对象正文和读取失败；验证成功与存储失败决策，不用无效 mock 代替存储层真实行为。
- 真实 HTTP regression 使用独立 PostgreSQL schema；注入可恢复的读、写、删除失败，验证非 2xx、稳定错误包络和没有假对象/误写；正常 CRUD、脱敏与权限保持；故障和资源都清理。
- 浏览器 E2E 经真实页面、真实网关与数据库完成供应商配置保存/读回/编辑/删除，确认配置能用于本地上游执行；至少一条失败路径观察真实后端拒绝，不能拦截管理 API 伪造成功或失败。
- 保存修复前反例失败和修复后通过记录；报告实际命令、pass/fail/skip、首次失败、重跑、清理状态和外部未覆盖项。修复前失败证据属于同一个修复 PR，不把故意失败用例先合入主干。

本批修复对应 [#136](https://github.com/qiniu/xhub/issues/136)。最终隔离验证结果：

| 实际运行 | 结果 | 本机报告 |
| --- | --- | --- |
| `XHUB_REGRESSION_STRICT=1 go test ./internal/gateway/family -count=1 -json` | 12 顶层、18 子用例通过；0 失败/跳过 | `.verification/interface-integrity-baseline/catalog-package-final.json` |
| `XHUB_REGRESSION_STRICT=1 go test ./cmd/regression -run '^(TestCatalogIntegrity.*\|TestProvider.*\|TestSupplierCatalogLifecycle\|TestOfficialCredentialSupportsNativeModel)$' -count=1 -json` | 9 顶层、17 子用例通过；0 失败/跳过 | `.verification/interface-integrity-baseline/catalog-regression-final.json` |
| `XHUB_REGRESSION_STRICT=1 go test ./internal/gateway -run '^(TestCatalogReads\|TestCredentialCatalogHydration)$' -count=1 -json` | 2 顶层、362 子用例通过；0 失败/跳过 | `.verification/interface-integrity-baseline/catalog-existing-first.json` |
| `bash scripts/e2e.sh scenarios/regression/catalog-persistence-en.test.ts scenarios/regression/catalog-persistence-zh-CN.test.ts` | 2/2 通过，0 重试；本轮只选择 413 个登记场景中的 2 个，其余 411 个未运行 | `.e2e/interface-integrity-final/results.json` |
| `go vet ./internal/gateway/family ./cmd/regression` | 退出 0 | `.verification/interface-integrity-baseline/vet.log` |

E2E 运行时设置 `E2E_RUN_DIR="$PWD/.e2e/interface-integrity-final"`。流程是真实页面创建/编辑供应商、刷新读回、非法 JSON 被拒且配置不变、模型引用凭据、网页推理抵达本地上游、输出与费用对应、删除后详情 404。没有拦截管理 API 伪造结果。完整日志为本机产物，不包含在 Git 提交中；同事可用已提交用例和以上命令复现。

在原提交的独立源码快照上执行 4 个新增 HTTP 回归，得到 4 顶层和 15 子用例失败，确认旧实现会返回假成功；2 个权限子用例通过。确定性存储/正文单测修复前也有 2 顶层、13 子用例失败。初次编写 HTTP 测试时曾用错凭据详情路径、搜索更新方法、master 管理身份及标签夹具/读回入口；这些是测试错误，修正后才计验收，首次日志保留，未当作产品回归。

本批保留合法空白正文、大于 1 MiB 的既有合法 JSON、更新即创建和凭据掩码行为。数据库错误后返回脱敏 500，缺失详情 404；批量删除遇真实错误停止并返回失败，先前已删项不回滚。没有声称所有通用目录动作都已实现；文档构建和 GET 扫描也不代替完整业务验收。

后续按独立、可回退变更推进：

1. 提示词 [#137](https://github.com/qiniu/xhub/issues/137) / 搜索 [#138](https://github.com/qiniu/xhub/issues/138) 测试契约与真实执行，先明确当前页面和保留组件的差异。
2. OAuth token 与通用 JSON-RPC 假成功：本批修正范围与验证见文末边界说明。
3. 测试基础设施严格模式、清理失败和历史排除清单逐项收口。
4. 以已验证的用户或 Key 域试点权威契约和离线文档，再逐域收拢兼容层。

本轮不改线上、不进行员工账号测试、不恢复已移除入口、不缩减现有产品功能。真实供应商、真实七牛 SSO、TLS/反向代理和正式 Docker 部署仍属于独立验收边界。


## OAuth 与 JSON-RPC 边界修正（#144、#145）

在 `30e60c7` 基础上继续复核并先运行失败测试，确认了 [#144](https://github.com/qiniu/xhub/issues/144) 与 [#145](https://github.com/qiniu/xhub/issues/145)。上文表格保留初始审计事实；本节说明本批修复后的契约，不代表补齐了 OAuth 或 MCP 服务。

| 入口 | 修正后的可观察契约 |
| --- | --- |
| `GET /authorize/flow`、`POST /authorize/complete`、`POST /v1/mcp/server/oauth/{server_id}/token` | 保留 URL，匿名与已登录请求均返回 HTTP 501、XHub `error.type=not_implemented`，禁止缓存。不生成随机令牌、不把 `e2e-fixture` 标为已连接、不回显授权码或 PKCE。它们不是可用的 OAuth 授权服务器 |
| 通用 REST 管理接口的顶层 `jsonrpc` 字段 | 原有管理权限检查后，返回 HTTP 400、`error.type=unsupported_protocol`；不返回 JSON-RPC `result`，不读写目录。资源嵌套 `metadata.jsonrpc` 仍可正常存储。这是 REST 拒绝错误协议，并非 JSON-RPC 方法调度器 |
| `/credentials/token`、`/search_tools/token` 等普通资源路径 | 移除生成令牌的路径后缀判断，`token` 作为合法资源名走真实持久化、回读、修改和删除；供应商详情仍通过 `/credentials/by_name/token` 读取 |
| `/ui/mcp/oauth/callback` | 中英文均只说明正在返回、收到回调不代表授权成功；转交原始授权码或错误，不宣称令牌已交换或数据已保存 |

企业 SSO 的 `/auth/enterprise/*` 使用独立处理器。本次未修改其实现；已有本地 SAML/OIDC 测试验证隔离边界。已移除的 `/v1/mcp` 与 `/v1/mcp/oauth/token` 继续返回 404，不借此恢复 MCP 功能。

验证分层：

- [`catalog_protocol_test.go`](../../../internal/gateway/family/catalog_protocol_test.go) 和 [`onboarding_test.go`](../../../internal/gateway/onboarding_test.go)：确定性覆盖 RPC 请求/通知/坏版本、正常 token 资源、无存储失败与 OAuth 正常形状/空/坏正文。
- [`protocol_false_success_test.go`](../../../cmd/regression/protocol_false_success_test.go)：真实 HTTP、独立 PostgreSQL schema、匿名/普通用户/管理员、无副作用、普通资源生命周期、会话有效性与已移除 URL。旧实现的两个顶层回归均失败，修复后通过。
- [`page.test.tsx`](../../../frontend/src/app/mcp/oauth/callback/page.test.tsx)：双语回调在授权码、上游错误及存储失败时都不能显示已授权或已保存。
- [`catalog-persistence-scenario.ts`](../../../frontend/e2e/catalog-persistence-scenario.ts)：双语真实浏览器创建名为 `token` 的供应商、编辑/刷新/回读、拒绝 RPC 且配置不变、引用凭据完成一次本地上游推理与计费、删除后 404；同时验证 OAuth 失败契约、回调提示、真实跳转和正常登录会话。无脚本临时浏览器仅观察自动跳转前的服务端文案，常规浏览器验证回调行为；不伪造管理接口响应。

可复现命令（均在隔离测试环境运行）：

~~~bash
XHUB_REGRESSION_STRICT=1 go test ./internal/gateway/family ./internal/gateway -count=1
bash scripts/regression.sh 'TestProtocolFalseSuccess|TestCatalogIntegrity|TestProvider|TestSupplierCatalogLifecycle|TestOfficialCredentialSupportsNativeModel|TestEnterpriseSAMLHTTPFlow|TestEnterpriseProvisioningAndArchive'
(cd frontend && npx vitest run src/app/mcp/oauth/callback/page.test.tsx src/components/networking.test.ts src/hooks/useToolsOAuthFlow.test.tsx)
E2E_RUN_DIR="$PWD/.e2e/protocol-false-success-final" bash scripts/e2e.sh scenarios/regression/catalog-persistence-en.test.ts scenarios/regression/catalog-persistence-zh-CN.test.ts
~~~

外部边界：没有商业 MCP OAuth 服务、真实企业 IdP 或生产网络验证；本批证明的是未实现功能明确失败、现有资源实际执行、会话隔离和本地供应商调用，不宣称完整 OAuth/JSON-RPC 支持。无需数据迁移，未部署线上。
