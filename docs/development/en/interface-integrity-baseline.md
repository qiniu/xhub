# API and test integrity baseline

[简体中文](../zh-CN/interface-integrity-baseline.md) · [Implementation plan](interface-integrity-roadmap.md) · [Development guide](README.md)

This baseline records a source audit on 2026-10-24 at `185726e37e8f456fa62b8df33560cda153b959d8`. It separates implementation facts, test evidence, and missing capabilities. It is neither a production inspection nor proof that every endpoint works. The first fix covers request parsing, persistence errors, and missing resources in the generic management catalog. It does not change the feature set, provider protocols, sessions, budgets, or production configuration.

## Inventory sources and denominators

The historical route catalog, registered entry points, and working capabilities are different sets.

| Source | Count at the source baseline | What it establishes |
| --- | --- | --- |
| [`internal/catalog/routes.json`](../../../internal/catalog/routes.json) | 755 rows and 755 unique `method + path` pairs; 226 parameterized paths | All embedded declarations, including removed routes and 8 `WEBSOCKET` labels; not 755 working HTTP endpoints |
| Literal `reg.Handle("METHOD /path", ...)` calls in non-test Go files under `internal/gateway` | 216 unique declarations, including 79 absent from the embedded catalog | Direct dedicated-handler declarations; their union with the catalog contains 834 unique declarations |
| Concatenated registrations in [`directoryModule`](../../../internal/gateway/directory.go) | 17 sites using `/scim/v2/connections/{connection_id}` | These sites are excluded from the literal count and must be expanded from actual registration |
| [`docs/testdata/catalog.json`](../../testdata/catalog.json) | 777 unique `method + path` pairs, including 356 unique GET paths | A historical test inventory that differs from runtime declarations; its GET count is not a coverage denominator |
| [`Server.GinRoutes()`](../../../internal/gateway/engine.go) | Runtime inventory not exported in this batch | Gin routes after removal rules, registration order, and parameter normalization; excludes Bypass dispatch before Gin |
| [`registry.Transports()`](../../../internal/provider/registry/registry.go) | Runtime inventory not exported in this batch | Registered provider actions; enumerate `KindBypass` separately, deduplicate `Method + PublicPath`, and retain multiple transports for a path |

The HTTP entry also handles CORS OPTIONS, and Bypass matching runs before Gin. Parameters denote templates rather than individual resource IDs. The static union of **834** is a reproducible source denominator, not the final exposed endpoint count or the number accepted by this PR. Future coverage must separately inventory runtime routes, Bypass, and explicitly refused endpoints, retaining unreviewed items. Do not restore removed routes just to increase coverage.

Registration precedence is defined by [`routes.go`](../../../internal/gateway/routes.go), [`ingress.go`](../../../internal/gateway/ingress.go), and [`removed.go`](../../../internal/gateway/removed.go): dedicated modules register first, the catalog fills remaining paths, duplicates do not replace handlers, and removed paths stay unmounted. Neither the catalog nor `handlerFor` alone identifies the final implementation.

This read-only command reproduces the static counts without starting a service or accessing a database. The pinned commit prevents later source changes from silently changing the baseline.

~~~bash
python3 - <<'PY'
import json, re, subprocess
base = '185726e37e8f456fa62b8df33560cda153b959d8'
def source(path):
    return subprocess.check_output(['git', 'show', base + ':' + path], text=True)
rows = json.loads(source('internal/catalog/routes.json'))
catalog = {(r['m'], r['p']) for r in rows}
files = subprocess.check_output(
    ['git', 'ls-tree', '-r', '--name-only', base, 'internal/gateway'], text=True
).splitlines()
literal = set()
for path in files:
    if path.endswith('.go') and not path.endswith('_test.go'):
        literal.update(re.findall(r'\breg\.Handle\("([A-Z]+) (/[^"\n]*)"\s*,', source(path)))
test_rows = json.loads(source('docs/testdata/catalog.json'))['http_routes']
print('catalog:', len(rows), len(catalog))
print('literal registrations:', len(literal))
print('literal-only:', len(literal - catalog), 'union:', len(catalog | literal))
print('test catalog:', len({(r['method'], r['path']) for r in test_rows}))
print('test GET paths:', len({r['path'] for r in test_rows if r['method'] == 'GET'}))
PY
~~~

