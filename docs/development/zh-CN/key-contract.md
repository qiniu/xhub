# 密钥管理接口契约

对应 #153；叠加用户契约 #152，沿用供应商试点的固定生成器和开发期校验器。本批不替换生产 handler、不改权限或预算规则、不迁移数据库，也不修改模型协议链。

## 范围与迁移边界

唯一源为 `contracts/keys/openapi.json`，生成 Go 类型、TypeScript 类型及[离线参考](key-api.generated.md)。覆盖八个操作：

| 操作 | 成功结果 |
| --- | --- |
| `POST /key/generate` | 含 `key` 明文的 Key 对象，`token` 同为明文 |
| `GET /key/list` | `keys/total_count/current_page/total_pages/size` |
| `GET /key/info`、`POST /v2/key/info` | `{info: ...}`，含未结算占用 |
| `POST /key/update` | Key 对象，无 `data` 包装，不含明文 |
| `POST /key/block`、`POST /key/unblock` | 明确的 `blocked: true/false` |
| `POST /key/delete` | `{deleted: 数量}`，不是布尔值 |

本批只把前端创建、更新和删除的响应类型接入生成契约，并修正相关页面声明中的 `null`；请求序列化、网络路径和运行逻辑保持原样。其他列表/详情客户端和旧聚合视图只部分迁移，旧聚合视图中大量继承字段不是后台支持证据。路由仍由原挂载文件注册；不把生成服务端/新客户端接入生产。

尚未纳入：轮换/重生成、重置消费、批量编辑、aliases、health、团队/模型/日志接口以及整份旧 `schema.d.ts`。轮换仅作为已有 E2E 的附加兼容验证，不宣称已建立其新契约。

## 已确认语义

- 默认创建当前用户个人 Key；显式 `owner_type=personal` 必须提供用户。团队从用户的成员关系派生，无团队也可创建。归属、模板可见性、模型范围仍由真实权限链验证。
- 创建省略/空预算不分走用户额度。更新省略 `max_budget` 保留，`null`/空串取消独立上限，`0` 禁止该 Key 消费。个人、团队、组织限制仍生效，创建多个 Key 不重置或预分配个人消费。
- 单个预算接受有限非负数字或可解析字符串；速率要求非负 int32 整数。OpenAPI 不模拟全部数字字符串解析规则，实际合法性由后台判定，非法值真实 HTTP 测试覆盖。
- `models` 空列表或 `all-proxy-models`/`all-team-models` 哨兵恢复继承，不授予超出父级的权限。`route_template_id` 省略保留，`null`/空串清除并继承；回归同时检查有效模板来源和真实推理。
- `duration` 创建为空不设到期；更新省略、`null`、空串保留旧到期，不能用它们撤销期限。非空必须是正整数加 s/m/h/d，溢出拒绝。
- `key_type=llm_api`、`allowed_routes=[llm_api_routes]`；不支持管理 Key、自动轮换或宽限期。相关固定字段是明确限制，不是未实现能力的成功承诺。
- 非创建读取不返回 `key` 或存储 hash。普通列表/详情 `token` 为遮罩，个人列表 `token` 为记录 ID。支持用记录 ID 查详情；不要把遮罩当作调用凭据。
- 详情 `budget_reservations` 没有记录时可能为 `null`，非空时是实际 `BudgetReservation`，不是另一管理页面的 `BudgetReservationView`；累计未结算额度另在 `budget_pending`。
- `scope=personal` 收窄到当前登录用户，管理员也不能借该入口读他人；`user_id` 查询不能扩大范围。一般列表仍沿既有管理权限，不改变权限默认值。
- 分页 page=1..100000、size=1..100；默认 1/50。非法分页或排序为400，空结果总页数1。`key_hash` 历史名字实际按记录 ID 筛选。
- 删除先解析并授权全部目标，再逐个执行数据库删除。越权混合批次不会开始删除，但中途存储失败可能部分删除，不是整批事务；重复删除404。本批不改变此语义。
- 错误码必须与 HTTP 状态一致，未知状态/HTML/重定向不可作为有效契约响应。没有全局成功兜底。

## 不隐藏的缺口

