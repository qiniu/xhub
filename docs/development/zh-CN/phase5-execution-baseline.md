# 第五阶段执行链基线与首批验收

日期：2026-10-29。关联 Proposal #241。基线 main `09273d4d`。

## 固定进度

| 工作包 | 本批状态 | 仍需完成 |
| --- | --- | --- |
| P0 行为与测试基线 | 完成首轮代码归属、测试映射与首批验证，缺口如下 | 后续每批实施前补对应动态场景和性能测量，不把测试文件存在当作已验收 |
| P1 Bypass 解析去重 | #242 已提交；初版 CI 通过，补齐双语场景登记后重跑 | 未合入、未部署；外部验收另列 |
| P2 请求配置视图 | 模型目录隔离、共用选路目录及热更新/安全重查已本地验证 | 价格与连接跨更新组合、多存储读取边界仍需审计，不宣称整个工作包完成 |
| P3 单次尝试结果 | 待实现 | HTTP/客户端提交/协议终态、取消和失败的共同事实 |
| P4 超时重试与资源释放 | 待实现 | 保留各入口差异，抽取安全公共部分 |
| P5 历史日志与结算编排 | 待实现 | 保留持久化顺序、所有权与结算幂等性 |
| P6 组合验收 | 待执行 | 对最终组合跑完整矩阵，不能用本批结果替代 |

## 入口、状态与副作用归属

| 边界 | 当前调用与状态归属 | 基线 / 后续约束 |
| --- | --- | --- |
| 标准入口 | `execution.Serve`；身份 → 正文 → 授权历史 → 预算/护栏 → 模板/缓存 → 候选 → 尝试 → 历史/记录 | 原生 Responses 有能力和账号条件，Messages 签名历史可走专用桥接；不统一成 Bypass |
| Bypass 入口 | `gateway.serveBypass` 通过 registry 匹配，转 `execution.ServeBypass`；部分公共 Google 请求转标准入口 | 不新增路径或供应商能力；创建/计数与任务后续操作分支保持 |
| 入站解析 | `ParseBypassBody` 保存原文、字段投影、multipart 分段；本批直接传入 `serveBypassCreate` | 原始数字依靠 RawMessage 出站保留；Fields 中的 JSON number 仍是既有 float64 投影，不能当无损正文 |
| 候选载荷 | `BypassBody.Payload` 根据每个候选改写模型、去除 session_id/disable_fallbacks | 不污染原文和下个候选；普通重复表单字段、文件头/顺序保留 |
| 路由计划 | `affinity.State.PlanRoute` 读取会话信号和授权历史，返回 request-local `RoutePlan` 与回调 | 本次核查 Bypass 读投影路径没有原地改写字段；不能把调用方输入作为历史授权 |
| 路由配置 | `Models` 在锁内复制目录和嵌套容器，执行器将同一目录传给 `RouteSettingsFor` 与候选选择 | 不宣称跨存储事务一致；权限、预算、凭据按原时点重查 |
| 凭据/预算 | `AttachCredential` 在候选层补连接；尝试层重新鉴权/限额；`PrepareBudgetAttempt` 可能再次改写实际出站正文 | 不删除每次尝试的安全检查，不提前缓存凭据或冻结撤销状态 |
| 转发/流测量 | `ForwardOfficial` 单次发送，SSE 保留原行，独立观察终态/用量 | 测量解析的是经过模型及预算处理的出站正文，P1 保留这次解析，不能复用入站投影假装等价 |
| 重试/取消 | 两类执行器分别管理超时、候选队列、提交状态和取消路径 | 同步、异步创建、固定历史不能共用无差别重试；P4 保留真实上游调用次数断言 |
| 历史/任务 | `RoutePlan` / CommitRoute 与 PinOfficial(Context) 分别管理对话归属和任务创建事实 | 对话终态保存顺序、任务调用方/传输隔离和过期行为保持，P5 不合并成通用缓存 |
| 日志/结算 | `CallNote` 记录尝试事实；宿主 `RecordSpend` 负责持久化/费用，异步任务另用 SettlementID | 调用 ID、原任务 ID、结算 ID 分工保留，不凭请求次数判断账单数 |

这份归属表是代码审计，不是所有并发行为的运行证明。原实现中共享配置是否存在具体竞态，需要 P2 用可重复热更新场景确认；本次未将其报为已复现 Bug。

## 行为矩阵与本次证据

