> 生产通过 `NewDurable` 将归属、响应、历史及 Messages 引用保存到 PostgreSQL 的 `kv` 表（kind 为 `conversation_state`），不自动过期。只有路由偏好使用一小时缓存；下文纯缓存描述仅适用于 `New` 创建的测试状态。

# affinity：会话偏好、响应续接与任务归属

[English](readme.md) · [上层架构](../readme_cn.md)

## 这个包解决什么问题

网关一次请求选中了某个部署，下一轮如何优先复用它？客户端只传父响应 ID 时，如何找回原会话、原部署及历史？视频等异步任务完成后，如何按创建时的部署和计费事实继续查询？这些跨请求状态由本包保存。

“对外使用”指仓库内其他 Go 包通过导出 API 调用本模块。由于位于 Go 的 `internal` 目录，仓库外程序不能直接导入；客户端通过网关 HTTP 协议使用会话信号，而不是操作 State。

| 状态 | 写入内容 | 后续行为 | TTL |
| --- | --- | --- | --- |
| 动态会话偏好 | 稳定部署 ID、实际模型池、型号、回退路径、随机版本 | 每轮重新验证；失效可重选，成功回退可更新 | 1 小时 |
| 响应归属 | 响应创建时的目标和会话 ID | 续接必须回原部署，动态偏好不能替换 | 1 小时 |
| responses 历史 | 合并后的输入与成功输出，包含工具字段 | 与响应归属同范围隔离，缺失即拒绝续接 | 1 小时 |
| 官方任务 | 创建时运行部署 ID、创建时计费快照 | 轮询找回原部署，结算使用创建事实 | 7 天 |

完整加密 Responses 历史还必须找到本调用方的会话部署归属，并升级为固定目标；共享缓存故障、原部署禁用或权重变化不能把密文切换给其他账户。没有归属时拒绝并要求新建会话。

本包不做鉴权、候选调度、模型能力检查或费用计算。缓存目标不能作为权限凭据；部署和回退路径必须由当前执行链验证。

## 导出 API 怎么使用

| API | 调用时机 | 调用方提供什么 | 结果与边界 |
| --- | --- | --- | --- |
| New(redis) | 网关组装时一次创建、跨请求复用 | 可选 Redis 实现，nil 使用内存 | 返回独立 State，不连接或探测存储 |
| PlanRoute(r, alias, body, principal) | 已鉴权、已解析正文，选部署之前 | 原始公开别名、请求及可信身份 | 返回私有计划；必须先检查 Err |
| CommitRoute(plan, deploymentID, responseID) | 上游实际成功后 | 实际 Target、稳定部署 ID、完整历史及可选响应 ID | 比较更新偏好，按需发布历史/归属；不返回存储错误 |
| PinOfficial(taskScope, runtimeDeploymentID) | 异步任务创建成功后 | 已隔离任务作用域、运行部署 ID | 七天缓存，不自行做租户隔离 |
| OfficialDeployment(taskScope) | 查询、轮询或取消任务之前 | 同一任务作用域 | 返回原运行部署 ID，缺失返回空串 |
| PinOfficialContext(scope, facts) | 创建成功时，保存部署归属之前 | 创建时 TaskContext 快照 | 七天缓存，不计算费用 |
| OfficialContext(scope) | 轮询结算及记录用量时 | 同一任务作用域 | 返回快照，需检查 Known 等字段 |

### 普通请求的调用顺序

1. 启动时创建 State；生产网关在组装阶段注入共享存储。同一实例重复使用，不能每次请求 New(nil)。
2. 从鉴权链获取 Principal，解析正文，调用 PlanRoute。Err 非 nil 就在上游调用前拒绝；不要继续使用部分计划。
3. 按当前目录、模板、能力、权重、权限及预算检查候选。普通 Target/Pinned 仅是偏好，失效时可重新调度。
4. 普通请求在发送前调用 Claim（若非 nil）。它在首次无偏好时原子申请候选，返回实际赢家；对赢家也须重新验证。生产链使用 [SelectAffinity](../../dataplane/routing/affinity.go)。
5. 执行上游；失败重试或回退沿现有执行链处理。成功后将实际型号、池及回退路径补入 plan.Target；responses 还要合并本轮输入、成功输出到 plan.History。
6. 成功后调用 CommitRoute。仅命中响应缓存或执行失败时不提交；Save 由 CommitRoute 调用，集成方通常无需自行调用 Save。

### 父响应续接的不同之处

previous_response_id 对应记录按原始 alias、鉴权调用方、响应 ID、入口隔离。计划继承父会话，SessionSource 为 parent；同时 Required=true，Target/Pinned 固定为创建时目标。明确 session_id 存在时必须与父会话一致。

