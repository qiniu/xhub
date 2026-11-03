# 用户管理接口契约

Proposal [#151](https://github.com/qiniu/xhub/issues/151)，接口完整性路线图的用户管理契约批次。[OpenAPI 3.0.3](../../../contracts/users/openapi.json) 是唯一结构源，[离线参考](user-api.generated.md) 由它生成。本批不修改 handler、数据库、鉴权或预算规则，不安装严格校验中间件。

## 范围及调用方

| 操作 | 当前响应与权限 |
| --- | --- |
| GET `/user/list` | 分页 `users`；平台管理员可看全部，组织/团队管理员按受管团队收窄，普通成员看自己。不是全局账号枚举权限。 |
| GET `/user/info`、`/v2/user/info` | 都返回 `{user_info}`；详情按自身/平台管理员权限检查，不能从列表可见性推断详情可读。管理员增加 SSO 绑定字段。 |
| GET `/user/filter/ui`、`/user/available_users` | 平台管理员选择器；先取最多 500 项，再按邮箱/ID不区分大小写包含匹配；不是完整分页查询。后一个别名只在真实 mount 中声明，生成检查也读取该挂载文件。 |
| POST `/user/new` | 返回裸用户对象，不是 `data` 信封，不自动创建 Key。CSV 逐行调用这里，不存在整批导入事务。 |
| POST `/user/update` | 返回 `{user_info}`；前端旧的 `{user_id,data}` 类型已纠正。普通成员只可更新自己的资料；额度/速率/刷新日走独立授权，团队/组织管理员不能据此改身份、角色或执行全员批量。 |
| POST `/user/bulk_update` | 仅平台管理员；原子预算/刷新日变更，成功返回 `atomic/batch_id/results/total_requested/successful_updates/failed_updates`。没有部分成功。 |
| POST `/user/delete` | 返回 `{deleted:true}`；禁止删除自己，普通成员不能删除他人。删除后原 Key 和会话失效。 |

`frontend/src/components/networking.tsx` 使用本批生成的创建、列表、详情、编辑、批量、删除响应类型，保持原 URL、序列化、错误处理和请求流程。历史页面的较宽展示类型仍兼容保留，`UserInfoV2Response.teams` 已改为实际团队对象数组。旧全量 `schema.d.ts` 不在本批重写；新契约只覆盖上表，不声称所有用户相关接口已经迁移。

## 不变的兼容语义

- 单人 `max_budget`：省略保持原值（创建时无个人上限）；显式 `null` 或空字符串清空个人上限；`0` 禁止消费；也接受有限非负数字字符串。速率必须为非负整数且不超过 2147483647。字符串的完整数值语义仍由真实服务检查，OpenAPI 不冒充 Go `ParseFloat`。
- 批量预算只接受数字或 `null`，不接受数字字符串/空字符串；仅刷新日变更时必须省略预算，不能自动补 `null`。最多 1000 个唯一用户、1 MiB 正文。`all_users:true` 会包含管理员和停用用户，必须显式使用。
- `budget_reset_day` 省略保留；0不刷新，1–31北京时间零点刷新，短月取最后一天。`null`、字符串、小数和越界非法。修改预算不重置历史消费；取消个人上限仍受团队/组织等其他规则约束。
- 不允许把目标预算降低到已消费/预占以下，父子额度分配约束不变。其他用户软预算已超支不能阻塞创建新用户。不同请求的幂等性/并发语义不由结构验证推断。
- 邮箱按现有规则去除首尾空白并转小写；显示名字保留用户原始语言。
- `user_role` 默认 user，兼容 internal_user/proxy_admin 等现有拼写。未知额外字段可能被忽略，不能认为“提交字段即保存成功”；批量预算严格拒绝额外字段。普通成员自己提交管理角色不会获得该角色。
- `organization_id` 在创建接口代表组织管理员授权，**不是普通组织归属**。加入团队请使用 `team_id`。`team_role` 与团队必须匹配。
- SSO 两个字段必须同时提供；均为空为解绑、省略保持；关联范围、连接存在性及身份唯一性由服务校验。绑定变化使旧会话失效。真实身份提供方不由本契约联网验证。
- 密码长度是 8–72 UTF-8 **字节**，不能用 OpenAPI 的字符数限制冒充。管理员同时改密码和其他资料仍走既有独立处理，**不宣称整个 `/user/update` 一定是单事务**；本批原子性回归覆盖账户字段与批量预算。
- `teams` 只有列表/详情加载成员关系；创建/更新/批量快照可为空。`models`、`organizations`、`metadata`、`key_count` 等是现有兼容占位，不能用来推断有效模型、组织权限或实际 Key 数量。`sso_user_id` 不等于新企业身份外部ID。
- 分页保留 `total/total_count`、`page/current_page`、`page_size/size` 别名；无效 page 回退1，page_size超出1..500回退100。查询参数保留旧字符串解析，不添加线上严格校验。详情兼容 GET 正文 `user_id` 优先于 query；建议新调用方使用 query。

## 结构验证的边界

契约校验器只接受已声明操作与受支持的 JSON 对象输入，响应必须为声明状态和 JSON 结构，错误 `code` 必须对应 HTTP 状态，不用 `default` 吞未知状态。OpenAPI 无法单独证明鉴权、SSO成对/唯一性、用户ID唯一性、预算分配或真实计费，必须配合实际 HTTP/数据库/上游回读。

创建、删除、详情的旧 `readMap` 对畸形输入有宽松处理，本批没有改成严格解析，也**不声称所有坏JSON都会400**；更新及批量的严格坏JSON路径有真实回归。503预留存储不可用形状，未在此批注入真实503；不是已覆盖所有状态来源。密码重置专用接口、统计分析、目录同步、全量旧schema、真实企业IdP/供应商和生产并发不在本批契约范围。

Go 补丁/联合输入使用 `json.RawMessage` 保留缺字段、显式null及原始值；不能仅凭 Go 编译通过认定输入合法。响应仍生成结构体，TypeScript保留已知字段约束。生成器模板注释保留标准格式，中文语义在源schema维护，不手改产物。

## 验证和维护

```bash
npm --prefix frontend run check:api:users
npm --prefix frontend run check:api:suppliers
node --test frontend/scripts/gen-user-contract.test.mjs frontend/scripts/gen-supplier-contract.test.mjs scripts/test_quality_workflow.cjs
go test -race ./contracts/...
bash scripts/regression.sh '^Test(UserContract|UserBulkBudget|UserMonthlyBudget|UserEnterpriseIdentity|SoftBudgetAllocationHTTP|SupplierContract)'
E2E_RUN_DIR="$PWD/.e2e/user-contract" bash scripts/e2e.sh \
  scenarios/regression/user-csv-import-en.test.ts \
  scenarios/regression/user-csv-import-zh-CN.test.ts \
  scenarios/regression/user-bulk-budget-en.test.ts \
  scenarios/regression/user-bulk-budget-zh-CN.test.ts \
  scenarios/regression/user-enterprise-identity.test.ts
```

`User API contract` CI独立执行类型漂移检查、生成器失败路径单测、契约校验和真实HTTP/PG回归；原发布E2E门禁继续执行用户流程。供应商契约复用同一生成器和验证器，本批也回归其检查以防基础设施抽取退化。

新增真实回归覆盖九个操作、分页别名、无自动Key、冲突、预算状态、普通/团队/组织管理员边界、企业绑定、数据库写失败不修改记录；既有回归验证月度刷新、父子分配、已有超支及身份解绑。浏览器覆盖中英文CSV下载/错误预览/创建/重复失败/登录/清理，批量预算保存/拒绝/恢复推理/真实用量，及本地TLS IdP绑定/登录/解绑失效。所有数据库及模型调用均为隔离测试环境，不使用员工账号或供应商密钥。

修改源后运行 `npm --prefix frontend run gen:api:users`；检查模式不自动修复漂移。生成工具版本沿用供应商试点，Go工具及npm初次下载需要网络，生成文档离线可读。回退本批只需回退代码与文档，无数据迁移或生产配置需要回滚。

## 本批实际验证记录（2026-10-24）

最终选定验证：契约 Go 单测 3 个包通过；生成器/工作流 13 项通过；前端单测 94 项通过；真实后台回归 26 个叶用例通过、0跳过；中英文 E2E 6项通过、0跳过，测试和清理退出码均为0。用户契约代码覆盖率100%，公共验证器97.2%；这不等于整个用户管理功能100%覆盖。全量前端 `tsc --noEmit --incremental false`、`go vet ./contracts/... ./cmd/regression`、双契约漂移检查均通过。ESLint现有217项错误，低于既有289门槛，本批未放宽预算。

前端实际命令（frontend目录执行）：

```bash
npx vitest run src/components/networking.user-contract.test.ts src/components/networking.test.ts \
  'src/app/(dashboard)/hooks/users/useCurrentUser.test.ts' \
  'src/app/(dashboard)/hooks/users/useUsers.test.ts' \
  --reporter=json --outputFile=../.verification/user-contract/frontend-unit.json
npx tsc --noEmit --incremental false
```

本地报告：`.verification/user-contract/frontend-unit.json`、`contract-unit.log`、`generator-workflow-final.log`、`typecheck.log`；后台报告 `.e2e/runs/regression-BQUBjddM/regression.log`；浏览器报告 `.e2e/user-contract-final/html/index.html`、`results.json`、`cleanup-status.json`。这些原始产物不提交，CI会按上述命令重新生成可下载证据。

首轮失败用于校正测试前置和生成契约：通用租户夹具默认带组织管理权，需撤销后才可测试普通成员；邮箱小写化、越范围预算403按现有规则断言；纯 `required` 的 anyOf分支曾使TS生成 `unknown`，负向编译测试拦住后改用完整分支。未通过放宽业务规则或忽略失败来让测试变绿。