| 范围 | 测试入口 | 本次证据 / 留待边界 |
| --- | --- | --- |
| JSON 原生扩展、大整数、null/空/零、多个候选、路径模型 | `transport.TestBypassBodyJSONContract`、`regression.TestBypassBodyHTTPContract` | 改写保持语义；真实 HTTP 上游接收正确字段，日志/费用核对 |
| multipart 文件/头/顺序/重复普通字段、错误 boundary/重复 model | `transport.TestBypassBodyMultipartContract`、`TestBypassBodyRejectedInputs`、`regression.TestBypassMultipartHTTPContract` | 二进制及文件名保留，非法请求无上游调用或费用；图片规格既有单测同时运行 |
| 原生/转换五协议普通与流式 | `TestEndpointDialogueMatrix`、`TestNativeResponsesBypassBoundary`、`TestNativeResponsesCrossProtocolHTTP` | 本地协议 HTTP 上游 + 真实网关/数据库通过；不代表真实供应商全部能力 |
| 流缺终态/终态不关闭/客户端取消 | `TestStreamMissingSemanticTerminal`、`TestStreamTerminalWithoutEOF`、`TestNativeAgentCancellation` | 真实 HTTP 回归通过；执行/transport 全套单测覆盖中途失败与重放边界 |
| 历史与所有权 | `TestNativeEncryptedHistoryOwnership`、`TestResponsesIncrementalContinuation`、`TestNativeMessagesAgentHistory` | 真实数据库回归通过；双语 Agent 工具历史浏览器通过 |
| 日志、计费、任务去重 | 新载荷回归、`TestSeedanceSettlementUsesMeasuredBandAndDeduplicates`、`execution.official_settlement_test.go` | 同步/流式成功与上游拒绝、任务轮询及单元持久化失败路径通过 |
| 路由/配置热更新 | `routing`、`execution` 既有单测；配置实现阅读 | 单测已运行；热更新期间的真实 HTTP 并发、凭据轮换组合列入 P2，未宣称完成 |
| 资源与历史持久化故障组合 | 既有 stream/history/settlement 单测 | 本批运行单测；最终组合动态故障矩阵属于 P4–P6 |
| 真实媒体/搜索/企业 SSO/外部 CLI | 未运行 | 外部依赖边界，不计入本批通过数，不影响纯解析去重的本地验收结论 |

## P1 实现与取舍

只导出现有请求载体为 `transport.BypassBody`，由 `ServeBypass` 传入 `serveBypassCreate`，删除同一入站正文的第二次解析。不另建包、不增加全局状态、不改变解析算法或错误优先级。

原生字段测量仍解析实际出站正文，除了模型替换，预算准备也可能改写出站字段。这次没有为省一次解析而提前移动测量，也没有修改 pricing blocked、上游错误或重试语义。

通过源码清点确认 `serveBypassCreate` 只有一个生产调用方；`ParseBypassBody` 保留入口与流测量两个生产调用点。类型导出仅发生于 internal 包，不改变公开 HTTP/OpenAPI。

## 实际测试结果

| 执行 | 结果 |
| --- | --- |
| 首次新增单测基线 | 162 叶子通过、1 失败、2 外部 live 跳过；失败是测试错误假设空 multipart 在解析层拒绝 |
| 修正夹具后旧实现单测基线 | 164 叶子通过、0 失败、2 外部 live 跳过 |
| 旧实现真实 HTTP/PG 基线 | 82 叶子通过、0 失败/跳过 |
| 可撤销丢字段变异 | 单元和真实 HTTP 回归均退出 1；报告保留，故障已撤销 |
| 修改后 dataplane/provider 单元及 race | 605 叶子通过、0 失败、2 外部 live 跳过 |
| 修改后真实 HTTP/PG 回归及覆盖率 | 82 叶子通过、0 失败/跳过 |
| 双语浏览器 | 6 通过、0 失败/跳过/重试，test_exit=0、cleanup_exit=0 |
| quality/release 工作流测试 | 83 通过、0 失败/跳过 |
| Go vet、场景 ESLint、文档构建 | 均退出 0；文档构建 443 页与 2 个首页 |

两个既有 live 跳过是 `TestQiniuSeedanceLive`、`TestQiniuFalLive`，需要真实供应商凭据，未冒充通过。浏览器仅选择本批相关流程，不将全场景目录中未选择的用例计入失败或通过。

首次失败经核对是 MIME 解析把只有起始边界的载荷视为空表单，缺少 model 随后仍被执行层拒绝。保留该既有行为，增加空表单断言，并使用实际被截断的非空分段验证解析错误，没有修改解析器使测试变绿。

