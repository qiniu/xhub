# Redis hot state, rate admission, and durable-usage handoff

[简体中文](readme_cn.md)

## Integration from other modules

Integration here means other Go packages in this repository. Go internal import rules restrict imports from outside the repository; HTTP clients use mounted gateway endpoints.

Open and share a Redis Client. Strict affinity methods retain errors; permissive hot-state methods follow their documented fallback contracts. Usage consumers must commit IAM before Ack. Close the client at shutdown.

| Entry point or resource | Usage and result |
| --- | --- |
| Open(url) / Close() | Open and close the Redis client |
| ReadAffinity(ctx,key) | Return empty,nil on miss and preserve failures |
| CompareAffinity / WriteAffinity | Atomically compare or strictly publish with TTL |

### Integration demo

This is a Go integration helper for a host. It compiles as a separate package inside the repository; callers must supply the real dependencies described below before execution.

```go
package demo

import (
	"context"
	"github.com/sunqirui1987/xhub/internal/live"
)

// ReadDemoAffinity 演示严格共享读取；传入 Redis URL 与独立演示键，返回载荷及存储错误。
// 不写任何键，调用完成关闭连接；运行需要真实 Redis。
func ReadDemoAffinity(ctx context.Context, redisURL, key string) (string, error) {
	client, err := live.Open(redisURL)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return client.ReadAffinity(ctx, key)
}
```

This helper compiles. An absent demo key returns empty,nil on real Redis; network failures return errors.

### Errors, state, and side effects

CompareAffinity must be atomic and must not renew TTL on comparison failure. Hot state is not the final ledger; local fallback cannot guarantee multi-instance consistency.

## Overview

`internal/live` owns state that must be shared immediately between gateway processes but does not belong in the durable IAM ledger: deployment cooldowns, recent latency and minute usage, hierarchical RPM/TPM counters, unflushed spend, a reliable usage queue, and short-lived strings. PostgreSQL remains the source of truth for identities and settled usage. This package closes the interval between admission and the IAM transaction committing.

The package registers no HTTP handlers. Request execution builds an IAM rate plan, calls `AdmitRatePlan`, records deployment telemetry, and publishes completed usage with `EnqueueSpend`. The usage flusher peeks logs, commits them through `internal/iam`, then calls `AckFlushed`.

## External interfaces

### Connection, telemetry, and counters

- `Open(url) (*Client, error)` parses the Redis URL, connects, and pings; parse, network, authentication, and ping failures close the client and propagate. `Close()` is safe on nil.
- `RecordFailure(id, allowed, cooldown)` counts failures and starts cooldown at the threshold; `allowed < 1` and nil clients are no-ops. `Cooled(ids)` returns cooled IDs and degrades to an empty map on read failure. `AddLatency` retains 20 samples for one hour; `Latencies` averages valid samples.
- `AddUsage`/`Usages` maintain per-deployment token totals in the UTC minute bucket. `HitRPM` and `HitTPM` increment one current-minute counter and return its new value; they do not provide combined admission.

### Rate admission

- `RateScope` carries a scope kind, ID, and optional RPM/TPM limits. `RateNode` adds a parent index. `RatePlan` carries the complete allocation tree and the current leaf-to-root `Path`.
- `CheckRatePlan(plan, rpmUsed, tpmUsed, est) string` is the side-effect-free reference algorithm. Empty means admitted; rejection is `<kind> rpm_limit` or `<kind> tpm_limit`. The caller must supply a valid acyclic tree, valid path/parent indexes, and usage slices aligned with `Nodes`; the function indexes them directly. Negative estimates become zero, and an explicit zero limit rejects.
- `AdmitRates(scopes, est, now)` converts every scope into an independent root (`Parent == -1`) and uses input order as `Path`; it does not build a parent chain. `AdmitRatePlan(plan, est, now)` runs one Redis Lua script against UTC minute keys. It checks all RPM constraints before TPM and increments every path counter only after all checks pass. Redis/script errors propagate; gateway callers treat them as dependency failures instead of admitting an uncounted request.

### Spend handoff and strings

