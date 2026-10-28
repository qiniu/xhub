# Unsupported legacy runtime operations (Phase 4 C3/runtime-ops)

These 12 entries only performed catalog KV operations. No scheduler, warm-up executor, event batch processor or Vantage exporter consumes them. Previously `POST /vantage/export` could save configuration and return `status: ok` with `exported_count: 0`; that did not prove an export occurred. The endpoints now fail explicitly.

## Exact scope

| Method | Path |
| --- | --- |
| `DELETE` | `/schedule/anthropic_beta_headers_reload` |
| `DELETE` | `/vantage/delete` |
| `GET` | `/schedule/anthropic_beta_headers_reload/status` |
| `GET` | `/vantage/settings` |
| `POST` | `/api/event_logging/batch` |
| `POST` | `/lazy/warm/{name}` |
| `POST` | `/reload/anthropic_beta_headers` |
| `POST` | `/schedule/anthropic_beta_headers_reload` |
| `POST` | `/vantage/dry-run` |
| `POST` | `/vantage/export` |
| `POST` | `/vantage/init` |
| `PUT` | `/vantage/settings` |

Matching uses the exact method/path and permits one trailing slash. `{name}` is a non-empty single segment. Extra subpaths and unrelated configuration/model routes are not captured by broad prefixes.

## Compatibility

Anonymous requests remain 401 and ordinary users remain 403. Authorized administrators with valid JSON receive 501 with `error.type=unsupported_operation` and `Cache-Control: no-store`. Invalid JSON and top-level JSON-RPC remain 400 first. No input credentials or stored private configuration are echoed; no same-name KV mutation or upstream request occurs. Existing records are not deleted or migrated.

Clients relying on the previous 200 must handle the explicit error. No replacement runtime executor is introduced. Existing configuration services, supplier credential rotation, model traffic, permissions and budget algorithms remain unchanged.

## Verification

```sh
go test -race ./internal/gateway/family ./internal/gateway -count=1
XHUB_REGRESSION_COVERPROFILE="$PWD/.verification/phase4-c3/runtime-regression.out"   bash scripts/regression.sh '^Test(RuntimeOperationBoundary|PolicyConfigService|SupplierServiceRuntimeRotation|TeamlessSessionBudgetHTTP)'
E2E_PORTABLE_CONSOLE=1 bash scripts/e2e.sh scenarios/regression/runtime-operation-en.test.ts scenarios/regression/runtime-operation-zh-CN.test.ts
```

The real HTTP/PostgreSQL regression fails for all 12 aliases before the fix, then checks each identity, JSON/RPC errors, the full KV snapshot and zero upstream calls after the fix. Adjacent tests exercise configuration persistence/storage failures, credential rotation and teamless budgets. Private schemas are removed; failed cleanup cannot count as success.

Bilingual browser flow: create policy configuration on the real page, reload/read it, call all 12 aliases and verify explicit errors plus unchanged configuration/upstream counters, reload again, send an English prompt through the playground to the local HTTP upstream and see e2e-ok, then delete the policy and schema. The existing policy form retains its Chinese labels; translated playground labels are used for each locale. No product UI copy changes are included.

Local deterministic upstream fixtures do not certify a paid vendor, production scheduler or Vantage service. Unsupported integration tests prove refusal, not external availability. Coverage collection/upload and both release E2E entries are mandatory; the 70% gate is unchanged.

## Remaining C3 scope

The fixed inventory still contains 15 `settings` entries: four get/update pairs, the SSO UI settings alias, three branding reads, settings and two config reads. Some have frontend callers and must not be indiscriminately rejected. The get-settings wrapper currently returns empty values and update/read namespaces differ; consumer behavior needs separate evidence. A successful save/read alone cannot establish default budgets, user defaults or UI flag effects. This change leaves those entries and main audit classifications unchanged.