故障注入只在本地临时删除出站 `opaque` 字段，分别执行单元与真实回归，均出现字段保留断言失败，再用 finally 恢复原文件。它证明测试能发现损坏，不是声称旧产品原本丢字段。

## 可复跑命令和报告

在专用隔离 PostgreSQL/Redis 测试环境执行：

```bash
XHUB_REGRESSION_STRICT=1 go test -race ./internal/dataplane/transport ./internal/dataplane/execution -count=1 -json
XHUB_REGRESSION_STRICT=1 go test -race ./internal/dataplane/... ./internal/provider/... -count=1 -covermode=atomic -coverprofile=.verification/phase5/P1/unit.out -json
XHUB_REGRESSION_COVERPROFILE="$PWD/.verification/phase5/P1/regression.out" bash scripts/regression.sh '^Test(BypassBodyHTTPContract|BypassMultipartHTTPContract|NativeResponsesBypassBoundary|NativeResponsesCrossProtocolHTTP|EndpointRedesignNativeLifecycle|EndpointDialogueMatrix|NativeEncryptedHistoryOwnership|NativeAgentCancellation|StreamMissingSemanticTerminal|StreamTerminalWithoutEOF|ResponsesIncrementalContinuation|NativeMessagesAgentHistory|SeedanceSettlementUsesMeasuredBandAndDeduplicates)$'
E2E_RUN_DIR="$PWD/.e2e/phase5-p1" bash scripts/e2e.sh scenarios/regression/agent-history-bypass-body.test.ts scenarios/regression/agent-history-en.test.ts scenarios/regression/agent-history-zh-CN.test.ts scenarios/regression/upstream-stream-errors.test.ts
node --test scripts/test_quality_workflow.cjs scripts/test_release_workflow.cjs
go vet ./internal/dataplane/... ./internal/provider/... ./cmd/regression
(cd frontend && ./node_modules/.bin/eslint --no-ignore e2e/scenarios/regression/agent-history-bypass-body.test.ts)
npm --prefix website run build
go test ./internal/dataplane/transport -run '^$' -bench '^BenchmarkBypassBodyProjection$' -benchmem -count=3
```

本地证据：`.verification/phase5/P1/` 下的 before/after unit JSON、before/after regression 日志、mutation.json 与失败日志、覆盖率文件和 workflow.log；真实 regression 的原始事件与清理随脚本保存在 `.e2e/runs/`；浏览器报告、trace 和清理在 `.e2e/phase5-p1/`。这些本地路径是附加证据，PR 中的测试源码、可复跑命令与 CI artifact 才是共享复核入口。

基准测量仅记录单次解析成本：小 JSON 约 386–389 ns / 736 B；1 MiB JSON 约 2.02–2.10 ms / 1.05 MB；1 MiB multipart 约 5.25 MB 分配。它说明移除一次重复解析的成本来源，不是端到端加速比例；并发、首字延迟及整体内存预算随 P2–P4 另做真实测量。

CI 复用模型契约作业采集新 HTTP 回归覆盖率并上传 backend flag；发布 E2E 现有 `agent-history-` 选择器自动包含新场景。70% patch 门禁不变，没有增大超时或排除用例。

## 回退与限制

本批无数据迁移、配置/权限/预算调整。回退这两个生产文件即可恢复旧解析调用，账单、历史、任务和配置保留；测试和文档可继续保留。未连接生产、未合并或部署，未新增灰度开关，因为本批没有新旧业务语义切换。

第五阶段剩余 P2–P6 仍按[总计划](phase5-execution-plan.md)推进。P1 本地通过不代表第五阶段全部完成，也不替代后续组合或生产验收。

## 模型目录隔离与热更新验收

旧 `Models` 暴露共享切片和嵌套 map。新增单元修改返回值后发现进程配置被污染；恢复旧 getter 运行真实 HTTP 回归，两个入口均在停用后检测到旧目录被改写。修正采用锁内复制配置容器，保留数字类型、nil 和无效配置，不用 JSON 往返隐式改值。

标准入口的原生解析资格仍在历史恢复前读取，路由目录在准入/护栏后捕获；Bypass 创建在公开别名解析前捕获。候选过滤、策略目录校验和标准入口缓存范围使用这份目录。预览也复用一次目录；按 ID 查询仅复制命中模型。命名凭据仍按候选读取，身份/预算仍按重试与回退边界重查。已经开始的请求正常完成，下一请求看到停用或更新；不把目录快照当作授权快照。