## First business paths and existing evidence

The table identifies the first deeply reviewed subset, not a replacement for the full source denominator. Unless stated otherwise, defects describe the baseline commit. New verification belongs in a separate execution record; do not interpret historical defects as post-fix behavior.

| Endpoint or entry | Implementation and caller | Baseline behavior and evidence | Gap and scope |
| --- | --- | --- | --- |
| `POST /credentials`, `GET /credentials`, `GET /credentials/by_name/{credential_name}`, `PATCH /credentials/{credential_name}`, `DELETE /credentials/{credential_name}` | `serveFamilyRoute → family.ServeMgmt → writeCatalogPersist`; the provider UI calls through `networking.tsx` | Normal KV persistence and redacted reads; [`provider_forms_test.go`](../../../cmd/regression/provider_forms_test.go) checks persistence, URL edits preserving secrets, authorization, redaction, and deletion; [`supplier_catalog_test.go`](../../../cmd/regression/supplier_catalog_test.go) checks credentials reaching a local upstream | **First fix**: storage failures must not succeed, missing resources must not be fabricated, and invalid JSON must not become an empty write. Preserve existing successful paths |
| `POST /prompts`, `GET /prompts/list`, `GET/PATCH/PUT/DELETE /prompts/{prompt_id}`, `GET /prompts/{prompt_id}/info` | The same generic handler; current [`prompts/page.tsx`](../../../frontend/src/app/(dashboard)/prompts/page.tsx) uses `ColumnWriteSurface` | Create, update, and delete scenarios use the real page and backend; storage defects are shared | **Shared first-fix scope**: parsing, errors, and readback; this does not implement prompt execution |
| `POST /search_tools`, `GET /search_tools/list`, `GET/PUT/DELETE /search_tools/{search_tool_id}` | The same generic handler; current [`search-tools/page.tsx`](../../../frontend/src/app/(dashboard)/search-tools/page.tsx) | Stores and lists configuration in KV; this does not establish that a search request runs | **Shared first-fix scope**; search execution and connection probing require separate work |
| `POST /prompts/test` | [`theme_settings.go:promptTest`](../../../internal/gateway/theme_settings.go); current page sends `prompt_id` | Checks only whether the ID is nonempty; always returns HTTP 200 with a success/failure string; no version lookup, variable rendering, or model call | **Confirmed defect, subsequent fix**. A success string is not model-execution evidence |
| `POST /search_tools/test_connection` | [`theme_settings.go:searchToolTest`](../../../internal/gateway/theme_settings.go); current page sends top-level name and ID | Checks only name or ID and returns HTTP 200; no search request or credential check | **Confirmed defect, subsequent fix**. Use a local HTTP fixture to prove a request and failure propagation |
| A generic management request containing `jsonrpc` | `writeCatalogPersist` checks the field before resource handling | An authenticated management request receives `{"jsonrpc":"2.0","result":{"tools":[]}}` without the requested resource operation or JSON-RPC method execution | **Confirmed reachable branch, track separately**. It does not bypass authentication and does not prove MCP support |
| `POST /v1/mcp/server/oauth/{server_id}/token` | Dedicated registration in [`routes.go`](../../../internal/gateway/routes.go); [`onboarding.go:mcpOAuthToken`](../../../internal/gateway/onboarding.go); explicitly retained by `removed.go` | Does not validate identity, authorization code, or server configuration; returns a random `at-...` string without a real token exchange | **Confirmed defect, subsequent fix**. No evidence that this string authenticates anywhere, so do not claim privilege escalation. Removal of other MCP routes does not make this dedicated entry unreachable |

Other `/credentials/*` declarations, prompt versions, and the search-provider catalog still need individual semantic review. Fixing KV error propagation does not establish that credential lookup by model, credential encryption migration, or versioned prompt execution is implemented.

## Confirmed false-success patterns

At the baseline, [`writeCatalogPersist`](../../../internal/gateway/family/handlers.go) has four separate problems:

1. It discards `ListKV` errors and may return an empty list, conflating no data with failed reads.
2. It may convert any `GetKV` error into an object synthesized from the ID. Updates likewise treat failed reads as new resources.
3. It discards `PutKV` errors and returns the in-memory object, claiming success without persistence.
4. Its delete loop counts successful calls but still returns HTTP 200 when one deletion fails. Batch atomicity or failure behavior needs an explicit contract.

