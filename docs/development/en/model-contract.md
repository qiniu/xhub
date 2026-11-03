# Model and endpoint management contract

Proposal [#159](https://github.com/qiniu/xhub/issues/159). The [OpenAPI source](../../../contracts/models/openapi.json) generates Go/TypeScript types and an [offline reference](model-api.generated.md). Existing handlers, authorization, budgets and protocol execution remain unchanged. No strict runtime middleware is installed.

## Migrated operations

- GET `/v2/model/info`: platform-admin deployment list with modelId/search/page/size. Unknown IDs return an empty list.
- GET `/model/groups`: platform-admin public-model groups, full groups on search matches, size capped at 200, default weights and fallback settings included. Storage errors are 503; invalid stored configuration is 500.
- POST `/model/new`: returns a bare stored deployment. Multiple deployments can share a public name. A saved credential or api_base is required; new manual pricing needs a positive rate.
- POST `/model/update` and PATCH `/model/{model_id}/update`: merge patches, retaining omitted fields and secrets represented by `*****`. Path ID takes precedence; POST accepts model_info.id or id. YAML deployments cannot be edited.
- POST `/model/disable` and `/model/enable`: return the persisted deployment. Disabled deployments cannot serve ordinary inference.
- POST `/model/delete`: returns the removed deployment plus id and deleted=true. Remaining fallback references produce 409; YAML deployments cannot be deleted.
- POST `/health/test_connection`: selects saved configuration by model_info.id, ignoring caller URL/key overrides. Missing input is 400; unknown deployment is 404. Upstream failures may return HTTP200 with status=error and healthy=false.

Public model_name, deployment model_info.id, upstream litellm_params.model, credential protocol custom_llm_provider, execution transport and endpoint_types serve different purposes. Probes validate supported dialogue execution, not every advertised endpoint or capability.

Only modelPatchUpdateCall and modelDeleteCall now use generated response types. Serialization and error handling are unchanged. Create/list/probe and other legacy clients are not fully migrated.

## Compatibility and verification limits

- Real authorization remains authoritative. Tests cover anonymous 401, inference-key/non-platform-session 403 and administrator operations without expanding permissions.
- Open parameter maps, model metadata, patches and the probe union retain raw JSON in Go. Unknown extensions, explicit null and numeric precision must survive roundtrips. Compilation is not proof of valid input.
- The specification describes supported shapes, not stricter production parsing. Legacy readMap does not necessarily reject all unknown fields or malformed JSON. Cross-field ID, price, protocol and credential rules require real HTTP tests.
- Legacy list parameters teamId/model/sortBy/sortOrder/access_group are ignored by the current handler and are not advertised as supported filters. Existing frontend conversion of some 403 responses to empty lists is unchanged.
- Redaction covers sensitive top-level strings, not arbitrary nested extensions. Probe diagnostics are sanitized and do not expose upstream secrets or payloads.
- Probes bypass ordinary routing, budgets and billing and may call upstream services. Separate real-key inference, disabled-deployment rejection and billing checks protect the user-facing path.
- Flat rates accept finite nonnegative numeric strings and null; string semantics remain server-validated.
- New manual prices must include a positive rate; legacy updates may accept zero. These existing semantics and prior spend are not changed.
- Outside this batch: price catalog, builtin discovery/bulk addition, default-weight/fallback writes, monitoring, `/model/available`, SDK model lists, inference wire contracts and the full legacy schema. Existing functionality is not declared broken merely because its contract is not migrated.
- External boundaries: real paid providers, production concurrency, all role combinations and every storage failure source. Creation/update storage failures are injected; not every route's full error matrix is claimed.

## Validation

```bash
npm --prefix frontend run check:api:models
node --test frontend/scripts/gen-model-contract.test.mjs frontend/scripts/gen-key-contract.test.mjs frontend/scripts/gen-user-contract.test.mjs frontend/scripts/gen-supplier-contract.test.mjs scripts/test_quality_workflow.cjs
go test -race ./contracts/...
XHUB_REGRESSION_COVERPROFILE=coverage/model-contract.out bash scripts/regression.sh '^Test(ModelContract|OfficialCredentialSupportsNativeModel)'
E2E_RUN_DIR="$PWD/.e2e/model-contract-final" bash scripts/e2e.sh scenarios/regression/model-contract-en.test.ts scenarios/regression/model-contract-zh-CN.test.ts
```

Regression uses real HTTP/PostgreSQL in a private schema and a controlled upstream, including persisted configuration, permissions, protocol mismatch, upstream403/invalid JSON, disabling, restored inference and usage. English/Chinese browser tests cover deployment detail, successful/failed probes, toggle persistence after reload, inference, billed logs and cleanup. Fixtures are created through real APIs; this does not claim full creation-form coverage.

The Model API contract CI checks generation drift, positive/negative types, validation, real regression and coverage validation/upload under the backend flag. The 70% patch gate is unchanged. Existing release E2E discovers the browser scenarios. Initial tool downloads, CI artifact upload and real-provider availability remain external dependencies. Rollback removes this batch only, with no database or production configuration migration.

## Verification record

2026-10-25: five contract packages / 124 leaf tests, 22 generator/workflow tests, nine frontend tests, four real HTTP/PG regressions and two English/Chinese browser scenarios passed with no failures or skips. Browser test and cleanup exits were both zero. Full TypeScript, go vet and drift checks passed. Existing ESLint errors: 217, below the unchanged 289 limit. This is targeted E2E, not acceptance of all 419 scenarios.

Local evidence: `.verification/model-contract/`, `.e2e/runs/regression-OxRI0749/`, `.e2e/model-contract-final/{results.json,html/index.html,cleanup-status.json}`, `coverage/model-contract.out`. CI regenerates and uploads evidence using the commands above. Local results do not prove production-provider availability.