边界：模板、模型默认项、全局回退和模型目录不构成数据库与内存间的单一事务。价格计划仍由既有预算准备建立，普通结算回退仍可按当前目录读取；任务轮询保留当前查找规则。此处没有修改这些业务语义。跨更新价格、连接与多存储组合仍待补验收，不宣称整个配置视图工作包已完成。

| 实际运行 | 结果与范围 |
| --- | --- |
| 相关 Go 单元与 race | 2622 叶子通过，0 失败；4 个既有跳过如下 |
| 最终目录/查找/执行/预览定向单元 | 8 叶子通过，0 失败/跳过；复制函数和 Models/FindDeployment 定向覆盖 100% |
| 真实 HTTP/PostgreSQL | 54 叶子通过，0 失败/跳过：热更新、凭据轮换、撤销/预算阻断重试、模板/回退/预览、协议及历史 |
| 双语浏览器 | 2 通过，0 失败/跳过/重试；页面停用/启用、在途对话、后续拒绝、型号/凭据更新、日志和费用；清理退出 0 |
| 工作流测试 | 83 通过，0 失败/跳过 |
| Go vet、场景 ESLint | 通过；清单测试文件另有 11 个既有未修改行警告，无错误 |
| 网站单元与构建 | 52 项通过，0 失败/跳过；构建 443 文档页与 2 首页；网站浏览器检查通过 |

四个跳过是 `TestQiniuSeedanceLive`、`TestQiniuFalLive`、`TestKodoLiveArchive`（未配置外部凭据）和 `TestBrowserEnterpriseFixture`（常规 Go 测试不启动浏览器专用导出夹具，E2E 运行器单独启动）。不计入通过，也不据本地夹具宣称真实 SSO、供应商或生产代理可用。

首次失败记录：旧实现单元及旧 getter 的真实回归按预期失败；去掉预览重复读时曾遗漏一个未使用 import，出现一次编译失败，修复后重跑。首轮 PR 网站检查另有一条源码断言仍匹配旧 `cfg.ModelList` 表达式；更新为独立目录读取，保留入口、模型能力与账号三项条件，重新运行完整网站测试。未延长超时、跳过失败项或降低 70% 门禁。#242 的双语场景另发现缺少全量清单登记，拆为两个语言入口并补登记；本批同样独立登记，定向结果不能替代全量验收。

实际命令：

```bash
XHUB_REGRESSION_STRICT=1 go test -race ./internal/config ./internal/dataplane/... ./internal/gateway/... ./internal/router/... -count=1 -json
go test -race ./internal/config ./internal/dataplane/execution ./internal/gateway -run 'Test(CloneModels|ModelsSnapshot.*|FindDeploymentSnapshot|ExecutionUsesValidatedModelView|Preview.*)' -count=1 -coverprofile=.verification/phase5/P2/focused-coverage.out -json
XHUB_REGRESSION_COVERPROFILE="$PWD/.verification/phase5/P2/regression-accepted.out" bash scripts/regression.sh '^Test(ModelView.*|ModelContract.*|ModelFallback.*|RouteTemplate.*|RoutePreviewMatchesInference|NativeResponses.*|NativeMessagesAgentHistory|ChatAgentToolHistory|ChangingABudgetTakesEffectImmediately)$'
E2E_RUN_DIR="$PWD/.e2e/phase5-p2-registered" bash scripts/e2e.sh model-view-hot-update-
node --test scripts/test_quality_workflow.cjs scripts/test_release_workflow.cjs
node --test frontend/e2e/checklist-reporter.test.cjs
npm test --prefix website
npm run build --prefix website
npm run test:e2e --prefix website
go vet ./internal/config ./internal/dataplane/... ./internal/gateway/...
go test ./internal/config -run '^$' -bench BenchmarkCloneModels -benchmem -count=3
```

报告：`.verification/phase5/P2/` 保存初次失败、最终单元事件、回归覆盖率、工作流和微基准；`.e2e/runs/regression-xHgU1Dlp/` 保存 54 项回归及清理校验；`.e2e/phase5-p2-registered/` 保存最终双语报告和清理状态。100 条嵌套模型复制的三次微基准为 119570–139957 ns/op、约 142656 B/op、2701 allocs/op，仅描述复制开销，不代表完整请求性能提升。

无 schema 迁移。回退代码可恢复原目录读取方式，不回滚配置、账单或历史；未合入、未部署。
