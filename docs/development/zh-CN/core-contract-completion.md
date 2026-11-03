# 第三阶段核心接口契约收尾

[English](../en/core-contract-completion.md) · [迁移清单](../../../contracts/migration-inventory.json) · [计划](interface-integrity-roadmap.md)

本批接续模型契约 #160，实现既定的供应商、用户、Key、模型管理、日志/日报核心集合。权限、预算、存储和生产部署不变。第三阶段验收针对下表集合；它不是全量 API 可用证明，也不代表第四阶段服务边界或第五阶段数据面执行链已完成。

## 迁移集合和证据

| 领域 | 操作数 | 前端入口 | 真实后台证据 | 浏览器证据 |
| --- | ---: | --- | --- | --- |
| 供应商 CRUD | 5 | 供应商管理客户端 | `SupplierContract*` | supplier-contract 场景（前批） |
| 用户 CRUD/筛选/批量预算/密码 | 11 | networking 用户管理、userSetPasswordCall | `UserContract*`，新增 `UserContractPassword` | 用户契约（前批）、password admin-reset |
| Key 完整注册生命周期 | 14 | networking Key管理、regenerateKeyCall、keyAliasesCall；无页面调用的bulk/reset/health保留HTTP契约 | `KeyContract*`，新增 `KeyContractRemaining` | key-lifecycle en/zh-CN |
| 模型/端点 CRUD、启停和探测 | 9 | modelInfoCall、modelCreateCall、modelPatchUpdateCall、modelDeleteCall、testConnectionRequest | `ModelContract*` | model-contract en/zh-CN |
| 日志列表/详情/会话及用户/团队/组织日报 | 9 | uiSpendLogsCall、uiSpendLogDetailsCall、sessionSpendLogsCall、fetchDailyActivity、userDailyActivityAggregatedCall | `UsageContractRealTraffic` | core-contract、usage-error en/zh-CN |

各领域 OpenAPI 为唯一类型源，生成 Go/TypeScript/双语离线文档。生成器检查漂移，真实 HTTP 回归以独立验证器检查响应；TypeScript 类型本身不是运行时验证器。列出的旧批测试证据不得冒充本批重新执行，具体本批运行命令与结果见 PR。

`contracts/migration-inventory.json` 是目录与 Go 字面量直接注册的静态并集，逐条标记 `core-contracted` 或 `peripheral-unmigrated`；新路由或契约变化未重新生成时 CI 失败。`catalog` 声明可能已退役或并无有效实现，**不能据此计算运行时 API 覆盖率**。动态拼接 SCIM、Bypass registry 另列边界。

## 统一前端错误行为

迁移客户端使用既有 ApiClient，保留 HTTP status、body 和实际错误信息；模型列表 403 不再伪装为空成功。日志/日报失败不得返回零统计。连接探测的 HTTP 200 仍可能 `healthy:false/status:error`；非2xx 抛出 ApiError，由页面显示真实失败。自定义Key失败仍需核验 `/auth/me`，不能直接清登录会话。

旧的未被任何页面调用的 `keyInfoCall(keys[])` 已移除：服务实际只接受一个 `key`，不以虚假数组调用充当已实现能力。AgentBuilder 创建结果读取真实的 `model_info.id`；这不证明其供应商执行能力已经完整实现。

用量缓存 token 未知时保持字段缺省。界面已初始化的汇总桶保留原有计算行为，仅细化类型；没有新增计费规则。日志正文允许未知 JSON 和非 JSON 文本，不以当前模型配置重写历史。

## 如实记录既有边界

- Key bulk_update 为逐项尽力更新，失败项跳过，`updated` 可为0，不是原子操作。客户端应比较结果数，不能只看200；本批不重定义授权或事务规则。
- reset_spend 仅重置Key计数，不删除历史记录，也不重置用户或父级预算。
- 密码重置撤销登录会话，保留API Key；短密码目前经既有IAM错误映射返回500，本批真实测试锁定该事实，未将其写成400承诺。
- 空session_id的日志查询既有实现会在鉴权前返回空页；不泄露数据，不代表凭据有效。
- 日志详情范围外与不存在均404；管理员跨用户读正文必须记审计。日志存储缺失正文可能为空，不保证每个拒绝都生成账单。
- 日志page无效回退1、page_size超出1..200回退50；日报无效日期为开放边界。未经实现的旧查询参数不承诺有效。

