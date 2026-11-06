# Runtime boundaries

Choose guarantees by execution path, not by the presence of a field in a form.

## Example

~~~bash
go test ./internal/dataplane/... ./internal/live -count=1
~~~

## Contract

| Path | Guarantee / limit |
| --- | --- |
| Complete response cache | Non-streaming results; process-local LRU, no shared invalidation/TTL guarantee |
| Idempotency-Key | Authenticated non-streaming responses replay within the same process; PostgreSQL stores a digest for 24 hours and returns `409 idempotency_result_unavailable` across instances or restarts without retaining response bodies. Database failure or claim timeout returns `503 idempotency_unavailable`. |
| Stream | No successful cache/stickiness update after failure; cannot restart after output |
| Native tasks | Query using creation identity and registered task path |
| Task settlement | Successful measured result, deduplicated PostgreSQL event |
| Task recovery | Redis context lasts seven days; without Redis, process-local |
| Guardrails | Pre-call text on normal Chat / Responses, not every Bypass path |

There is no background task polling settlement or budget reservation. Preserve the existing task ID after an uncertain create result.

## Source and verification

[internal/dataplane/execution/official.go](../../../internal/dataplane/execution/official.go)

~~~bash
go test ./internal/dataplane/... -count=1
~~~

[Test prerequisites](testing.md) · [Next: pricing](pricing.md)
