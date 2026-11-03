# User management API contract

Proposal [#151](https://github.com/qiniu/xhub/issues/151). [OpenAPI 3.0.3](../../../contracts/users/openapi.json) is the structural source for [offline reference](user-api.generated.md), Go and TypeScript types. No handler, database, authorization or budget rule is changed. No strict runtime middleware is installed.

## Scope and compatibility

| Operations | Existing behavior |
| --- | --- |
| GET `/user/list` | Paginated `users`; platform admins see all, team/organization admins are scoped to managed teams, members see themselves. |
| GET `/user/info`, `/v2/user/info` | Both return `{user_info}`. Self/platform-admin authorization; list visibility does not imply detail access. Admin projections include enterprise identity fields. |
| GET `/user/filter/ui`, `/user/available_users` | Platform-admin pickers: load up to 500 users, then case-insensitive email/ID substring filtering. No full pagination. The second alias exists in the actual mount but not the legacy catalog; generation checks both sources. |
| POST `/user/new` | Bare user object, no automatic Key. CSV imports call this once per row, not an atomic batch import endpoint. |
| POST `/user/update` | `{user_info}`, correcting the frontend's obsolete `{user_id,data}` type. Members edit their own profile. Budget/rate/reset writes are separately authorized; team/org admins do not thereby gain identity or bulk-write privileges. |
| POST `/user/bulk_update` | Platform-admin atomic budget/reset changes, with `atomic/batch_id/results/total_requested/successful_updates/failed_updates`; no partial success. |
| POST `/user/delete` | `{deleted:true}`; self deletion is refused, members cannot delete others. Old sessions and Keys become invalid. |

Networking response types for creation/list/detail/update/bulk/delete use the generated contract, preserving existing URLs, request serialization and error behavior. Wider legacy page view types remain compatible; detail `teams` is corrected to the actual object array. The old whole-API `schema.d.ts` is not rewritten. Dedicated password reset, analytics and directory synchronization endpoints are outside this batch.

- Single-user `max_budget`: omission preserves (uncapped on create), null/empty string clears, zero denies spend. Finite nonnegative numeric strings remain supported. Rates require nonnegative integers up to 2147483647. Full string-number semantics are enforced by the service, not approximated as Go `ParseFloat` by OpenAPI.
- Bulk budgets accept numbers/null only, never numeric/empty strings. Reset-only updates must omit the budget. Maximum 1000 unique targets and 1MiB body. Explicit `all_users:true` includes admin and disabled accounts.
- Reset day: omitted preserves; zero disables; 1–31 resets at Beijing midnight, clamped to month end. Null/string/fraction/out-of-range values fail. Editing budgets never resets spend. Clearing the personal cap does not bypass team/organization restrictions.
- Lowering a target below spend/reservations and invalid parent-child allocations still fails. Unrelated soft-budget overspend does not block new users. Schema validation makes no concurrency or idempotency guarantees.
- Email is trimmed and lowercased; display names preserve user language.
- Role defaults to user, retaining existing internal_user/proxy_admin aliases. Extra legacy fields may be ignored, while bulk rejects extras. A member submitting an admin role does not acquire it.
- Creation `organization_id` **assigns organization administration**, not ordinary affiliation. Use `team_id` for team membership; `team_role` must match a team.
- SSO fields must be supplied together; both empty unbind, omission preserves. Existence, scope and uniqueness are server checks. Binding changes invalidate old sessions.
- Password length is 8–72 UTF-8 **bytes**, not OpenAPI character length. Admin password updates and other fields retain separate processing; this document does **not** promise the entire update endpoint is transactional. Atomicity tests cover account fields and bulk budgets.
- Teams are loaded on list/detail; create/update/bulk snapshots may contain empty teams. `models`, `organizations`, `metadata` and `key_count` are compatibility placeholders, not effective access or actual key counts. `sso_user_id` is not the new external identity ID.
- List aliases remain `total/total_count`, `page/current_page`, `page_size/size`. Invalid page defaults to 1; page_size outside 1..500 defaults to 100. Legacy string query parsing is preserved. Detail accepts legacy GET object-body user_id overriding query; new callers should prefer query.

## Evidence boundaries

The development validator accepts declared operations and supported JSON-object inputs, verifies status/media/shape and requires error code to match HTTP status. No default response swallows unknown statuses. It cannot prove authorization, identity pairing/uniqueness, target uniqueness, allocation or billing; real HTTP, PostgreSQL and upstream evidence are required.

Legacy create/delete/detail `readMap` parsing remains permissive: this batch does **not** claim every malformed JSON returns 400. Update/bulk strict malformed JSON paths are tested against the real server. The 503 shape is documented but no real 503 fault is injected in this batch. Production concurrency, actual corporate IdP/vendor services and the remaining whole-API schema are outside coverage.

Generated Go patch/union inputs use raw JSON to preserve missing/null/zero semantics. Go compilation alone is not validation. Responses remain generated structs; TypeScript constrains known fields. Standard generator comments are retained; bilingual semantics belong in the source schema.

## Checks and maintenance

```bash
npm --prefix frontend run check:api:users
npm --prefix frontend run check:api:suppliers
node --test frontend/scripts/gen-user-contract.test.mjs frontend/scripts/gen-supplier-contract.test.mjs scripts/test_quality_workflow.cjs
go test -race ./contracts/...
bash scripts/regression.sh '^Test(UserContract|UserBulkBudget|UserMonthlyBudget|UserEnterpriseIdentity|SoftBudgetAllocationHTTP|SupplierContract)'
E2E_RUN_DIR="$PWD/.e2e/user-contract" bash scripts/e2e.sh \
  scenarios/regression/user-csv-import-en.test.ts \
  scenarios/regression/user-csv-import-zh-CN.test.ts \
  scenarios/regression/user-bulk-budget-en.test.ts \
  scenarios/regression/user-bulk-budget-zh-CN.test.ts \
  scenarios/regression/user-enterprise-identity.test.ts
```

The independent User API contract CI job checks drift, generator failure paths, schema/types and real HTTP/PG regressions. Release E2E still covers user flows. The supplier pilot shares generator/validator infrastructure and is also retested.

New HTTP regressions cover nine operations, pagination aliases, no automatic Key, conflicts, budget state, member/team/org boundaries, identity binding and storage-write failure preservation. Existing real regressions cover monthly reset, parent allocations, overspend and identity unlinking. EN/zh browser scenarios cover CSV download/invalid preview/create/duplicate failure/login/cleanup, bulk save/reject/restore inference and actual spend, and local TLS IdP binding/login/unbinding with session invalidation. All data and upstream requests are isolated test fixtures, never employee accounts or vendor credentials.

Regenerate via `npm --prefix frontend run gen:api:users`. Check mode never repairs drift. Tool versions remain pinned as in the supplier pilot; first-time tool installation needs network, generated references are offline. Reverting this batch requires no data migration or production configuration rollback.

## Executed verification (2026-10-24)

Final selected runs: three Go contract packages passed; 13 generator/workflow tests passed; 94 frontend tests passed; 26 real backend regression leaves passed with zero skips; six EN/zh E2E cases passed with zero skips and both test/cleanup exit codes zero. User-contract package statement coverage is 100%, shared validator 97.2%; this does not mean full user-management feature coverage. Full frontend `tsc --noEmit --incremental false`, `go vet ./contracts/... ./cmd/regression` and both artifact drift checks passed. ESLint reports 217 existing errors against the unchanged 289-error budget.

Additional command executed in frontend:

```bash
npx vitest run src/components/networking.user-contract.test.ts src/components/networking.test.ts \
  'src/app/(dashboard)/hooks/users/useCurrentUser.test.ts' \
  'src/app/(dashboard)/hooks/users/useUsers.test.ts' \
  --reporter=json --outputFile=../.verification/user-contract/frontend-unit.json
npx tsc --noEmit --incremental false
```

Local evidence: `.verification/user-contract/frontend-unit.json`, `contract-unit.log`, `generator-workflow-final.log`, `typecheck.log`; backend `.e2e/runs/regression-BQUBjddM/regression.log`; browser `.e2e/user-contract-final/html/index.html`, `results.json`, `cleanup-status.json`. Raw artifacts stay untracked; CI reruns the documented commands and publishes evidence.

Initial failures corrected test assumptions and generation: generic tenant fixtures already grant organization administration, so that grant was removed before member tests; email lowercasing and cross-scope budget 403 assertions follow existing rules. Required-only anyOf branches initially generated TS unknown; negative compilation tests caught this and full branch schemas fixed it. No runtime rule or failure gate was relaxed.