1. 未使用的旧 `keyInfoCall` 发送 `{keys:[...]}`，而 `/v2/key/info` 实际要求单个 `key`。真实回归确认前者400；本批不因旧函数存在就声明批量能力，也不改变该函数。
2. [#154](https://github.com/qiniu/xhub/issues/154) 已由后续修复移除状态兜底：仅接受与目标一致的明确布尔确认。空、缺字段、类型错误或相反状态显示“无法确认，请刷新核对”；不能把未知结果当成功，也不宣称后台未写入。详见下方追加验证。
3. `organization_id` 按 Key 所属用户当前团队的组织精确匹配，与原权限范围、个人范围和其他筛选取交集，计数和分页在筛选后计算。空值不限制组织；非空时无团队 Key 不匹配；组织管理员身份不决定个人 Key 的组织归属。`status/expand/return_full_object/include_team_keys/include_created_by_keys` 仍被忽略，不声明为有效过滤能力；未发现启用中的已删除 Key 页面入口。
4. 创建及部分删除/详情解析是宽松旧实现；契约支持单个 JSON 对象，不承诺所有未声明/畸形输入都会被旧 handler 拒绝。严格更新畸形 JSON 已做真实400及数据保持测试。

## 验证与维护

验证分层：生成器负例检查缺源、坏 JSON、未知路由和四种产物漂移；Go 单测检查字段/状态/缺省/null/0及禁止读取响应多出凭据；TS 负例阻止缺目标的更新/删除、批量详情、管理 Key 和错误响应类型。真实 HTTP/PG 检查八个操作、权限隔离、占用、数据库写失败、模板继承、模型调用计费、禁用恢复和删除失效。浏览器用英文和中文各走个人多 Key 共享预算/控制台调用，以及创建、禁用恢复、编辑到期、轮换、删除完整流程。

```bash
npm --prefix frontend run check:api:keys
npm --prefix frontend run check:api:users
npm --prefix frontend run check:api:suppliers
node --test frontend/scripts/gen-key-contract.test.mjs frontend/scripts/gen-user-contract.test.mjs frontend/scripts/gen-supplier-contract.test.mjs scripts/test_quality_workflow.cjs
go test -race ./contracts/...
go vet ./contracts/... ./cmd/regression
bash scripts/regression.sh '^Test(KeyContract|SharedPersonalKeyBudgetHTTP|PersonalKeysIsolation|ModelLimitChain|TeamlessPersonalKeys)'
E2E_RUN_DIR="$PWD/.e2e/key-contract-final" bash scripts/e2e.sh \
  scenarios/regression/shared-key-budget.test.ts \
  scenarios/regression/key-lifecycle-en.test.ts \
  scenarios/regression/key-lifecycle-zh-CN.test.ts
```

独立 Key API contract CI 检查生成漂移、类型、验证器和真实数据库回归，保留日志并清理。E2E 沿用发布验证门禁。生成用 `npm --prefix frontend run gen:api:keys`，检查命令不会自动修复漂移；首次工具下载需网络，生成参考离线可读。

外部边界：模型上游使用可控本地 HTTP 服务，不测试真实供应商或生产身份；没有新增生产配置/数据迁移。503/409通用错误形状已声明但本批未针对每个接口注入这些状态；也不覆盖生产负载与并发删除故障。回退只需撤回契约、类型和门禁，不需要数据库回滚。

## 本次实际验证（2026-10-24）

- 四个 Go 契约包 race 通过；生成器/工作流16项通过，最终工作流自测9项通过；前端138项通过、0失败。
- 最终后台回归8个叶用例通过、0失败；1个真实外部供应商 live 子例按离线模式跳过，不计入通过。之前只跑本批四个契约用例与两项既有预算/隔离回归时为6通过、0跳过。
- EN/zh E2E 4项通过、0失败/跳过，`test_exit=0`、`cleanup_exit=0`；范围是本批2条双语场景，不是全项目业务场景（当前目录417条）完成。
- 三领域生成漂移与TS负例、全前端类型检查、Go vet均通过。ESLint仍为217个历史错误，未增加，沿用289预算；不把“门禁通过”描述成零静态问题。
- 首轮构建暴露旧页面把真实null声明成非空类型，已修正声明。首轮新增组件断言把明文展示误当输入框，按实际`pre`文本展示修正后138项通过。未放宽业务校验或失败门禁。

实际额外命令（在 `frontend` 目录）：

```bash
npx tsc --noEmit --incremental false
npx vitest run src/components/networking.test.ts \
  src/components/organisms/create_key_button.test.tsx \
  src/components/organisms/create_key_button.integration.test.tsx \
  src/components/templates/keyEditFormValues.test.ts \
  src/components/templates/key_info_view.test.tsx \
  'src/app/(dashboard)/hooks/keys/useSetKeyBlockedState.test.ts' \
  --reporter=json --outputFile=../.verification/key-contract/frontend-unit.json
npx eslint . --quiet --format json --output-file ../.verification/key-contract/eslint.json --pass-on-unpruned-suppressions
node scripts/check-eslint-error-budget.mjs ../.verification/key-contract/eslint.json 289
```

本地报告：`.verification/key-contract/frontend-unit.json`、`unit-go.log`、`generator-tests.log`、`workflow-final.log`、`full-typecheck.log`、`vet.log`、`eslint-budget.log`；后台 `.e2e/runs/regression-J1Uxq05D/regression.log`；浏览器 `.e2e/key-contract-final/html/index.html`、`results.json`、`cleanup-status.json`。原始运行产物不入Git，CI重跑并上传报告。

## 状态确认修复追加验证（#154，2026-10-24）

仅修改 `useSetKeyBlockedState` 的响应确认与双语提示，权限、预算、后台状态写入和数据库结构不变。只有与目标一致的布尔 `blocked` 才触发成功回调；未知结果保留确认框并提示刷新核对。自动化 E2E 已加入场景目录。

- 先补复现单测：旧实现50项中43失败、7通过；修复后该文件50项及详情组件13项，共63通过、0失败。
- 真实后台5项通过、0跳过；新增状态写失败用PG约束注入，验证500、持久状态保持、恢复后明确确认，以及禁用401/恢复200的真实模型准入。首轮测试复用提示词命中缓存，已改为逐次不同英文提示词以验证真实上游调用，未放宽断言。
- EN/zh E2E 4项通过、0失败/跳过，测试与清理退出码均为0：新场景分别覆盖未提交写入的坏响应、真实写入后回复被丢弃、刷新回读、正常禁用恢复及数据面；已有场景覆盖创建、编辑、到期、轮换、删除。故障响应仅在浏览器边界注入，不冒充真实后台会返回坏200；持久状态与推理均走真实网关/PG。
- 前端类型检查、Go vet、报告器单测8项通过、0失败。ESLint217个历史错误，未增加，既有预算289未变。
- 独立筛选审计：组织不匹配、`status=deleted` 两个预期为空的真实请求均返回有效Key，2项失败作为修复前的缺陷证据，未算通过。#156 的组织问题现由 `TestKeyContractOrganizationFilter` 和双语组织筛选 E2E 覆盖；`status=deleted` 不在本修复范围。

实际命令（从仓库根执行；`npx` 三条在 frontend 下执行）：

```bash
npx vitest run 'src/app/(dashboard)/hooks/keys/useSetKeyBlockedState.test.ts' src/components/templates/key_info_view.test.tsx --reporter=json --outputFile=../.verification/key-state/unit.json
npx tsc --noEmit
npx eslint . --quiet --format json --output-file ../.verification/key-state/eslint-final.json --pass-on-unpruned-suppressions
node frontend/scripts/check-eslint-error-budget.mjs .verification/key-state/eslint-final.json 289
node --test frontend/e2e/checklist-reporter.test.cjs
go vet ./cmd/regression
bash scripts/regression.sh '^TestKeyContract'
E2E_RUN_DIR="$PWD/.e2e/key-state-registered" bash scripts/e2e.sh \
  scenarios/regression/key-state-confirmation-en.test.ts \
  scenarios/regression/key-state-confirmation-zh-CN.test.ts \
  scenarios/regression/key-lifecycle-en.test.ts \
  scenarios/regression/key-lifecycle-zh-CN.test.ts
```

报告：`.verification/key-state/repro.json`、`unit.json`、`typecheck.log`、`eslint-final.json`、`vet.log`、`reporter-final.log`；后台 `.e2e/runs/regression-iUEW1nLJ/regression.log`；浏览器 `.e2e/key-state-registered/results.json`、`html/index.html`、`cleanup-status.json`。独立缺陷证据在 `.e2e/runs/regression-fAFBzhat/regression.log`；复现源码 `.verification/key-state/key_filter_audit_test.go.txt`。产物不入Git，CI执行实际测试。

边界：本地受控模型上游，未调用真实供应商、未验证生产网络丢包发生频率；本批没有线上操作、合并或部署。回退撤销前端确认校验即可，无数据库迁移。
