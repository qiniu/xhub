# XHub API Integrity and Incremental Decoupling Plan

[中文](../zh-CN/interface-integrity-roadmap.md) | [Development guide](README.md)

Updated: 2026-10-24. Analysis baseline: `185726e37e8f456fa62b8df33560cda153b959d8`. Status: staged implementation. Core contracts and excluded domains are tracked separately; submission, integration into main, and deployment are separate acceptance events.

XHub is serving production users. This plan preserves existing features, pages, and compatibility endpoints. It first establishes complete API behavior, trustworthy test results, and consistent frontend/backend contracts, then gradually consolidates the compatibility layer and execution pipeline. Improvement is measured by how easily a behavior change can be understood, verified, and rolled back, rather than by the number of lines removed.

## Scope and invariants

- No feature reduction, navigation redesign, bulk removal of old pages, or large framework replacement. Do not introduce a Python service or split the application into microservices.
- Preserve users, SSO, keys, budget rules, model permissions, conversation identifiers, and stored cost snapshots. Any necessary semantic change requires a separate proposal.
- Correcting false success and invalid-input handling is in scope, but its impact on existing callers must be recorded. A bug fix that changes observable behavior must not be presented as a behavior-preserving refactor.
- Migrate one explicit business boundary per batch. Preserve existing HTTP paths and compatibility for valid requests. Do not replace storage structures, protocol implementations, and frontend callers together.
- Admission and permissions for the selected target remain server-side checks. Retries must not charge twice. After stream output begins, another upstream must not continue the same output. Do not blindly replay task creation when its outcome is unknown.
- Acknowledge usage queues only after the database commit succeeds. Do not remove local durability protections merely to simplify asynchronous logging.
- This document does not authorize deployment, dependency installation, or feature changes. Follow the repository issue conventions to reuse or add proposals, and schedule a release only after implementation and tests pass.

## Confirmed facts and verification boundaries

| Area | Fact in the baseline | What must still be demonstrated |
| --- | --- | --- |
| Probe endpoints | [promptTest and searchToolTest](../../../internal/gateway/theme_settings.go) check identifier fields only; they return success wording without invoking a model or search service | Whether valid configurations actually work, and whether failures, timeouts, permissions, and cleanup are handled correctly |
| Generic persistence | Some branches in [writeCatalogPersist](../../../internal/gateway/family/handlers.go) ignore storage errors and return success | Which real routes reach those branches; failures must not produce false success, unintended writes, or partial updates |
| API type source | [gen-api-types.mjs](../../../frontend/scripts/gen-api-types.mjs) stops generation because a complete Go OpenAPI source is missing | The new contract must match real handlers; unmigrated APIs must not be reported as covered |
| Public documentation route | [removed_schema_test.go](../../../cmd/regression/removed_schema_test.go) requires `/openapi.json` to return 404 | Generate documentation offline first; any new online endpoint needs an explicit route, authorization policy, and disclosure scope |
| Repeated parsing and clients | Management JSON readers, the two frontend HTTP clients, and Bypass request parsing contain repeated implementations | Before consolidation, verify error behavior, null semantics, uploaded files, and preservation of original fields |
| Test gates | [quality.yml](../../../.github/workflows/quality.yml) uses a historical lint baseline; the [Vitest configuration](../../../frontend/vitest.config.ts) excludes some known failing files in quality mode | Reports must disclose the actual execution scope, exclusions, skips, initial failures, and rerun results |

These facts do not imply that all APIs are unusable or all tests are false. A product endpoint that falsely reports a successful connection test is different from a unit test that legitimately uses a mock.

Record “unknown,” “partially verified,” “verified,” and “explicitly unsupported” separately. Unknown capabilities must not automatically be labeled supported, nor should existing request handling change without a contract decision. Every inference needs an evidence entry point, an unresolved question, and a verification method. Do not promise performance improvements, supplier compatibility, or security without evidence.

## Delivery standard for API integrity

Create a reviewable API inventory. Begin with one business domain, then expand to every officially exposed endpoint. Register compatibility aliases, native endpoints, and catalog fallback endpoints too; do not substitute a small new subset for the complete coverage denominator.

Each entry must contain at least:

| Category | Required information |
| --- | --- |
| Identity and implementation | Method, path, business domain, handler, compatibility aliases, implementation status, and contract version |
| Request | Authentication, role and tenant scope, field types, defaults, absent/null/zero semantics, unknown fields, and request size |
| Response | Success and error bodies, status codes, pagination, and relevant headers; a generic 200 entry is insufficient |
| Business result | Storage changes, runtime application point, whether an upstream is contacted, cost, idempotency, revocation, and cleanup |
| Evidence | Unit, real HTTP regression, and user-journey E2E cases and reports; supplier behavior that remains unverified |

A successful save must be readable from actual state, and its effect must be observable on the relevant runtime path. If an endpoint only saves configuration, clearly state that connectivity has not yet been verified. A successful test must distinguish structural validation, network connectivity, and an actual protocol invocation.

When an existing “test connection” or “test prompt” feature promises actual execution, changing its wording to “structural validation passed” does not complete the feature. Record its actual promise and add invocation, failure, and cleanup checks. If a change only removes false success temporarily, keep the feature's integrity status marked incomplete.

