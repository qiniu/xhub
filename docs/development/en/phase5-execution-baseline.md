# Phase 5 baseline and first-batch acceptance

Date: 2026-10-29. Proposal #241. Baseline main `09273d4d`. See the [fixed execution plan](phase5-execution-plan.md).

## Status

P0 has an initial ownership/test inventory with explicit gaps. P1 has local implementation and verification; CI, merge and deployment remain separate. P2 model-directory isolation has local evidence but pricing/connection update combinations and multi-store reads still need audit. P3 attempt results, P4 retries/resources, P5 history/logging/settlement and P6 combined acceptance remain pending. This is not phase-wide completion.

## Ownership audit

- Standard Serve preserves authentication, authorized history, budget/guardrail checks, templates/cache, attempts and persistence order. Native selection and Messages signed-history bridging remain conditional.
- Bypass dispatch uses the provider registry and keeps create/count/follow-up differences. Some public Google operations enter the standard path.
- ParseBypassBody keeps raw bytes, a field projection and multipart parts. The projection still has existing float64 JSON numbers; RawMessage rewriting preserves outbound precision.
- Payload changes the candidate model and removes gateway controls without mutating original bytes or file parts.
- PlanRoute owns request-local history/affinity planning. The Bypass projection consumers inspected here do not mutate the input fields.
- Models now copies the directory and nested containers under the model lock. Execution passes the same directory to RouteSettingsFor and candidate selection. Template/default/fallback reads remain separate storage operations, not a multi-store transaction.
- Credential attachment and per-attempt authentication/limits remain in their existing positions. Budget preparation can rewrite outbound payloads.
- ForwardOfficial measures the actual outbound body. Its stream-measurement parse remains intentionally separate from inbound parsing.
- Retry and cancellation policies stay entry-specific. Required parent responses and ambiguous asynchronous creation cannot be replayed like ordinary synchronous requests.
- RoutePlan/CommitRoute own dialogue affinity; PinOfficial(Context) owns scoped task creation facts. RecordSpend remains the settlement/logging boundary, with call, task and settlement IDs retaining different roles.

## P1 change

Export the existing internal carrier as `transport.BypassBody` and pass it from ServeBypass into its only production create caller. Remove the second inbound parse. No new package, global state, parsing algorithm, error precedence or HTTP/OpenAPI change.

The outbound stream-measurement parse remains because model and budget preparation may change the payload. No pricing, replay or upstream-error semantics were changed. There are now two production ParseBypassBody call sites: inbound validation and outbound stream measurement.

## Coverage and gaps

New transport tests cover JSON precision/extensions/null/empty/zero, repeated candidate rewrites, path models, multipart bytes/headers/order/repeated ordinary fields, invalid input and empty-form behavior. New real HTTP/database tests inspect actual upstream bodies, call counts, rejection/recovery, spend and logs.

Existing real regressions cover the five-protocol endpoint matrix, native Responses/Bypass boundaries, cross-protocol conversion, encrypted history ownership, Messages continuation, client cancellation, missing terminals, terminal-without-EOF and asynchronous settlement deduplication. Dataplane/provider unit suites cover routing, streams, retries and task settlement boundaries. Real hot-update concurrency and combined persistence failures remain P2–P6 work.

Browser acceptance covers both languages for body preservation/logging/error recovery, native Agent parallel tools/history and upstream error details. No browser route mocks are used. External suppliers, live media/search, SSO and long-lived production clients were not tested.

## Actual results

| Run | Result |
| --- | --- |
| Initial new unit baseline | 162 passed leaves, 1 failed, 2 existing live skips |
| Corrected-fixture old implementation unit baseline | 164 passed leaves, 0 failed, 2 existing live skips |
| Old implementation real HTTP/PG baseline | 82 passed leaves, 0 failed/skipped |
| Reversible unknown-field deletion mutation | Unit and real regression both exited 1; mutation restored |
| Changed dataplane/provider unit/race suite | 605 passed leaves, 0 failed, 2 existing live skips |
| Changed real HTTP/PG regression with coverage | 82 passed leaves, 0 failed/skipped |
| Browser flows | 6 passed, 0 failed/skipped/retried; test_exit=0, cleanup_exit=0 |
| Quality/release workflow tests | 83 passed, 0 failed/skipped |
| Go vet, scenario ESLint, documentation build | All exited 0; 443 documentation pages and 2 homepages built |