Required 请求跳过 Claim，重新验证原目标后只能发往该目标；目标失效、归属不可用或 responses 历史缺失时拒绝，客户端需要提交完整历史。固定归属不是“尽量粘住”的普通偏好。

## 完整 demo：从包外调用到状态恢复

下面是可执行的 [example_test.go](example_test.go)，覆盖普通会话复用、responses 续接和异步任务往返。它使用本机缓存、模拟鉴权结果与一个已验证候选，演示状态 API 的接法，不发送真实上游请求。生产链的鉴权、资格验证、上游执行及计费不能用示例夹具替代。

在仓库根目录执行：

```sh
go test ./internal/gateway/affinity -run '^ExampleState$' -v -count=1
```

```go
package affinity_test

import (
	"fmt"
	"net/http/httptest"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
)

// ExampleState 演示包外调用方如何使用会话偏好、父响应续接及官方任务归属。
// 无参数和返回值；前置独立内存 State、模拟鉴权身份及已验证候选，通过固定输出验证状态往返。
// 不发送真实上游请求、不做真实鉴权，生产执行器须负责这些步骤；测试结束释放内存，无外部数据。
func ExampleState() {
	// State 在网关启动时创建并跨请求复用；每个请求创建 State 会丢失内存偏好。
	state := affinity.New(nil)
	principal := &auth.Principal{UserID: "demo-user"} // 夹具；生产中必须取自鉴权结果。
	alias := "demo-model"
	body := map[string]any{"session_id": "demo-session", "input": "Hello"}
	request := httptest.NewRequest("POST", "/v1/responses", nil)

	plan := state.PlanRoute(request, alias, body, principal)
	if plan.Err != nil {
		panic(plan.Err) // 演示失败即终止；HTTP 执行器应在调用上游前返回对应错误。
	}
	// 仅演示状态 API，假定目录中已验证且符合当前权限、能力及权重的候选只有一个。
	// 生产链通过 SelectAffinity 验证 plan.Target 和 Claim 的赢家，不可直接盲用缓存目标。
	target := dpcontract.RouteTarget{DeploymentID: "demo-deployment", Model: alias, Pool: alias}
	if plan.Claim != nil {
		winner := plan.Claim(target)
		if winner.DeploymentID != target.DeploymentID || winner.Model != alias || winner.Pool != alias || len(winner.Path) != 0 {
			panic("unexpected target outside the demo candidate set")
		}
		target = winner
	}
	// 此处假定上游执行成功；失败尝试不能调用 CommitRoute。
	// 执行器提供实际成功目标及合并输入、输出后的完整历史，再发布响应 ID。
	plan.Target = target
	plan.History = []llmdialogue.Turn{{Role: "user", Text: "Hello"}, {Role: "assistant", Text: "Hi"}}
	state.CommitRoute(plan, target.DeploymentID, "demo-response")

	next := state.PlanRoute(request, alias, body, principal)
	if next.Err != nil {
		panic(next.Err)
	}
	fmt.Println(next.SessionSource, next.Pinned, next.Required)

	// 续接只提供增量输入，继承父会话、固定目标及历史；固定目标失效时应拒绝，不能改投其他部署。
	continuation := state.PlanRoute(request, alias, map[string]any{
		"previous_response_id": "demo-response", "input": "Continue",
	}, principal)
	if continuation.Err != nil {
		panic(continuation.Err)
	}
	fmt.Println(continuation.SessionSource, continuation.Pinned, continuation.Required, len(continuation.History))

	// 任务作用域由调用方隔离，演示值代表用户、协议与任务 ID 的组合，生产使用 officialTaskScope。
	// 任务 API 使用运行部署 ID（与上面的稳定部署 ID 属于不同用途），创建事实先于部署归属保存。
	scope := "demo-user/openai-videos/demo-task"
	state.PinOfficialContext(scope, providercontract.TaskContext{
		Known: true, StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Model: "demo-video-model",
	})
	state.PinOfficial(scope, "demo-provider|demo-video-model")
	fmt.Println(state.OfficialDeployment(scope), state.OfficialContext(scope).Known)

	// Output:
	// explicit demo-deployment false
	// parent demo-deployment true 2
	// demo-provider|demo-video-model true
}
```

预期输出：explicit 表示本轮明确会话，false 表示普通偏好；parent 表示继承父响应，true 表示固定目标，2 是恢复的历史轮数。最后一行表明官方任务找回运行部署和有效的创建快照。

## 会话识别优先级

先校验所有标准 X-Session-Id 头及正文 session_id，重复来源必须完全相同。随后若有 previous_response_id，校验归属并继承父会话；没有父响应时使用明确 ID。仍无 ID 时依次使用 prompt_cache_key 指纹、开头指令与首条用户内容的指纹，最后随机 UUID。