## 外围未迁移集合（明确不计本批完成）

- 组织/团队成员生命周期、SSO/SCIM目录同步及身份映射：单独的企业身份/层级管理边界，保留现有客户端与回归，未生成本批核心CRUD契约。
- 模型公开发现、定价目录刷新、默认/回退路由配置及内置模型批量发现：执行与配置领域，保留现有行为，不以模型CRUD测试冒充覆盖。
- 其他全局统计、模型监控、护栏报表、标签/客户/Agent统计、预算对账管理：不纳入日志/日报集合。
- MCP/OAuth、搜索、提示词、存储、通知、视频等兼容目录，以及原生/跨协议推理：后续按真实服务边界整理，不机械生成“已实现”文档。

这些边界在总清单可查，不能宣称“所有管理功能全量迁移”。如果第三阶段范围扩展至这些领域，应继续逐领域契约化；不得降低现有70% patch覆盖率门禁或拿静态枚举代替真实测试。

## 本地验证与回退

生成/漂移：`node frontend/scripts/gen-{key,user,usage}-contract.mjs --check`（逐个执行）；清单：`node frontend/scripts/contract-inventory.mjs --check`。
单元：`go test ./contracts/...`、Node生成器/工作流测试、Vitest客户端与页面测试；真实回归使用 `scripts/regression.sh`；浏览器使用 `scripts/e2e.sh`，报告保存于 `.e2e/<run>/`。
覆盖率必须来自实际执行并上传；无数据库、零匹配、跳过、清理失败不能算通过。外部真实模型商、生产SSO未调用，本地受控HTTP上游只证明网关契约和数据链。
没有数据迁移或新开关；逐域回退前端客户端及契约提交即可，不需要恢复数据库。不要将契约检查工具接入生产请求链。

## 收尾复核与兼容门禁

#162 的合并目标是 `codex/model-endpoint-contract`；#160 先进入 main 后，该分支才接收 #162。重新向 main 提交这批代码，不能以两个 PR 的 merged 状态当作主分支包含关系的证据。

- 团队汇总客户端 `teamDailyActivityAggregatedCall` 使用生成的 `DailyUsage` 类型，类型反例防止回退为 `any`；双语用量恢复 E2E 观察真实团队汇总响应和可重试界面。
- `TestUsageContractScope` 在两个独立组织/团队产生真实流量，验证普通平台用户的日报、混合团队/组织筛选、跨账号日志与同名会话隔离，以及管理员分页。范围外 `user_id` 取交集返回空统计，范围外团队/组织列表返回404；锁定既有规则，不改授权和预算。
- 兼容检查使用固定版本 [oasdiff v1.11.7](https://github.com/oasdiff/oasdiff/tree/v1.11.7)，对比 PR 基线 SHA 与工作树，`WARN` 和 `ERR` 都阻断。新领域执行自比较以验证工具可解析，明确标注为新增而非向后兼容证明。旧领域删除、无基线、零契约、非法JSON/引用、工具故障均失败。
- 门禁实测接口删除、请求必填项增加、引用字段类型变化、响应字段删除/类型变化；正常新增可选字段和文档修改可以通过。生成漂移、真实 HTTP 验证和兼容比较分别负责不同风险，不能互相替代。语义、权限、预算、计费与 SSE 的兼容仍依赖真实回归。
- 本地命令：`go install github.com/oasdiff/oasdiff@v1.11.7`，然后 `node frontend/scripts/check-contract-compatibility.mjs origin/main`。CI 使用实际 PR base SHA；手动任务使用父提交，缺历史不能自比较伪通过。工具只用于开发/CI，不进入生产依赖。
- CI 保留整体覆盖率门禁和70% patch门槛，不通过忽略文件或减少真实用例使结果变绿。所有上传完成前的部分 Codecov 结果不能代替最终验收。

核心测试之外的企业SSO、真实商业模型上游和线上反向代理未执行；本批不合并、不部署。阶段4/5尚未开始。
