# Phase 4: compatibility-layer ownership audit

> Update 2026-10-28: use the [fixed closure inventory](phase4-closure.md) for current remaining work. Below is the original registration audit and historical verification record.

This audit is based on `939e0d6c` (#180 on main). **Phase 4 is not complete.** Supplier, search-tool, prompt, guardrail and policy configuration have separate boundaries. Access-group #182 and vector-store configuration #184 are still pending PRs. Passing tests, merging and deploying are separate states.

This change adds tests, a snapshot and development documentation only. It does not change APIs, permissions, budgets or storage, lower the 70% coverage gate, merge or deploy.

## How the inventory is produced

`internal/gateway/route_audit_test.go` calls the actual modules' `Mount` methods, including dynamically assembled SCIM paths, through production `Server.handle`. Existing code determines module precedence, removed-route filtering, Gin parameter normalization and first-registration wins. The audit compares the resulting Gin route set with normal `mountModules` / `mountCatalog` registration.

The reviewed snapshot is `internal/gateway/testdata/route-ownership.json`. Ordinary Go tests fail on registration drift. Mutation tests actually remove the identity module, add/remove catalog entries and let a module take over a catalog route. An in-memory Gin HTTP test verifies first-handler precedence and restoration of named parameters.

This supplements `contracts/migration-inventory.json`; it does not replace OpenAPI:

- The existing inventory records declarations and contract migration.
- The new snapshot records ownership for default built-in module registration. It does not execute every business handler or prove vendor, SSO, proxy or full API availability.
- In `catalog:<handler>:<class>`, class is the existing catalog classification, **not proof of actual authorization inside that handler**. Protocol handlers can delegate further.
- `module:<name>` means a module took precedence over a catalog declaration. `module_only` includes registrations absent from the catalog.
- Internal handler changes still require domain unit tests, real HTTP/database regression and browser E2E. Registration does not qualify an unknown API for Swagger's supported set.

## Counts and interpretation

| Scope | Count | Meaning |
| --- | ---: | --- |
| Catalog declarations | 755 | Includes aliases and removed entries; not supported API count |
| Removed catalog declarations | 89 | Not mounted by the registrar |
| Catalog declarations taken over by modules | 138 | Such as users, Keys, models and usage |
| Declarations mounted through catalog handlers | 528 | Includes data, public, retired and management compatibility paths |
| Of these, `serveFamilyRoute:manage` | 296 | Requires further delegation analysis; not 296 confirmed defects |
| Module registrations absent from catalog | 95 | Includes dynamically assembled directory-sync routes |
| Unique Gin routes | 757 | Parameter aliases can share a registration key; counts are not directly additive |

Of the 296 management declarations, 69 fall within existing supplier (8), search (6), prompt (8), policy (28) and guardrail (19) delegation ranges. These are resource-entry counts, not declarations that every operation works: guardrails and other adapters retain explicit 501/404 branches. The 11 access-group entries in #182 and 5 vector-store configuration entries in #184 will remain catalog entries after merging, because internal delegation preserves the old HTTP adapter.

A smaller catalog count or a shorter source file is therefore not a completion criterion.

## Existing boundaries and pending work

| Resource | Boundary / PR | State and limitation |
| --- | --- | --- |
| Suppliers | `suppliers` / #169, #171 | Main; configuration, redaction, rotation and real local upstream calls |
| Search tools | `searchconfig` / #173 | Main; persistence, actual probes, upstream failures and deletion |
| Prompts | `promptconfig` / #175 | Main; versions, persistence, actual probes and failures |
| Guardrails | `guard` management service / #177, #178 | Main; persistence and runtime block/redact/allow; unsupported remote capabilities still rejected |
| Policies | `policyconfig` / #180 | Main; existing configuration/runtime guard boundaries retained, no new authorization semantics |
| Access groups | `accessgroupconfig` / #182 | Pending; configuration is not runtime model authorization; singular/plural storage remains separate |
| Vector-store configuration | `vectorconfig` / #184 | Pending; singular configuration is separate from plural data plane; completed metadata does not prove indexing |

Their PRs contain #182/#184 test evidence. This round validates main without those pending services. Independent branch passes do not establish post-merge integration acceptance.

## Remaining work

Preserve existing rules while proceeding in this order. Establish normal, failure, persistence and runtime evidence before extracting a service or explicitly rejecting unsupported behavior. Example paths below are investigation entry points, not support claims.

| Order | Scope | Finding and next action |
| --- | --- | --- |
| 1 | Tags and customer configuration: `/tag/new`, `/tag/info`, `/customer/update` | Tag writes still use generic KV; `GET /tag/list` uses the usage module's empty catalog. Existing batch-delete regression verifies persistence through detail reads, not list/runtime consumption. Inventory readers and writers before extracting the inconsistency into another service. |
| 2 | Budget configuration and old IAM aliases: `/budget/new`, `PATCH /organization/member_update`, `POST /team/block` | Generic ownership does not establish an IAM/budget operation. Check old callers and actual effects individually. This audit changes no permissions, limits, blocking or settlement. |
| 3 | Settings, health and action-shaped paths: `/coordination_redis/settings/test`, `/health/drain`, `/debug/memory/gc/configure` | Names do not prove probes or actions execute. Verify effects and failures with local dependencies. A KV write returning 200 is not an implementation of the named action. |
| 4 | Retained server, plugin, skills, OAuth aliases and other compatibility entries | Check actual handlers, authentication and consumers; separate explicit 501/410, removed entries and generic persistence. Do not bulk restore or remove features. Existing OAuth/JSON-RPC rejection tests do not cover every similarly named path. |
| 5 | Proven duplicate/dead branches and combined acceptance | Delete old code only after proving no mounted entry or other caller still uses it and obtaining compatibility regression evidence. Re-run combined regression after #182/#184 merge, then update snapshot/status. |

Inference-shaped `/rag/*`, `/ocr` and `/models/{model_name}:generateContent` entries also appear in the management group and need prioritized semantic checks. This is a registration-based risk, not a vendor execution failure reproduced in this round. It is not evidence that Phase 5 already covers them.

Actual data-plane lifecycle work—native/cross-protocol behavior, SSE, history ownership, retries, logs and settlement—remains Phase 5. Confirmed false success in management endpoints should receive a separate fix and evidence, not be hidden by deferring it to Phase 5.

## Validation and reproduction

Executed: 9 leaf route-audit/mutation tests passed; 37 real PostgreSQL/HTTP regression leaves passed with zero skips; 8 browser scenarios passed with zero failures. The other 419 browser scenarios were not run. Browser coverage includes supplier save/reload/invalid update/model calls, explicit OAuth rejection, guardrail management/data plane and policy isolation in both locales, plus English prompt/search actual probe failures. E2E cleanup recorded `test_exit=0, cleanup_exit=0`.

~~~bash
go test -race ./internal/gateway -run '^TestRoute(OwnershipAudit|Audit)' -count=1
bash scripts/regression.sh '^Test(SupplierServiceRuntimeRotation|SupplierCatalogLifecycle|SearchConfigServiceLifecycle|PromptConfigService.*|GuardManagementService.*|PolicyConfigService.*|CatalogIntegrity.*)$'
E2E_RUN_DIR="$PWD/.e2e/phase4-audit" bash scripts/e2e.sh \
  scenarios/regression/guard-management-service-en.test.ts \
  scenarios/regression/guard-management-service-zh-CN.test.ts \
  scenarios/regression/policy-config-service-en.test.ts \
  scenarios/regression/policy-config-service-zh-CN.test.ts \
  scenarios/regression/catalog-persistence-en.test.ts \
  scenarios/regression/catalog-persistence-zh-CN.test.ts \
  scenarios/regression/prompts-probe-en.test.ts \
  scenarios/regression/search_tools-probe-en.test.ts
~~~

Only after reviewing actual registration changes, regenerate locally with `XHUB_UPDATE_ROUTE_AUDIT=1 go test ./internal/gateway -run '^TestRouteOwnershipAudit$' -count=1`, then run the read-only check without that variable. CI does not update the snapshot by default. The ordinary Go quality job runs the test; no new service is required.

Local reports: `.verification/phase4-audit/report.txt`, `.verification/phase4-audit/registration.jsonl`, `.e2e/phase4-audit/results.json`, `.e2e/phase4-audit/cleanup-status.json`. The regression script prints its run directory. This round uses no production accounts, external models, real enterprise SSO or production reverse proxy; local upstream success does not verify those boundaries.