Reconcile existing semantics using the implementation, callers, regression tests, and confirmed product rules. Do not invent backend response fields from old type files, upstream documentation, or model inference alone. Record contract conflicts as explicit decisions rather than quietly accepting them by changing test expectations.

## Automated API documentation and contracts

### Recommended pilot

Select one group of user or key management create, read, update, and delete operations. Retain the existing routes and handlers. Use OpenAPI 3.0.3 for a tooling compatibility pilot; finalize the format and directories after pinning and verifying tool versions. The paths below are suggestions only.

~~~text
api/contracts/<domain>.yaml  Authoritative contract
  -> Go request/response types   Adopted by existing handlers one API at a time
  -> TypeScript types           Used by the corresponding frontend callers
  -> Static API documentation   Local and CI build artifacts
  -> Real HTTP contract checks  Validation of actual responses and errors
~~~

The initial contract comes from confirmed business behavior. After migration, it becomes the domain's sole source of API definitions. Business logic remains handwritten; generated code is not a business implementation. Keep existing callers and types for unmigrated APIs, track migration by domain, and do not overwrite the entire `schema.d.ts` at once.

### Tool responsibilities

| Tool | Planned purpose | Boundary |
| --- | --- | --- |
| [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) | Generate Go types from contracts; evaluate integration with existing Gin handlers in the pilot | Do not replace the whole routing framework in the first batch; a generator does not implement authorization or business validation |
| [openapi-typescript](https://openapi-ts.dev/introduction) | Generate frontend types from the same contract while retaining the current client ecosystem | Passing type checks does not demonstrate correct runtime responses |
| [Swagger UI](https://swagger.io/open-source/swagger-ui/) | Render API documentation for local viewing and testing-environment exploration | Displayed documentation does not prove implementation; do not send requests to production by default |
| [swaggo/swag](https://github.com/swaggo/swag) | Alternative for generating documentation from Go annotations | Its current primary documentation describes Swagger 2.0; compatibility with the OpenAPI 3 toolchain must be verified. Do not maintain two authoritative sources |

Documentation does not require a new external service. Pin dependencies and static resources, and keep real credentials out of examples. Initially, do not restore `/openapi.json` or enable interactive production writes by default. If documentation is later added inside the console, separately verify access permissions, its address, and request destinations.

Add contract syntax validation, generated-output drift checks, breaking-change checks, and real-response validation to CI domain by domain. First record compatibility rules that are already valid, including unknown fields and numeric strings. Generating types does not justify a new strict middleware that rejects all legacy requests in one change.

### Special boundaries for native and streaming APIs

OpenAPI describes endpoints, authentication, error envelopes, and expressible fields. It cannot by itself demonstrate correct SSE event ordering, failures after the first byte, settlement after stream interruption, tool calls, or conversation continuation.

Native requests must preserve unknown fields, numeric precision, multipart file contents, and protocol events. Management APIs may gradually adopt strict types; native supplier payloads must not be indiscriminately deserialized and stripped of unknown fields. Cross-protocol conversion should handle only explicitly supported semantics. Do not generalize local fixture compatibility to every supplier.

## Rules for trustworthy tests

### What each layer can demonstrate

| Layer | Real boundary and purpose | Must not claim |
| --- | --- | --- |
| Unit and component | Parsing, authorization decisions, conversion, and state machines; controlled mocks and fault injection are allowed | A successful supplier connection or a completed real database transaction |
| Backend regression | Real HTTP handlers, isolated PostgreSQL, actual persistence and admission; a local protocol upstream can record requests and inject faults | Passing a local upstream test proves Fenno or another real supplier works |
| Browser E2E | Real pages through the gateway to the data plane, asserting persistence and upstream results; no browser interception that fabricates success for key management APIs | Clicking a button, checking a toast, or receiving 200 alone completes business acceptance |
| Real supplier acceptance | A bounded check with an identified endpoint, model, protocol, time, budget cap, and dedicated test identity | One success proves compatibility for an entire supplier or all models |

Arrange real supplier checks separately from the default offline suite. Do not borrow employee accounts or record chat contents. When credentials are unavailable, explicitly report the boundary as unverified rather than producing a passing result.

The current browser suite uses a production build of the console, real gateway handlers, and local dependency fixtures. Enterprise mode runs a test-specific gateway entry point. This does not validate startup of the release Docker images or demonstrate that real Qiniu SSO, external TLS, or a reverse proxy works. Keep separate acceptance items for these deployment boundaries.

### Required failure and cleanup coverage

- Save flows must cover invalid JSON, absent/null/zero fields, insufficient permissions, missing resources, database failure, read-back, and runtime effects. Verify that failures create no fabricated objects or unintended changes.
- Connection tests must use a local HTTP fixture to verify that an actual request occurs. Inject 401, 403, 429, 5xx, timeouts, and invalid bodies separately. If a feature performs structural validation only, report that level explicitly instead of displaying connection success.
- Inference coverage includes JSON and SSE, same-protocol and cross-protocol paths, tools and images, parent responses and old conversations, cancellation, and interrupted streams. Check client results, upstream attempt counts, log correlation, and cost consistency.
- Once a streaming response is committed, its HTTP status may already be fixed at 200. Record failures inside the stream separately; do not pretend a fresh HTTP error response can still be sent.
- Key unit and regression failure cases should reproduce against the unfixed implementation first. Use local, reversible fault injection to confirm the tests fail as intended. Never conduct destructive experiments in production or shared databases.
- Do not hide initial failures with reruns or expand exclusion lists to make CI green. Missing required cases, zero matched cases, skips caused by missing required dependencies, or cleanup failures must block acceptance of the batch.
- Clean isolated schemas, independent Redis namespaces, and test resources on both success and failure, and retain cleanup results. Strict mode also needs coverage for invalid DSNs, connection failures, and deletion failures.

Prefer the existing strict regression and E2E isolation facilities. Assign an owner and restoration condition to each lint baseline item and test exclusion. When a change touches an excluded file, execute it separately and report the result.

## Decoupling boundaries

Retain the existing modular monolith. Clarify responsibilities within modules before considering directory moves; do not add a second unifying framework.

| Boundary | Responsibility | Do not expand into |
| --- | --- | --- |
| HTTP and compatibility adapters | Legacy path mapping, request parsing, and response envelopes | Guessing business behavior from paths and fabricating success |
| Domain services | Resource validation, authorization, transactions, and business results | Supplier protocols and SSE handling |
| Configuration and routing | Compile effective configuration, filter candidates, and build an attempt plan | Redefining identity, budgets, or conversation ownership |
| Protocol adapters | Preserve native payloads and perform explicit cross-protocol conversion | Writing identity data or settling charges independently |
| Executor | Timeouts, cancellation, retry boundaries, and output lifecycle | Individual supplier field rules |
| Accounting and observation | Usage, price snapshots, logs, and idempotent persistence | Changing upstream response semantics |

Delegate compatibility routes to explicit services one route at a time, preserving existing response shapes. Start with one actively used management resource, verify save/read/failure/permission behavior, then expand to the next. Removing generic fallbacks requires an explicit coverage inventory; do not broadly turn existing routes into 404s or delete old APIs.

Standard and Bypass paths can share lifecycle, diagnostics, and settlement components while retaining differences in history, guardrails, asynchronous tasks, and retries. Rechecking target permissions on every retry is necessary behavior, not redundant code to remove.

## Implementation phases

Advance by completion criteria, not promised calendar dates. Clear bugs in phase 1 may proceed in parallel with the offline tooling pilot in phase 2. Migration of a particular API must wait until its baseline and error behavior are understood.

| Phase | Deliverables | Completion criteria | Rollback boundary |
| --- | --- | --- | --- |
| 0 Establish the baseline | Core endpoint inventory, compatibility aliases, existing tests and exclusions, minimal reproductions | Every API in the first batch has real handler and caller evidence; completeness gaps are explicit | Inventory and tests only; no production behavior changes |
| 1 Correct error semantics | Tests that fail before the fix and pass afterward; fixes for storage errors, JSON validation, and probe check levels | Fault injection prevents false success; success paths, permissions, i18n, and cleanup pass | Independent fix commits without data migration; restoring false success is not the default remediation |
| 2 Pilot contracts | OpenAPI for one domain, Go and TS types, offline documentation, and contract CI | Reproducible generation with no drift; real HTTP positive and negative cases match the contract; old clients remain usable | No new online documentation route; retain existing handlers and unmigrated callers |
| 3 Extend core contracts | Domain-by-domain migration for users, keys, models, logs, and shared frontend error policy | Itemized coverage for migrated APIs; the full inventory still shows unmigrated entries; core user E2E paths pass | Roll back by domain; do not replace the entire schema or all clients at once |
| 4 Consolidate the compatibility layer | One explicit service boundary per resource type; remove replaced duplicate implementations | Regression evidence covers old URLs, permissions, responses, transactions, and errors | Preserve storage and external APIs; adapters can be restored independently |
| 5 Organize the execution pipeline | First remove repeated Bypass parsing, then unify configuration snapshots and attempt results, then extract lifecycle components | Protocol, stream, history, log, cost, and retry matrices pass; unknown fields are preserved | Change one boundary at a time; candidate canaries retain the old path; preserve history formats |

Suggested initial PR sequence: baseline and negative tests → persistence errors and strict JSON → truthful probe semantics → user or key contract pilot → corresponding frontend migration → one compatibility resource delegated to a service. Start execution-pipeline restructuring after these results stabilize.

The fail-before/pass-after verification takes place within the same fix PR, retaining evidence of the failure before the fix and the pass afterward. Do not merge deliberately failing tests first. A separate baseline PR adds only passing behavioral baselines and gap records; it must not manufacture a green result by skipping new negative cases.

Track errors in established behavior as BUG issues. Use Proposals for new documentation routes, check-level semantics, or contract workflows. Cross-module plans use Architecture-Design under repository conventions. This document does not replace acceptance records for individual changes.

Stage 2 piloted supplier contracts (#149), followed by users, keys, and model/endpoints (#159). Stage 3 closes the 48-operation core set in #161, including logs/daily usage, password reset, and key lifecycle. See [core contract completion](core-contract-completion.md). #162 was merged into the already-closed source branch of #160, so its changes did not reach main. A new main-targeted PR must integrate that work and pass compatibility and real-test gates before acceptance. Peripheral APIs remain explicitly unmigrated.

## First phase-4 batch: supplier configuration service

[Proposal #168](https://github.com/qiniu/xhub/issues/168) extracts suppliers only; it does not complete phase 4.

- `family` retains legacy URL parsing, management authorization, JSON validation, HTTP envelopes, and error mapping.
- `internal/gateway/suppliers` owns CRUD, patch merging, defaults, and response redaction through a narrow KV interface, without HTTP or data-plane dependencies.
- Shared sensitive-field detection moves to `internal/credentialfields`; search configuration retains its own recursive merge semantics.
- Storage kind/IDs, replacement creates, update upserts, nontransactional batch deletion, extensions, nulls, and zero values remain unchanged. No schema migration or cache is added.
- Model execution still reads the original `credentials` records. Real regression covers address/key rotation, mask preservation, upstream 401, recovery, and billing; English and Chinese E2E covers UI editing through inference.
- Reverting this batch restores the previous implementation without database rollback. This batch is PR-only; merge and deployment are separate decisions. Other resources migrate individually; native protocol execution is outside this batch.

## Stage 4, second batch: search configuration service

[Proposal #172](https://github.com/qiniu/xhub/issues/172) moves search configuration CRUD, defaults, recursive secret patches, and response redaction into `internal/gateway/searchconfig`. Compatibility adapters retain routing, authorization, JSON parsing, and HTTP envelopes. Connection probes keep reading the original `search_tools` storage and sending real upstream requests.

Preserve legacy namespaces, same-ID replacement, missing-record upsert, non-transactional deletion, and null/zero/extension fields. Search execution, permissions, and budgets remain unchanged; no cache or database migration. Public routes still follow the existing registry: internal legacy namespaces do not introduce public APIs or expand stage-three OpenAPI coverage.

Acceptance covers storage failures, nested masks, actual search authentication, invalid credentials, recovery, bilingual pages, and cleanup. Real PostgreSQL regression coverage is uploaded by CI and bilingual search E2E flows run in release verification. Revert this service and adapter batch without data rollback; PR submission, merging, and deployment remain separate acceptance events.

## Stage 4: prompt configuration service

[Proposal #174](https://github.com/qiniu/xhub/issues/174) moves prompt configuration CRUD and defaults into `internal/gateway/promptconfig`. Compatibility adapters retain routing, authorization, strict JSON parsing, and HTTP envelopes. Prompt tests keep reading the `prompts` store and using the existing inference pipeline.

Preserve same-ID replacement, missing-record upsert, shallow patches, nested creation expansion, non-transactional deletion, and null/zero/extension fields. Variable rendering, permissions, budgets, guardrails, routing, logs, and billing remain unchanged, with no database migration. `version` remains existing metadata; the test executor accepts only the default version. This batch does not implement version history, Dotprompt, or new capabilities, or expand stage-three contract coverage.

Acceptance covers real SQL failures, edited saved templates, actual outbound content, usage/cost, bilingual failure/recovery flows, and cleanup. Revert this service and its adapters without data rollback; merging and deployment remain separate decisions.

## Stage 4: guardrail configuration management service

[Proposal #176](https://github.com/qiniu/xhub/issues/176) moves configuration reads, patch merging, validation, name conflict checks and persistence from `guard.Manage` into a package-local `managementService` with a narrow KV store interface. Existing validation and response redaction remain shared; management lists retain editable configuration. Execution still loads rules independently through `listGuardrails`, rejects ambiguous names and inherits global defaults. These are existing semantics; this batch does not merge the two read paths.

The HTTP adapter retains authorization prerequisites, routing, JSON parsing, YAML read-only checks, error mapping and the global mutation lock. That lock still spans reading, parsing and saving, preserving error precedence and preventing concurrent duplicate names or lost credential updates. This batch neither tightens legacy JSON parsing nor declares internal branches to be public routes.

The `guardrails` namespace, masked/empty credential preservation, explicit null deletion, extension fields, list aliases and existing 400/404/409/503 responses remain unchanged. No data migration or changes to permissions, budgets, trials, execution, usage or unsupported capabilities. Real HTTP/PostgreSQL tests cover concurrency, storage failures and execution; bilingual browser tests cover create, trial, redaction, edit, block and delete. Regression coverage is uploaded separately; the 70% gate remains unchanged. Reverting the service and adapter commit requires no data rollback. Merge and deployment remain separate decisions.

## Acceptance and release for each batch

Each report must identify the commit, affected APIs, old and new behavior, test layers, commands, pass/fail/skip/exclusion/rerun counts, report paths, cleanup results, and external dependency boundaries. Tests not executed must say “not run.” A command that matches no cases must not be reported as passing. Count Go top-level tests and subtests separately. Reports retain only request IDs and redacted structures, not real keys, passwords, or chat contents.

Retain at least the following user journeys, selecting those affected by each batch:

| User journey | Required assertions |
| --- | --- |
| Enter model and key pages after login | Valid sessions remain active; genuinely invalid sessions log out; a custom key error does not end the login session |
| Import users or update budgets | Preview matches execution; real data can be read back; invalid input and failures cause no unintended writes; SSO mappings remain unchanged |
| Create or update a key | Preserve omitted-budget semantics, the personal total budget, permission narrowing, and the behavior of other keys |
| Save a model or connection configuration, then invoke it | The selected endpoint and protocol actually take effect; failures can be located; credentials are not disclosed |
| Continue an old conversation and make streaming calls | Tools, images, history ownership, errors, and usage remain consistent; streams with emitted output are not replayed |
| View logs and usage | Correlate all attempts, upstream errors, and final outcomes for the same request; explain costs using the original snapshots |

For page changes, verify Chinese and English, dynamic parameters, and error states. Example prompts use English; preserve user input as supplied. Complete acceptance in a test environment before release. Changes to execution behavior start with controlled-account canaries; documentation or build success must not trigger automatic full deployment. Observe protocol errors, login failures, billing differences, and latency. Derive thresholds from the phase 0 baseline rather than inventing them.

Shadow comparisons may compare pure parsing or routing results, but must not duplicate paid upstream requests. Rollbacks restore implementations or configuration snapshots; they must not reverse already recorded bills, clear old conversations, or replace database formats on both sides at once.

### Existing validation entry points

The following commands are examples for implementation work, not a claim that this plan has executed or passed them. Select suites for the specific change. Use dedicated test databases; never point these commands at production. See the [testing guide](testing.md) for parameters.

~~~bash
XHUB_REGRESSION_STRICT=1 go test ./internal/util ./internal/provider/... ./internal/dataplane/... -count=1
bash scripts/regression.sh
bash scripts/e2e.sh
~~~

~~~bash
cd frontend
npm run test:unit
npm run test:component
npm run test:integration
npm run test:types
~~~

`regression.sh` enables strict database mode and saves full logs. `e2e.sh` uses a local protocol upstream and an isolated environment. Do not mix `make testdata`, real-supplier mode, or skips caused by missing dependencies into default acceptance. Before the contract pilot is complete, `npm run gen:api` should still fail explicitly; do not fabricate a temporary schema merely to make the command pass.

At the end of each batch, it must be possible to answer: which module owns the behavior, where its contract lives, which failures are rejected, which real test proves it, and how it can be rolled back without affecting data.

## How to use references

Use LiteLLM's [request lifecycle](https://docs.litellm.ai/docs/proxy/architecture), [routing responsibilities](https://docs.litellm.ai/docs/routing), and [Responses adapter interface](https://github.com/BerriAI/litellm/blob/main/litellm/llms/base_llm/responses/transformation.py) as references for responsibility separation and compatibility validation. Its [native Responses configuration](https://docs.litellm.ai/docs/response_api_native_passthrough) illustrates why capabilities and execution paths require explicit declarations. It does not establish the capabilities of XHub's current suppliers, and is not a reason to copy every feature or commercial-edition restriction.

Pin the reference version or commit during implementation and record what is being adopted. Preserve XHub's existing accounting reliability, permission checks, and history constraints. No Python service or change to the current feature set is required.


See [core contract completion](core-contract-completion.md) for stage3 scope, evidence and explicit unmigrated boundaries. Submission does not mean merge or deployment.

## Phase 4: policy configuration management service

[Proposal #179](https://github.com/qiniu/xhub/issues/179) moves policy configuration into `internal/gateway/policyconfig`. HTTP adapters retain authentication, path parsing, response envelopes and error mapping. The service only depends on KV storage, not authorization, budgets, guardrail execution or model execution.

- Preserve the separate `policy` / `policies` namespaces, legacy `policie_` IDs and `policie_alias`, replacement creates, shallow updates, missing-record upserts, nested expansion, extension fields, null/zero values and non-transactional deletion counts.
- `status` and `version` remain configuration metadata. Existing generic dispatch for attachment, resolution and version-history paths is preserved; it is not evidence of a complete policy engine. No new capability or phase-3 OpenAPI coverage is claimed.
- Unit tests cover normal, boundary, failure and namespace behavior. Real HTTP/PostgreSQL regressions cover permissions, SQL read/write faults, recovery and cleanup; real local-provider calls verify unchanged billing and zero-budget denial. The same regressions also run against the pre-refactor management implementation as a compatibility baseline.
- Browser tests in both console locale environments cover page creation, reload, HTTP updates, rejection/recovery, model calls and deletion. The legacy policy page still uses its existing Chinese controls; this backend extraction does not claim to fix that localization debt. Independent request contexts clean up after success, failure and timeout; cleanup errors fail tests. CI uploads real regression coverage without lowering the 70% gate.
- No data migration or production deployment. Reverting the service and adapter restores the old implementation over the same stored records.

## Phase 4: access-group configuration management service

[Proposal #181](https://github.com/qiniu/xhub/issues/181) moves access-group CRUD and legacy normalization into `internal/gateway/accessgroupconfig`. HTTP adapters retain authentication, path parsing, strict JSON, response envelopes and error mapping.

- Scope is the 11 registered operations: `/access_group/list|new|{id}/info|{id}/update|{id}/delete`, `/access_groups`, and list/create plus item get/update/delete under `/v1/access_group`. Singular and v1 routes share `access_group` storage; the plural list still reads the separate `access_groups` namespace.
- Preserve replacement creates, missing-record upserts, shallow patches, nested expansion on creation, array defaults, extension fields and non-transactional deletion. Keep legacy name fallback, null/zero/unexpected-type handling, and the existing creation-time fallback for `updated_at`.
- These are legacy configuration records. Current model authorization does not consume their `models/access_model_names/assigned_key_ids/assigned_team_ids` fields. There is no active standalone access-group management page. This change adds no page or grants, does not restore removed budget, unified-access-group or MCP routes, and does not expand phase-3 OpenAPI coverage claims.
- Run the same real HTTP/PostgreSQL regressions before and after extraction, covering all 11 operations, real SQL read/write/delete faults, recovery and cleanup. Real local-provider requests verify billing, unchanged Key model restrictions and zero-budget denial.
- Both console locale E2Es edit configuration through management APIs, then log in as a real ordinary user and verify the model page, 500 budget, playground conversation and model availability after deletion. Independent cleanup runs after success, failure and timeout; cleanup errors fail tests. CI uploads actual regression coverage with the 70% gate unchanged.
- No database migration, permission or budget-rule change. Revert the service and adapter to use the same stored records; merging and deployment remain separate decisions.

## Phase 4: vector-store configuration management service

[Proposal #183](https://github.com/qiniu/xhub/issues/183) delegates the five singular /vector_store management operations (list/new/info/update/delete) to vectorconfig.Service. HTTP adapters retain authentication, path parsing, strict JSON, response envelopes and error mapping. Storage remains in the vector_store KV namespace, with no migration.

- Preserve replacement creates, missing-record upserts, shallow patches, nested expansion, non-transactional deletion, extension fields and null/zero values. Name, alias, status and file_counts defaults follow the old behavior.
- status=completed and file_counts are legacy configuration metadata, not proof of indexing or retrieval. The plural /vector_stores and /v1/vector_stores data-plane paths are not migrated. File writes and search still return 501; management configuration does not appear in caller-owned data-plane resource lists.
- Run the same real HTTP/PostgreSQL regressions before and after extraction: all five operations, permissions, SQL read/write/delete faults, recovery and cleanup. Real local-provider calls verify unchanged costs and zero-budget rejection.
- Both console locale environments cover page creation/reload, API update/invalid-input/recovery, unsupported-operation rejection, model calls and deletion. Existing Chinese controls remain unchanged; this backend extraction does not claim to fix legacy localization debt. Independent cleanup tests cover business failures, timeout, cleanup errors and zero deletions.
- CI collects and uploads real regression coverage without lowering the 70% gate. No expansion of phase-3 OpenAPI coverage or change to authorization/budgets. Revert the service and adapter to reuse the same records; merging and deployment remain separate decisions.

## Phase 4 remaining-scope review

The [compatibility-layer ownership audit](compatibility-layer-audit.md) supplements the static contract inventory with actual module registration, distinguishing main, pending PRs and generic catalog entries. Extracted configuration services do not complete Phase 4: remaining management semantics require individual verification, while data-plane lifecycle work remains Phase 5.

## Stage 4: tag and end-customer configuration

[Proposal #189](https://github.com/qiniu/xhub/issues/189) builds on the tag read/write fix in #188. The narrow KV service `metadataconfig` owns CRUD, legacy defaults and list projections for `tag`, `tags`, `customer` and `end_user`. Existing adapters retain authentication, request parsing, envelopes and error mapping. Public scope is new/info/update/delete/list under `/tag`, `/customer` and `/end_user`; internal `tags` compatibility does not add public endpoints.

Customer lists still combine two independent namespaces and expose only user_id/spend/blocked/max_budget. Identical IDs remain two rows rather than one identity. A failure reading either namespace now returns a sanitized 500 instead of a misleading empty or partial 200; an empty database still returns an empty list. Tags retain their name dictionary. Customer GET detail currently reads user_id from a JSON body; query-only behavior is unchanged.

blocked/max_budget remain configuration metadata. This batch adds no authorization, blocking or budget enforcement. block/unblock, statistics and other execution-shaped legacy endpoints are excluded and require a separate semantic audit. Registration or successful storage does not prove their operation. Swagger support claims are unchanged.

Real HTTP/PostgreSQL tests run before and after extraction. Customer read faults fail before the fix and pass after it; CRUD, namespace isolation, permissions, model calls and billing remain covered. English/Chinese browser tests exercise customer persistence and tag refresh/error recovery/chat/deletion. Real Playwright subprocesses verify cleanup after failures and timeouts. CI separately collects regression coverage without lowering the 70% gate.

No database migration is needed; reverting the service and adapters preserves existing data. This batch depends on #188 and is not a claim of merge, deployment or completion of stage 4. Live providers, production SSO and reverse proxies are outside the offline boundary.

## Stage 4: budget configuration and legacy organization/team endpoints

[Proposal #191](https://github.com/qiniu/xhub/issues/191) moves budget configuration persistence and defaults into `internal/gateway/budgetconfig`. The HTTP adapter retains authorization, path/ID parsing and response envelopes. Separate `budget`/`budgets` namespaces, overwrite-on-create, shallow patches, missing-record upsert, null/zero values, legacy nested-field expansion and non-transactional deletion counts remain unchanged.

These configuration records are not account budget ledgers. Saving `max_budget: 0` does not automatically assign that limit to an organization, team, user or Key. Existing IAM limits, inheritance, soft budgets, renewals and reservation reconciliation keep their original handlers. This change does not connect the two concepts or expand Stage 3 OpenAPI support claims.

The following 20 registered legacy endpoints have no IAM executor. Previously they could write ineffective KV records, return unrelated objects or report a missing catalog record. After the existing management authorization, they now return `501` / `unsupported_iam_operation`, without reading or writing the generic catalog:

| Method | Paths |
| --- | --- |
| POST | `/organization/info` |
| PATCH | `/organization/member_update`, `/v2/organization/{organization_id}` |
| GET | `/organization/spend/report` |
| POST | `/team/block`, `/team/unblock`, `/team/bulk_member_add`, `/team/key/bulk_update` |
| POST | `/team/model/add`, `/team/model/delete`, `/team/permissions_bulk_update`, `/team/permissions_update` |
| POST | `/team/{team_id}/disable_logging`, `/team/{team_id}/member/{user_id}/reset_spend` |
| PATCH | `/team/{team_id}` |
| GET | `/team/filter/ui`, `/team/metadata_schema`, `/team/permissions_list`, `/team/spend/report`, `/team/{team_id}/members/me` |

Clients of these unimplemented endpoints must handle the explicit rejection instead of treating a historical 200 as a successful IAM mutation. Real organization/team creation, editing, membership, lists, details and `/team/spend/by_user` retain their existing handlers. Removed callback routes remain 404. Anonymous 401 and member 403 responses are unchanged.

Validation includes the pre-extraction budget baseline, false-success reproduction, real PostgreSQL read/write failures and recovery, all 20 legacy endpoints, unchanged IAM data, real local-provider inference and charges, and existing zero-budget denial. Browser tests run in both console locales through budget creation, editing, refresh, failure/recovery, legacy rejection, inference and deletion. The old budget controls remain Chinese; this batch does not claim to fix their existing localization debt. Independent fixtures clean up after success, failure and timeout; cleanup failures fail the tests. CI uploads real regression coverage and retains the 70% gate.

No KV migration or deletion is required. Budget service extraction can be reverted independently while retaining stored records; restoring false success is not a valid recovery strategy. Tests use isolated PostgreSQL and a local protocol provider, not external model credentials. Creating the PR does not merge or deploy it.

## Phase 4: truthful operational results

[Bug #193](https://github.com/qiniu/xhub/issues/193) addresses legacy operations that treated KV persistence as execution and dependency health that was reported without probing. Fifteen catalog operations without executors now reject explicitly; `/health/services` also replaces its fixed healthy response with `501 unsupported_operation`. Scope is limited to coordination Redis settings/testing, legacy memory/debug entries, and backlog/drain/history/license/shared-status entries. Some old GETs returned catalog 404s, others empty results; POSTs only stored KV. These paths now state that they are unimplemented without writing ineffective records or deleting existing ones.

`/health/readiness/details` probes the database on every request, returning 503 on failure while preserving successful status, version and detail fields. Public liveness and real model connection probes remain unchanged, as do anonymous 401, member 403, budgets and billing. Retired alert-service queries retain 400. No Redis, GC or drain executor is added and no phase-3 supported contract coverage is claimed. Other operations, including cache configuration/flush, still need a separate audit.

Real HTTP/PostgreSQL tests first reproduce false success and readiness after database disconnection, then verify the fix. Unit tests cover path boundaries and recovery; browser tests in both locales cover login, explicit failures, real health status and playground chat. CI separately collects/uploads regression coverage; the 70% gate stays unchanged. No migration is required. Reverting this batch restores the old implementation, including its incorrect success semantics. Submission does not mean merge or deployment; paid providers and production are outside this validation.

## Phase 4: real cache-management effects

[Bug #195](https://github.com/qiniu/xhub/issues/195) separates cache management from the generic catalog and customer-list file into `cacheadmin`. The HTTP adapter retains administrator authorization; the module receives the actual process cache without configuration KV or Redis dependencies.

| Endpoint | Behavior in this batch |
| --- | --- |
| GET `/cache/settings` | Reports actual runtime state: `enabled: true`, `cache_type: memory`, `scope: process`, `configurable: false`; no ineffective saved configuration or credentials are echoed |
| GET `/cache/ping`, `/ping` | Retains the process-memory readiness response; an uninitialized cache returns 503, without claiming a Redis probe |
| POST `/flushall` | Retains the success envelope and really clears the current process response cache; other instances, sessions, usage and Redis are unaffected |
| POST `/cache/settings`, `/cache/settings/test`, `/delete` | No runtime executor exists for these operations; return `501 unsupported_operation` without writing catalog records or echoing credentials |

Compatibility changes are intentional: settings GET no longer returns arbitrary persisted JSON, and ineffective writes/deletes change from 200 to 501. Historical KV records remain stored but unused. `/cache/delete` and `/cache/flushall` remain unregistered (404). No phase-3 OpenAPI coverage is added. The bilingual console shows runtime state, scope and real clearing, with no ineffective save action.

Real HTTP/PostgreSQL tests first reproduce configuration echo, false connection tests and false deletion, then verify explicit rejection without writes. Unit tests exercise actual memory readiness/clearing. Browser flows cover both languages, free hits, billed local-upstream calls after clearing, and real authorization failure/recovery. Permissions, budgets and the 70% gate stay unchanged; regression coverage is uploaded separately. No migration is needed. Reverting restores the old behavior, including false success; that is not a recommendation to revert. Paid providers and production are outside this validation; other legacy operations remain tracked separately.

## Stage 4: legacy server OAuth boundary

[Bug #197](https://github.com/qiniu/xhub/issues/197) reproduces a separate set of aliases reaching generic KV storage: authorization returns 200/status=ok, stores/echoes submitted credentials, and credential deletion deletes the ordinary server record. No OAuth executor exists for these aliases. They differ from the `/authorize/*` and `/v1/mcp/.../token` paths fixed by #144.

- Reject seven unimplemented operations: `POST /server/oauth/session`; `GET /server/oauth/{server_id}/authorize`; `POST /server/oauth/{server_id}/register` and `/server/oauth/{server_id}/token`; `POST` and `DELETE /server/{server_id}/oauth-user-credential`; `GET /server/{server_id}/oauth-user-credential/status`.
- Keep the existing management authorization first: anonymous 401, ordinary user 403, administrator 501/unsupported_operation with no-store. Reject before reading the body or accessing storage; no secret echo, token, redirect, record creation or deletion. Ordinary server CRUD, enterprise SAML, model access and budget rules remain unchanged.
- This does not implement OAuth or expand Stage 3 OpenAPI/Swagger support. Ordinary server configuration only stores records. Other legacy health, user-credential, user-env-vars, plugins/skills aliases still require separate audits; Stage 4 is not complete.
- Red/green real HTTP/PostgreSQL tests compare storage snapshots, ordinary CRUD, sessions, local upstream inference and billing, and zero-budget rejection. A separate real enterprise SAML regression protects login. English and Chinese browser flows verify that rejected OAuth operations leave playground inference usable and strictly clean their records. CI uploads separate real regression coverage; the 70% gate remains unchanged.
- No migration or automatic cleanup of existing KV records. Reverting this handler restores old dispatch but also reintroduces the defects; rollback requires that tradeoff to be considered. No automatic merge or production deployment.

## Stage 4: legacy server health and per-user configuration boundary

[Bug #199](https://github.com/qiniu/xhub/issues/199) is reproduced with real HTTP/PostgreSQL: `GET /server/health` only lists configuration with `status=ok`, without probing. Generic KV dispatch treats `user-credential` / `user-env-vars` child paths as ordinary server IDs, stores/echoes secrets and can delete unrelated ordinary configuration.

- Reject exactly six operations: `GET /server/health`; `POST` and `DELETE /server/{server_id}/user-credential`; `GET`, `POST` and `DELETE /server/{server_id}/user-env-vars`. Keep management authorization (anonymous401, ordinary user403) and JSON object validation (malformed400). Valid administrator requests return501/unsupported_operation with no-store, without touching storage or external services.
- Ordinary server CRUD, including records named user-credential/user-env-vars, retains its storage format. Enterprise SSO, removed MCP prefixes and dedicated skill handlers keep their existing paths. No new personal secret store or MCP executor, permission/budget rule change, or expansion of Swagger support.
- Red/green real regression compares database snapshots and checks name collisions, authorization/errors, ordinary CRUD, sessions, inference/billing and zero-budget denial. Catalog traversal asserts the two read operations' precise501 contract. Bilingual browser tests verify configuration readback, rejection, real local upstream playground replies and strict cleanup. CI separately collects/uploads regression coverage and runs both browser scenarios; the70% gate remains unchanged.
- No migration or automatic cleanup of historical records. Reverting this boundary restores old dispatch and its defects. Server import/approval, plugins, skill consumers and dead branches still require individual audits; this batch does not complete Stage4. Existing dedicated skill handlers must not be mistaken for placeholders merely because their paths appear in the catalog.

## Phase 4: legacy server import and submission boundary

[Bug #203](https://github.com/qiniu/xhub/issues/203) reproduces five false successes over real HTTP/PostgreSQL: import merely stores its body, registration has no review state machine, submissions lists ordinary configuration, and approve/reject treat the action name as the record ID.

The exact five operations now return `501/unsupported_operation` with `Cache-Control: no-store`, after existing authorization and JSON validation and before storage. Ordinary server CRUD, colliding resource names, 404 responses for the unregistered `/v1/mcp/server` counterparts, permissions and budget rules stay unchanged. This does not implement MCP execution or expand the core OpenAPI inventory. No schema migration or historical record deletion is performed.

Tests first reproduce all five failures, then verify no writes, error envelopes, ordinary CRUD, real local inference billing and zero-budget rejection. English and Chinese browser flows exercise the actual gateway and verify successful playground inference by upstream request count after the rejected operations. CI uploads dedicated regression coverage and selects both release flows without lowering the 70% gate. External MCP services are not exercised.

Remaining work includes plugin management, skill consumers, residual management entries (including execution-looking RAG/OCR routes), obsolete branches, and a combined regression of all pending PRs on a temporary integration branch. Individual green PRs do not prove combined compatibility. SSO metadata refresh remains a separate feature for the user-selected merge batch; nothing is merged or deployed here.