- `SpendRef(kind, id)` forms the hot-spend identity; `HotSpend(id)` reads the unacknowledged amount without consuming it. Strict budget admission uses the durable IAM reservation transaction, not a telemetry read that degrades to zero.
- `EnqueueSpend(SpendLog)` is the only queue writer and requires a request ID and valid scope deltas. Lua atomically appends the log and increments all hot-spend scopes. Request IDs are permanent idempotency keys: retries are successful no-ops and the first payload wins. Free and unpriced events use the same queue without monetary increments.
- `PeekLogs(n)` returns decoded and raw entries without deletion and stops at the first corrupt queue item. After PostgreSQL commits, `AckFlushed(deltas, n, head)` verifies the observed head, subtracts hot spend, and trims the prefix atomically; a changed head is a no-op. There is no destructive drain or independent aggregate-spend handoff.
- `GetString(ctx, key)` returns `(value, true)` only for an existing readable key. `SetString(ctx, key, value, ttl)` stores a TTL value and silently skips nil clients.

Callers include inference admission/routing, the usage flusher, IAM quota checks, and deployment telemetry. Their HTTP routes live in gateway packages.

## Core implementation

### Hierarchical reservation

A tree is required because fixed child allocations reserve capacity at shared ancestors. For each node, the algorithm computes unused fixed allocations below it: a fixed child contributes `max(limit-used, 0)` and a shared child passes through fixed descendant reservations. A path node deducts sibling reservations, then `credit` adds back the current path's own reservation so a request is not blocked by its allocation. RPM is checked first, then TPM with the same topology. The function assumes a valid acyclic tree, valid indexes, and usage slices with one entry per node; malformed input can panic or recurse indefinitely. IAM constructs valid plans.

`AdmitRatePlan` serializes nodes, parents, limits, estimate, and path into Lua arguments and creates two keys per node. Lua reads every counter, performs both dimensions in full, then increments all path keys. Keys expire after 120 seconds. The single script prevents races and partial ancestor updates across replicas; `CheckRatePlan` mirrors it for deterministic tests.

### Reliable usage handoff

`EnqueueSpend` validates and serializes the event before a script updates the log queue, request-dedup hash, and floating-point scope counters together. No separate pending-subject index is needed: each event carries its scope identities. IAM adds these hot values during `QuotaPath`. The flusher follows peek, database commit, acknowledge: failed IAM transactions leave Redis intact, while successful commits receive one head-checked acknowledgement. IAM also deduplicates `request_id`, making replay safe after a crash between commit and acknowledgement.

Telemetry reads deliberately degrade when Redis is absent, and several optional methods accept nil clients. Atomic admission and reliable queued accounting require a real Redis client. Enqueue success does not wait for disk fsync or replica quorum, so Redis persistence and retention remain deployment boundaries.

### Dependencies and verification

The package uses Redis and `internal/util`; `internal/iam` is the durable consumer. `rate_plan_test.go` covers valid allocation trees, shared ancestors, zero limits, and estimates; callers remain responsible for satisfying the plan preconditions. `rates_test.go` compares Lua admission and atomic increments with the pure algorithm. `redis_test.go` covers concurrent deduplication, invalid queue input, corrupt heads, acknowledgement, and key expiry. Redis-backed cases skip when no test Redis is available.

## Source responsibilities and entry points

### `rate_plan.go`

Defines `RateNode`, `RatePlan`, the pure `CheckRatePlan` entry point, and private helpers for child indexes, fixed-descendant reservation, and path credit. The implementation directly indexes nodes and usage slices, so its result contract and valid-plan preconditions must match Lua exactly.

### `rates.go`

Defines `RateScope`, the `rateAdmission` Lua program, `AdmitRates`, and `AdmitRatePlan`. It serializes the caller-supplied plan without index validation, builds UTC minute keys and arguments, applies a two-second Redis timeout, maps the returned node/dimension index to a rejection string, and propagates Redis errors.

### `redis.go`

Defines `Client`, `SpendLog`, key construction, connection lifecycle, cooldown/latency/minute telemetry, unacknowledged spend reads, reliable queue operations, and TTL strings. Its scripts provide enqueue idempotency and atomic acknowledgement; private parsers may omit bad telemetry values but never skip past a corrupt reliable-queue entry.

Organization admission reads `HotSpendSince(id, periodStart)` so unacknowledged previous-period charges do not occupy a renewed budget. Enqueue and acknowledgement atomically maintain a per-organization timestamp index. Queues left by an older deployment without that index use their original start times until drained; missing timestamps are counted conservatively. Other budget levels retain cumulative spend.
