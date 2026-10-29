# Phase-four combined acceptance report

## Validated revisions and conclusions

The integration baseline is main `e8a29c4f972e8832e1c6e02a8226d0befcc65584`. Branch `codex/stage4-final-integration` combines five candidates and preserves their history. It has not been merged into main or deployed.

| PR | Validated candidate commit | Scope |
| --- | --- | --- |
| #231 | 3db9211f41aedec845f3b2931fc9ae64190d3871 | 15 legacy settings declarations, real theme services and frontend error handling |
| #233 | 42b42bbf06eadded271e0225b224264cfda4b8f6 | 14 model aliases and real model management |
| #235 | d9765274c04a2960fad55798ac532b0232efdf46 | 26 analytics declarations and real logs/usage |
| #237 | e505484ecf19954a79f97bb64630307281414b86 | 7 legacy file/WebSocket declarations; native protocols retained |
| #239 | 23bdd2ae787672786bb845cd531da7378060ac59 | 9 discovery declarations and shared search-provider definitions |

At integration review, #239 passed its test, build, contract and Codecov checks; publish/deploy were skipped by the PR workflow design. Fenno reported no defects on product commit80e7578;23bdd2a only updated evidence references. The reviewer did not run browser tests; this integration did. The other four candidates had no failed or pending checks. Individual candidate results do not replace integration CI.

## Integration changes

- Preserve exact dispatch for settings, models, analytics, discovery and files, after the existing JSON-RPC rejection and before generic KV handling.
- Combine real-regression coverage selectors and release E2E lists without dropping a batch. Add combined cases without increasing timeouts, skipping tests or lowering gates.
- Add `cmd/regression/stage4_combined_acceptance_test.go`: generate billed traffic in one HTTP/PostgreSQL service, interleave old operations, verify unchanged permissions, sessions, deployments, theme, KV and billing, then exercise real disable/enable, zero-budget403 and upstream failure logs.
- Add `frontend/e2e/stage4-combined-scenario.ts` and two locale entry points: provider selection, billed chat, interleaved rejections, model pages, log details/reload and real usage. Delete test models and isolate database schemas.
- Extend `internal/gateway/phase4_closure_test.go` to fix the pending queue at five candidates and71 declarations. Remove obsolete pending PRs already merged through#229, retain historical observations and fold historical tables in both closure documents.
- Do not modify authorization rules, budget algorithms or `codecov.yml`. The70% target applies to existing patch coverage; it is not a claim that every aggregate report exceeds70%.

## Actual validation

These results describe the original combined acceptance run, not a new execution merely caused by translating this report.

| Validation | Result | Local report |
| --- | --- | --- |
| Strict-database Go race/coverage | 4244 leaf tests passed;4 existing external/special-fixture skips; zero test/build failures (4721 pass events include parent and child tests) | `unit-race-final.json` / `unit-final.out` |
| Full HTTP/PostgreSQL backend regression | 1254 leaves passed;32 existing conditional skips; zero failures | `backend-full.log` |
| Five-batch and combined regression/coverage | 112 leaves passed; zero failures/skips | `backend-boundaries.log` / `backend-boundaries.out` |
| Target browser E2E | 23 passed; zero failures/skips/retries; test_exit=0 and cleanup_exit=0 | `.e2e/stage4-final-integration/` |
| Relevant frontend unit/component tests | 165 passed; zero failures/skips | `frontend.json` |
| Workflow/scenario registration | 108 passed; zero failures/skips | `workflow-final.log` |
| Closure inventory gate | 11 pass events including parent/child tests; zero failures | `closure-final.json` |
| API compatibility | 7 unit tests passed;5 OpenAPI specifications have no breaking changes against main | `compatibility-unit.log` / `compatibility.log` |
| Existing ESLint budget | 221 errors ≤ existing maximum289; unchanged | `eslint-budget-final.log` |
| Document links | 3 passed | `document-links-final.log` |
| Go vet / diff whitespace checks | Passed | `vet.log` |

The32 conditional backend skips include live vendors, unconfigured real Agent CLIs, the opt-in local Next proxy and cooldown cases requiring a dedicated Redis. They are not counted as passes. The112 focused cases have no skips. The4 Go unit skips cover two paid multimodal live fixtures, live Kodo archival and a dedicated enterprise browser fixture. No skip was added.

Run commands from the repository root unless the frontend directory is indicated:

