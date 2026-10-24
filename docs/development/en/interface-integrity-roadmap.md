# XHub API Integrity and Incremental Decoupling Plan

[中文](../zh-CN/interface-integrity-roadmap.md) | [Development guide](README.md)

Updated: 2026-10-24. Analysis baseline: `185726e37e8f456fa62b8df33560cda153b959d8`. Status: implementation plan; the phases below have not yet passed acceptance.

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

The first phase-2 pilot targets supplier management, whose persistence failure baseline is established: [contract pilot and boundaries](supplier-contract-pilot.md) (Proposal #149). Core user and key batches are documented in [user contracts](user-contract.md) and [key contracts](key-contract.md). Phase 3 adds the [model and endpoint batch](model-contract.md) (Proposal #159). Logs and explicitly excluded operations remain unmigrated; phase 3 is not complete and the compatibility layer has not been refactored.

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
