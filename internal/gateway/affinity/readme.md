> Production uses PostgreSQL (`conversation_state` in `kv`) for ownership, responses, history and Messages references without a cache TTL. Only routing preferences use the one-hour cache. Cache-only details below describe `New` test instances; production uses `NewDurable`.

# affinity: Session preferences, response continuation and task ownership

[中文](readme_cn.md) · [Parent architecture](../readme.md)

## Purpose and integration boundary

This package keeps the cross-request facts needed to prefer a previous deployment, resume a response on its original target, and poll an asynchronous task using its creation-time deployment and billing context.

The public API is for other Go packages within this repository. Go's internal import rule prevents external repositories from importing it directly. HTTP clients use gateway session signals instead of constructing State.

| State | Stored facts | Subsequent behavior | TTL |
| --- | --- | --- | --- |
| Session preference | Stable deployment ID, model, pool, fallback path, revision | Revalidate each request; reselect when invalid; update after successful fallback | 1 hour |
| Response ownership | Creation-time target and session ID | Continuation must use the original deployment | 1 hour |
| responses history | Combined inputs and successful outputs, including tool fields | Scoped with ownership; missing history rejects continuation | 1 hour |
| Official task | Runtime deployment ID and creation-time billing facts | Poll original deployment; settle using creation facts | 7 days |

Affinity does not authenticate callers, schedule candidates, validate deployment eligibility, or calculate charges. A cached target never grants permission.

Full encrypted Responses history also requires caller-scoped session deployment ownership and a required target. Shared-cache failures, disabled origins or changed weights must not move encrypted history to another account. Unknown ownership requires a new conversation.

## Exported API

| API | When to call | Inputs | Result and boundary |
| --- | --- | --- | --- |
| New(redis) | Once during gateway composition; reuse across requests | Optional shared cache; nil means memory only | Independent State; no connection or health probe |
| PlanRoute(request, alias, body, principal) | After authentication and parsing, before deployment selection | Original public alias, request, trusted caller | Request-private plan; check Err first |
| CommitRoute(plan, deploymentID, responseID) | After actual upstream success | Actual Target, stable deployment ID, combined history, optional response ID | Compare-update preference and publish response facts; no error return |
| PinOfficial(taskScope, runtimeDeploymentID) | After asynchronous creation succeeds | Caller-scoped task identifier and runtime deployment ID | Seven-day cache; does not isolate tenants itself |
| OfficialDeployment(taskScope) | Before polling, querying or cancelling | Identical scope used at creation | Original runtime deployment ID, or empty when unavailable |
| PinOfficialContext(scope, facts) | At creation, before publishing task deployment | Creation-time TaskContext | Seven-day snapshot; does not price the task |
| OfficialContext(scope) | During polling settlement and usage recording | Identical scope | Snapshot; caller checks Known and other required fields |

### Normal request lifecycle

1. Create and retain State at startup. Creating New(nil) per request loses in-memory continuity. Production gateway composition injects shared storage.
2. Obtain Principal from authentication, parse the body and call PlanRoute. Reject Err before contacting any upstream; an error plan may contain partial fields.
3. Validate candidates against the current directory, template, capabilities, weights, permissions and budgets. A normal Target/Pinned is only a preference and can be replaced when invalid.
4. Before sending a normal request, invoke Claim when non-nil. An empty preference is claimed atomically; the returned winner must also pass current eligibility checks. Production uses [SelectAffinity](../../dataplane/routing/affinity.go).
5. Execute through the normal retry/fallback chain. After success, set plan.Target to the actual model, pool and fallback path. For responses, combine the current input and successful output into plan.History.
6. Call CommitRoute after success. Do not commit failed attempts or a response-cache-only hit. CommitRoute invokes Save; integrations normally do not invoke Save separately.

### Parent response continuation

previous_response_id looks up ownership scoped by the original alias, authenticated caller, response ID and endpoint. The plan inherits the session with SessionSource=parent, sets Required=true and fixes Target/Pinned to the creation target. An explicit session ID must match the parent session.

Required requests skip Claim and revalidate the original target. They cannot send the cursor to a different deployment. Missing ownership, missing responses history or an invalid original target rejects continuation; the client must submit full history.

## Executable demo

The following [example_test.go](example_test.go) exercises the exported API from another package: preference reuse, responses continuation and official task state. It uses memory, a simulated authenticated caller and one assumed eligible candidate. It performs no real upstream execution or authentication; production must supply those steps and current eligibility checks.

Run from the repository root:

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