```bash
# Match CI strict database and race coverage; run backend regression separately and exclude third-party npm Go sources
export XHUB_REGRESSION_STRICT=1
export XHUB_TEST_DATABASE_URL='<isolated-local-PostgreSQL-DSN>'
go list ./... | grep -vE '/cmd/regression$|/frontend/node_modules/' > /tmp/xhub-unit-packages.txt
xargs go test -json -race -timeout=25m -covermode=atomic -coverprofile=.verification/stage4-final-integration/unit-final.out < /tmp/xhub-unit-packages.txt

go vet ./...
go test -json ./internal/gateway -run '^TestPhase4Closure'
XHUB_REGRESSION_COVERPROFILE=.verification/stage4-final-integration/backend-boundaries.out bash scripts/regression.sh '^Test(SettingsOperationBoundary|ModelAliasBoundary|AnalyticsAliasBoundary|ProviderFileBoundary|LegacyWebsocketBoundary|DiscoveryBoundary|SearchProviderDiscovery|SearchProbe|Stage4CombinedAcceptance)'
bash scripts/regression.sh
node --test scripts/test_quality_workflow.cjs scripts/test_release_workflow.cjs frontend/e2e/checklist-reporter.test.cjs frontend/e2e/scenario-coverage.test.cjs frontend/e2e/optional-settings-response.test.cjs
PATH="$HOME/go/bin:$PATH" node --test frontend/scripts/check-contract-compatibility.test.mjs
PATH="$HOME/go/bin:$PATH" node frontend/scripts/check-contract-compatibility.mjs origin/main

# Run in frontend/
npx vitest run src/components/Teams.test.tsx src/components/team/TeamInfo.test.tsx src/components/networking.test.ts src/lib/http/api.test.ts 'src/app/(dashboard)/old-usage/page.test.tsx'
npx eslint . --format json --output-file ../.verification/stage4-final-integration/eslint.json
node scripts/check-eslint-error-budget.mjs ../.verification/stage4-final-integration/eslint.json 289
```

```bash
E2E_PORTABLE_CONSOLE=1 E2E_RUN_DIR="$PWD/.e2e/stage4-final-integration" bash scripts/e2e.sh scenarios/regression/stage4-combined-en.test.ts scenarios/regression/stage4-combined-zh-CN.test.ts scenarios/regression/settings-operation-en.test.ts scenarios/regression/settings-operation-zh-CN.test.ts scenarios/regression/model-alias-en.test.ts scenarios/regression/model-alias-zh-CN.test.ts scenarios/regression/analytics-boundary-en.test.ts scenarios/regression/analytics-boundary-zh-CN.test.ts scenarios/regression/protocol-boundary-en.test.ts scenarios/regression/protocol-boundary-zh-CN.test.ts scenarios/regression/discovery-boundary-en.test.ts scenarios/regression/discovery-boundary-zh-CN.test.ts scenarios/columns/search-tools-connection.test.ts scenarios/regression/search_tools-probe-en.test.ts scenarios/pages/logs.test.ts scenarios/pages/audit-logs.test.ts scenarios/keys/rotation-secret.test.ts scenarios/regression/swagger-reference-en.test.ts scenarios/regression/swagger-reference-zh-CN.test.ts agent-native-protocol.test.ts
```

Three preparation failures were retained and not counted as passes. First, a historical untracked file, `output/log-filter-investigation/log_filter_probe_test.go`, was accidentally compiled as a separate package by `go test ./...`. It is not part of Git and was preserved under `.verification/stage4-final-integration/historical-log-filter-investigation/`. A later bare `go test ./...` also launched the long backend suite and reached Go's default10-minute timeout; the existing `bash scripts/regression.sh` runner subsequently passed the full suite without changing its timeout. Finally, the local Go1.25 installation lacked `covdata`, so coverage collection failed for three packages without tests. Rebuilding that tool from the matching local GOROOT source fixed the environment; the strict race/coverage command then passed. No tool binary or toolchain-version change is committed. No repository test was deleted or skipped. ESLint's pre-existing errors are evaluated against the unchanged budget, not represented as zero errors.

## Reports and limits

- Full local evidence: `.verification/stage4-final-integration/`, including `unit-race-final.json`, backend logs, coverage profiles, frontend JSON, ESLint budget, compatibility and workflow results.
- Browser evidence: `.e2e/stage4-final-integration/results.json`, Playwright reports and `cleanup-status.json`. The23 target tests do not mean all483 registered scenarios ran. The19/483 inventory count measures registered entries; the run also contains multiple native-protocol tests. Unselected entries are not failures.
- Full backend run: `.e2e/runs/regression-KM5nrlMz/`; focused112-leaf run: `.e2e/runs/regression-xQc9URyU/`.
- Upstreams are actual local HTTP protocol fixtures, without browser-route mocks. They verify requests, authorization, persistence, errors, billing and cleanup, but do not prove live paid-model/search availability, enterpriseSSO connectivity/certificates or production traffic.
- Existing live tests skip when their required credentials are absent. Skips are reported separately. Database connection failures, zero matched tests and cleanup failures still fail the gate.
- The integration PR still requires its own CI/review. Do not merge, deploy or close the five source PRs as part of this verification. If the combined PR is later merged, reconcile the included source commits instead of applying the same patches again.
