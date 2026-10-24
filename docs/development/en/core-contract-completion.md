# Stage 3 core contract completion

[中文](../zh-CN/core-contract-completion.md) · [Inventory](../../../contracts/migration-inventory.json) · [Roadmap](interface-integrity-roadmap.md)

This batch follows #160 and covers the agreed supplier, user, key, deployment and logs/daily-usage core set. It does not change authorization, budgets, storage or production deployment. Completion applies to this set, not all API declarations or stages 4/5.

| Domain | Operations | Client / real HTTP evidence | Browser evidence |
| --- | ---: | --- | --- |
| Suppliers | 5 | Supplier clients / SupplierContract tests | Prior supplier contract scenarios |
| Users including passwords | 11 | User clients, userSetPasswordCall / UserContract tests | Prior user scenarios, password admin-reset |
| Key lifecycle | 14 | Key clients, regeneration, aliases; bulk/reset/health are HTTP-only / KeyContract tests | key-lifecycle en/zh-CN |
| Deployments and probes | 9 | modelInfo/Create/Patch/Delete, testConnectionRequest / ModelContract tests | model-contract en/zh-CN |
| Logs and user/team/org daily usage | 9 | log/detail/session/daily clients / UsageContractRealTraffic | core-contract and usage-error en/zh-CN |

OpenAPI generates Go, TypeScript and offline references. CI checks drift and real HTTP responses with an independent validator. TypeScript is not runtime validation. Prior batch evidence is not a claim that every old test was rerun; the PR records actual commands and results.

The inventory combines catalog declarations and literal Go registrations, marking every entry core-contracted or peripheral-unmigrated. Changes require regeneration. Catalog entries can be retired or unimplemented: these counts are **not runtime coverage**. Dynamic SCIM and transport-registry Bypass routes are listed separately.

## Client errors and compatibility

Migrated clients use the existing ApiClient and preserve HTTP status, body and diagnostic. Model-list 403 no longer becomes an empty success. Logs/daily failures must not become zero usage. Probe HTTP200 can still mean healthy=false/status=error; non-2xx throws ApiError. Session verification remains separate from custom-key authentication failure.

Unused keyInfoCall(keys[]) was removed because the actual endpoint accepts one key. AgentBuilder reads the real nested model_info.id; this does not establish that its provider execution feature is fully supported. Unknown cached-token counts remain absent; initialized UI aggregation buckets keep existing calculations. Unknown log JSON and non-JSON text are preserved.

Existing boundaries remain explicit:
- Bulk key updates are non-atomic best effort and updated can be zero. Inspect counts, not only HTTP200.
- Resetting key spend does not erase historical usage or reset parent/user budgets.
- Password resets revoke sessions but retain API keys. Short passwords currently map to HTTP500 through the existing IAM error path; the real test records this rather than promising 400.
- Empty session_id returns an empty log page before authentication under existing behavior, not proof of valid credentials.
- Missing and out-of-scope log details return404; cross-user admin reads are audited. Stored bodies may be unavailable; not every rejection creates a usage record.
- Invalid log page defaults to1, page_size outside1..200 defaults to50. Invalid daily dates leave bounds open; undeclared filters are not guaranteed.

## Explicit exclusions

Organization/team membership CRUD; SSO/SCIM and directory mappings; public model discovery, price refresh, default/fallback routing and builtin discovery; other global/monitor/guardrail/tag/customer/agent reports and budget reconciliation; MCP/OAuth, search, prompts, storage, notifications, video and inference protocols remain outside this core batch. They are visible as unmigrated, not silently declared complete. Expanding stage3 to them requires further domain contracts. Stage4 service boundaries and stage5 execution chains are not implemented here.

## Verification and rollback

Run each gen-key/user/usage-contract.mjs with --check, contract-inventory.mjs --check, go test ./contracts/..., Node generator/workflow tests, Vitest clients and pages, scripts/regression.sh and scripts/e2e.sh. Reports live in the selected .e2e run directory. Missing databases, zero matches, skipped tests and failed cleanup cannot pass. Keep the70% patch gate unchanged and upload coverage from real regression execution.

Tests use isolated PostgreSQL and controlled local HTTP model servers; external vendors and production SSO are not exercised. No schema migration or feature flag is introduced. Roll back domain client/contract changes without restoring databases; never add the development validator to the production request path.