The initial unit failure was an incorrect test assumption: a multipart opening boundary with no part is parsed as an empty form; execution then rejects the missing model. Preserve that behavior and test it explicitly. A truncated nonempty part is the actual malformed-input fixture. No product parser change was made to pass that assertion.

The two pre-existing live skips are TestQiniuSeedanceLive and TestQiniuFalLive, requiring real supplier credentials. They are not counted as passes. The mutation only temporarily removed an outbound opaque field locally and restored the source in finally; this proves regression detection, not an original product defect.

## Reproduction and evidence

Commands were executed against isolated local test dependencies:

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

Local evidence is in `.verification/phase5/P1/`; regression event/cleanup evidence is in `.e2e/runs/`; browser reports/traces/cleanup are in `.e2e/phase5-p1/`. Shared reviewers should use committed tests, these commands and CI artifacts, not depend solely on local files.

The parsing microbenchmark records approximately 386–389 ns / 736 B for small JSON, 2.02–2.10 ms / 1.05 MB for 1 MiB JSON, and 5.25 MB allocated for 1 MiB multipart. This describes one redundant parse's cost, not an end-to-end speedup. Concurrency, first-byte latency and overall memory budgets remain later-batch measurements.

New HTTP regressions reuse the model-contract coverage upload with the backend flag. The release job's existing agent-history- selector includes the new browser file. The 70% patch gate, timeouts and exclusions are unchanged.

## Rollback

Revert the two production files to restore the original parsing call. Keep configurations, bills, tasks and history. There is no migration or rollout switch, and no production access, merge or deployment occurred. P1 acceptance does not replace later combined or external acceptance.

## Model-directory isolation and hot updates

The old getter exposed shared slices/maps. A unit reproduction altered process configuration through a returned view; restoring the old getter made both real HTTP paths fail the disable/isolation assertion. Models now copies configuration containers without JSON round-tripping, preserving numeric types, nil and invalid values for existing validators. Preview compilation and candidate diagnostics share one directory; lookup by runtime ID copies only the matching entry.

Native Responses parsing eligibility is still observed before history restoration. Standard routing captures its directory after admission/guardrails; Bypass create captures before public-alias resolution. In-flight upstream calls finish, subsequent calls observe disable/update. Named credentials, retry authentication and budgets retain their existing boundaries. Pricing plans and settlement fallback reads are unchanged. Cross-update pricing/connection combinations and multi-store consistency remain open; this is not completion of all P2 work.

Verification: 2,622 related Go/race leaves passed, zero failed, four existing skips; final focused checks passed eight leaves. CloneModels, its recursive helper, Models and FindDeployment have 100% focused coverage. Real HTTP/PostgreSQL regression passed 54 leaves with no skips: hot updates, credential rotation, key revocation/budget exhaustion before retries, routing/preview and protocol/history cases. Two bilingual browser flows passed with zero failures/skips/retries and cleanup exit zero. Workflow tests passed 83 cases; vet and scenario ESLint passed.

Existing skips: TestQiniuSeedanceLive, TestQiniuFalLive and TestKodoLiveArchive require external credentials; TestBrowserEnterpriseFixture is a helper started separately by the browser runner. They are not counted as passes; local fixtures do not prove real SSO/vendor/production proxy availability.

Failure evidence is retained: expected old-getter failures and one unused-import build failure after preview cleanup, fixed before rerunning. Review also identified missing catalog registration for the preceding Bypass browser flows; locales now have separate entry files with exact catalog titles. This batch follows the same rule. Filtered browser success never substitutes for full catalog acceptance.

Commands and evidence locations are listed in the [Chinese report](../zh-CN/phase5-execution-baseline.md). Local evidence: `.verification/phase5/P2/`, `.e2e/runs/regression-xHgU1Dlp/`, and `.e2e/phase5-p2-registered/`. Three 100-model nested-directory benchmarks measured 119570–139957 ns/op, approximately 142656 B/op and 2701 allocations/op; this measures isolation cost, not end-to-end latency improvement.

No schema migration, merge or deployment. Reverting code does not revert configuration, spend or history. Permissions, budgets and the 70% patch gate are unchanged.