The first output line denotes an explicit session with an optional preference. The second denotes parent inheritance with a required deployment and two restored history turns. The final line confirms task deployment and creation context recovery. Code comments follow this repository's Chinese annotation convention; example prompts remain English.

## Session signals and isolation

First validate every standard X-Session-Id header value and body session_id; all supplied values must be identical. If previous_response_id is present, restore its ownership and inherit its session. Otherwise use the explicit ID, then the prompt_cache_key fingerprint, then a fingerprint of opening instructions and the first user content, and finally a random UUID.

| SessionSource | Meaning |
| --- | --- |
| explicit | Standard header or body ID |
| parent | Parent response session, even when an explicit ID matches |
| cache_key | SHA256 fingerprint of prompt_cache_key |
| prompt | SHA256 fingerprint of opening instructions and first user content |
| generated | Random UUID when no reliable signal exists |

Standard signals must be nonempty strings, at most 256 bytes, without surrounding whitespace or control characters. Invalid present signals do not silently fall through. Lower-priority prompt_cache_key is not validated when an explicit or inherited session already exists. Legacy session-id headers and metadata do not identify sessions. Inference never changes the user's original input and does not authorize history access.

Caller scope prefers key:<hash>, then user:<id>. Without a trusted caller scope, a session can still be identified, but no preference callbacks or response commits are enabled. Preferences also isolate the original alias and endpoint; fallback does not change the original scope.

## Storage, concurrency and failure semantics

State's zero value and New(nil) both support memory-only storage. Restart, expiry and eviction can remove records. All local record types share a limit of 8192 entries; writes evict the earliest expiry, not the least recently accessed item. Do not copy a used State. Invoke a plan's Claim/Save sequentially within its request; its closures share mutable request state.

| Path | Cache miss | Shared storage failure |
| --- | --- | --- |
| Normal preference read | Reschedule; do not fill a shared miss with stale local state | Log and allow local fallback |
| First Claim | Atomic empty-value claim; others receive the winner | Local claim; convergence limited to this process |
| Successful Save | Compare the complete request snapshot before writing | Log; preserve the successful upstream result |
| Parent ownership/history read | Reject continuation | Reject; no local substitute |
| Response history/ownership publish | Unconditional write with TTL | History failure stops ownership publication; no local substitute |
| Official task read/write | Permissive API; reads may use local copies | Limited error visibility; may use local copies |

Claim/Save compare the complete serialized value. Random Revision distinguishes repeated deployment values and prevents ABA overwrites by late requests. Cache comparison is atomic, but upstream execution is not serialized. Local degradation does not provide cross-instance agreement. New requests read shared state again after Redis recovery; stale local preferences are not automatically copied back.

Redis implementations must distinguish errors from misses in ReadAffinity; atomically compare, write and renew TTL in CompareAffinity; and return publication errors in WriteAffinity. Comparison mismatch returns the current value without renewal. GetString/SetString are the permissive task API. See [internal/live/affinity.go](../../live/affinity.go). Strict session operations use independent one-second timeouts; permissive task operations do not set that timeout here.

CommitRoute updates the preference, writes nonempty responses history, then publishes ownership. History and ownership are separate writes, not a transaction; ownership write errors are discarded. Empty history can still publish ownership, but subsequent continuation rejects it, so the executor must prepare full history. This temporary context is not an upstream provider's long-term store. Task methods also permit overwrites: scope isolation, creation snapshot immutability and authorization remain caller responsibilities.

## Source map and verification

| File | Responsibility |
| --- | --- |
| [affinity.go](affinity.go) | State, shared interface, scoped keys, task API |
| [session.go](session.go) | Signal validation, content structure and fingerprint boundaries |
| [route.go](route.go) | PlanRoute/CommitRoute lifecycle and callbacks |
| [cache.go](cache.go) | Strict reads, comparison, TTL and local eviction |
| [example_test.go](example_test.go) | Executable caller integration |

Real consumers: [adapted execution](../../dataplane/execution/serve.go), [official execution](../../dataplane/execution/official.go), and [SelectAffinity](../../dataplane/routing/affinity.go).

```sh
go test ./internal/gateway/affinity ./internal/dataplane/routing -count=1
go test -race ./internal/gateway/affinity -count=1
```

Unit tests cover signals, fingerprints, isolation, history expiry, concurrent claims, late commits and task snapshots; the demo verifies exported API usage. Real HTTP/persistence regression lives in [session_affinity_test.go](../../../cmd/regression/session_affinity_test.go); browser-to-gateway-to-local-upstream flows live in frontend/e2e. Memory examples do not verify Redis network failures, multiple instances or real suppliers, and do not replace backend regression or browser E2E.