| SessionSource | 含义 |
| --- | --- |
| explicit | 标准头或正文明确 ID |
| parent | 父响应的原会话；即使明确 ID 同值也记 parent |
| cache_key | prompt_cache_key 的 SHA256 指纹 |
| prompt | 开头指令及首条用户内容的 SHA256 指纹 |
| generated | 无可靠信号时生成随机 UUID |

标准信号必须是非空字符串，最多 256 字节，无首尾空白或控制字符。存在但非法的信号不能悄悄改用低优先级来源；已有明确/继承 ID 时不再校验低优先级 prompt_cache_key。旧 session-id 头和 metadata 不参与身份解析。推断指纹不是权限标识，用户原始输入不会被修改。

调用方范围优先 key:<hash>，否则 user:<id>；无可信范围仍可生成会话标识，但不建立偏好或提交归属。会话偏好还按原始 alias 和入口隔离，跨模型回退不改变原隔离范围。

## 存储、并发和降级契约

State 零值和 New(nil) 都可使用内存；进程重启、过期或容量淘汰会丢失状态。所有本机记录共用 8192 项容量，写入前淘汰最早到期项，不是 LRU。不要复制已使用的 State；同一计划的 Claim/Save 按请求顺序调用，不并发调用同一闭包。

| 路径 | 未命中 | 共享存储故障 |
| --- | --- | --- |
| 普通偏好读取 | 重新调度，不读本机旧值补共享未命中 | 记录日志，允许本机降级 |
| 首次 Claim | 空值原子申请，其他请求取赢家 | 改用本机申请，仅当前进程收敛 |
| 成功 Save | 比较本轮完整旧值再写 | 记录日志，不撤销已成功上游结果 |
| 父响应归属/历史读取 | 拒绝续接 | 拒绝续接，不读本机替代副本 |
| 响应历史/归属发布 | 无条件写入，设置 TTL | 历史失败不再发布归属，不发布本机替代归属 |
| 官方任务读写 | 宽松接口，读取可用本机副本 | 接口不保留完整错误信息，可能使用本机副本 |

Claim/Save 比较完整序列化载荷，随机 Revision 区分同部署的不同版本，防止 A→B→A 后旧请求覆盖新值。不同请求缓存比较是原子的，但不持锁等待上游，不能保证上游请求串行执行。共享故障降级不保证跨实例一致；后续新请求在 Redis 恢复后重读共享值，不自动回灌本机旧偏好。

Redis 接口实现必须遵守：ReadAffinity 区分未命中与错误；CompareAffinity 原子比较、写入及续期，不匹配返回当前值且不续期；WriteAffinity 返回真实发布错误。GetString/SetString 是官方任务的宽松接口。仓库实现见 [internal/live/affinity.go](../../live/affinity.go)。严格会话调用使用独立的一秒超时，任务宽松调用在本包不设置该超时。

CommitRoute 先更新偏好，再写非空 responses 历史，最后写归属。历史与归属不是跨键事务，归属发布错误不向外返回；空 History 仍可能发布归属，但后续续接会拒绝，因此执行器必须准备完整历史。这里只保存短期上下文，不等价于原厂长期 store。官方任务方法也不是写一次的强约束，作用域隔离、创建后不改快照和授权由调用方保证。

## 文件导航与验证

| 文件 | 阅读目的 |
| --- | --- |
| [affinity.go](affinity.go) | State、共享接口、隔离键及任务 API |
| [session.go](session.go) | 信号校验、内容结构和指纹边界 |
| [route.go](route.go) | PlanRoute/CommitRoute 生命周期及回调 |
| [cache.go](cache.go) | 严格读取、原子比较、TTL 及本机淘汰 |
| [example_test.go](example_test.go) | 包外可执行调用示例 |

真实调用方见 [统一执行入口](../../dataplane/execution/serve.go)、[官方入口](../../dataplane/execution/official.go)；完整选路协调见 [SelectAffinity](../../dataplane/routing/affinity.go)。

```sh
go test ./internal/gateway/affinity ./internal/dataplane/routing -count=1
go test -race ./internal/gateway/affinity -count=1
```

单元测试验证信号、内容指纹、隔离、历史过期、并发申请、迟到提交和任务快照；demo 验证导出 API 的调用契约。真实 HTTP/持久化回归见 [session_affinity_test.go](../../../cmd/regression/session_affinity_test.go)，浏览器到网关及本地上游流程位于 frontend/e2e。纯内存 demo 不验证 Redis 网络故障、多实例或真实供应商，不能替代后台回归和浏览器 E2E。