[`family/codec.go:readMap`](../../../internal/gateway/family/codec.go) also ignores body-read and JSON errors, turning invalid input into `{}`. This amplifies the persistence problems. There are 10 `readMap` implementations across gateway packages. The first fix tightens the touched catalog boundary rather than replacing all parsers and inadvertently changing native payloads or established empty-body behavior.

`writeCatalogPersist` also contains a token-generating path-suffix branch. Its reachable route set needs separate confirmation. The dedicated `mcpOAuthToken` in the table is already confirmed reachable. Dead code or removed catalog entries alone cannot establish production exposure.

## Missing prompt and search execution contracts

A prompt test needs to define ID/version lookup versus inline content, variable and history rendering, selected model and protocol, authorization and budgets, JSON versus SSE, usage, and failure recording. The retained [`useConversation.ts`](../../../frontend/src/app/(dashboard)/prompts/_components/prompt_editor_view/conversation_panel/useConversation.ts) sends `dotprompt_content`, variables or history and expects SSE. The backend accepts only `prompt_id` and returns JSON. A retained component is not proof that the current route mounts it; record the simple live page and retained editor separately.

A search probe needs valid stored or inline provider configuration, authentication, a bounded test query, real response handling, timeouts, and secret protection. The retained [`SearchConnectionTest`](../../../frontend/src/app/(dashboard)/search-tools/_components/SearchConnectionTest.tsx) sends `litellm_params` through `networking.tsx` and expects `status`, `message`, and `results_count`. The backend instead reads top-level name/ID and omits `status`. The current simple page uses another request shape. Establish the contract for actual usage before inventing compatibility for every retained component.

Relabeling these actions as successful structural validation is not complete implementation. If an interim fix removes false success or explicitly reports unsupported behavior, functional completeness remains open until real execution exists.

## Evidence from different test layers is not interchangeable

| Existing entry | Assertion scope | What it does not establish |
| --- | --- | --- |
| [`TestCatalogReads`](../../../internal/gateway/catalog_reads_test.go) | GET paths in the test snapshot; JSON, selected status codes, and error envelopes | Resource existence, storage-error propagation, write effects, or all runtime routes |
| [`column-scenario.ts`](../../../frontend/e2e/column-scenario.ts) | Real page CRUD and cleanup; test actions only match `succeeded` or `flushed` | Prompt execution or search connectivity. The assertion is insufficient for accepting those features |
| [`SearchConnectionTest.test.tsx`](../../../frontend/src/app/(dashboard)/search-tools/_components/SearchConnectionTest.test.tsx) | Rendering, loading, errors, and notifications after mocked responses | Backend protocol execution, network calls, or actual search results; legitimate component mocks are not inherently fake tests |
| [`provider_forms_test.go`](../../../cmd/regression/provider_forms_test.go) and [`supplier_catalog_test.go`](../../../cmd/regression/supplier_catalog_test.go) | Real HTTP, isolated database, credential readback, and local upstream protocol | Uninjected storage failures, commercial suppliers, or production proxies |
| [`scripts/regression.sh`](../../../scripts/regression.sh) | PostgreSQL preflight, strict database mode, and matching-test checks | Correct failure handling in every helper: baseline `testsupport.Postgres` may still skip an invalid DSN and ignores cleanup failures; separate fixes remain |
| [`vitest.config.ts`](../../../frontend/vitest.config.ts) | Unit/component/integration/types projects; 8 historical file exclusions under the quality gate | Passing results for excluded files. Do not add exclusions or hide first failures with reruns |

[`gen-api-types.mjs`](../../../frontend/scripts/gen-api-types.mjs) explicitly refuses generation without a complete Go/OpenAPI source. Preserve that failure. A route list is not a request, response, or error contract, and a fabricated schema must not be used to make the build green.

## Acceptance boundary and follow-up queue

The first fix needs all evidence below. Actual reports determine completion; document checkboxes cannot establish it:

- Unit checks cover objects, empty-body policy, invalid/truncated/non-object JSON, and read failures, plus success/failure decisions. Storage behavior still needs real database verification.
- Real HTTP regression uses private PostgreSQL schemas and reversible read/write/delete failures; assert non-2xx, stable errors, no fabricated resources or unintended writes, retained CRUD/redaction/authorization, and cleanup of faults and resources.
- Browser E2E uses the real page, gateway, and database to save, read back, edit, and delete provider configuration; prove that it reaches a local upstream. At least one failure path must observe a real backend rejection rather than intercepting management APIs to synthesize success or failure.
- Preserve pre-fix failing cases and post-fix results. Report actual commands, pass/fail/skip counts, first failures, reruns, cleanup, and external boundaries. The red-to-green evidence belongs to the same fix PR; do not merge deliberately failing tests first.

This batch addresses [#136](https://github.com/qiniu/xhub/issues/136). Final isolated verification results:

| Command actually run | Result | Local report |
| --- | --- | --- |
| `XHUB_REGRESSION_STRICT=1 go test ./internal/gateway/family -count=1 -json` | 12 top-level tests and 18 subtests passed; 0 failed/skipped | `.verification/interface-integrity-baseline/catalog-package-final.json` |
| `XHUB_REGRESSION_STRICT=1 go test ./cmd/regression -run '^(TestCatalogIntegrity.*\|TestProvider.*\|TestSupplierCatalogLifecycle\|TestOfficialCredentialSupportsNativeModel)$' -count=1 -json` | 9 top-level tests and 17 subtests passed; 0 failed/skipped | `.verification/interface-integrity-baseline/catalog-regression-final.json` |
| `XHUB_REGRESSION_STRICT=1 go test ./internal/gateway -run '^(TestCatalogReads\|TestCredentialCatalogHydration)$' -count=1 -json` | 2 top-level tests and 362 subtests passed; 0 failed/skipped | `.verification/interface-integrity-baseline/catalog-existing-first.json` |
| `bash scripts/e2e.sh scenarios/regression/catalog-persistence-en.test.ts scenarios/regression/catalog-persistence-zh-CN.test.ts` | 2/2 passed, 0 retries; this run selected 2 of 413 registered scenarios, while the other 411 were not run | `.e2e/interface-integrity-final/results.json` |
| `go vet ./internal/gateway/family ./cmd/regression` | Exit code 0 | `.verification/interface-integrity-baseline/vet.log` |

The E2E run used `E2E_RUN_DIR="$PWD/.e2e/interface-integrity-final"`. It created and edited a provider through the real page, refreshed and read back its state, rejected invalid JSON without changing configuration, bound a model to the credential, invoked the local upstream through the playground, checked output and cost, and verified that details returned 404 after deletion. Management API responses were not intercepted or fabricated. Full logs are local artifacts excluded from the Git commit; teammates can reproduce them with the committed tests and commands above.

Running four new HTTP regressions against an isolated source snapshot of the original commit produced 4 failing top-level tests and 15 failing subtests, confirming false successes in the old implementation; 2 authorization subtests passed. Deterministic storage/body tests also produced 2 failing top-level tests and 13 failing subtests before the fix. The initial HTTP test draft used an incorrect credential-detail path, search-update method, master-key management identity, and tag fixture/readback entry. These were test errors, corrected before acceptance. Their first logs were retained and were not classified as product regressions.

This batch preserves valid whitespace-only bodies, existing valid JSON larger than 1 MiB, update-as-create behavior, and credential masking. Database failures now return a redacted 500, and missing details return 404. Batch deletion stops and fails on a real error; earlier deletions are not rolled back. This does not claim that every generic catalog action is implemented. Documentation builds and GET sweeps do not replace complete business acceptance.

Follow-up work proceeds as separate reversible changes:

1. Real prompt [#137](https://github.com/qiniu/xhub/issues/137) / search [#138](https://github.com/qiniu/xhub/issues/138) execution contracts, accounting for the current pages and retained components.
2. OAuth token and generic JSON-RPC false successes, with explicit authorization and protocol behavior for retained features.
3. Strict test infrastructure, cleanup failures, and historical exclusions.
4. An authoritative contract and offline documentation pilot for a verified user or Key domain, then incremental compatibility-layer consolidation.

No production changes, employee-account tests, restored removed routes, or feature reduction are included. Real suppliers, Qiniu SSO, TLS/reverse proxies, and release Docker deployment remain separate acceptance boundaries.
