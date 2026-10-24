# Key management API contract

Implements #153 on top of the user contract in #152, reusing the supplier pilot's pinned generators and development validator. No production handler, permission, budget, model protocol, database schema or deployment configuration changes.

## Scope

`contracts/keys/openapi.json` generates Go types, TypeScript types and the [offline reference](key-api.generated.md). Eight operations are covered:

| Operation | Success |
| --- | --- |
| `POST /key/generate` | Key object with plaintext `key` and `token` |
| `GET /key/list` | `keys/total_count/current_page/total_pages/size` |
| `GET /key/info`, `POST /v2/key/info` | `{info: ...}`, including pending reservations |
| `POST /key/update` | Key object, no `data` envelope or plaintext |
| `POST /key/block`, `POST /key/unblock` | Explicit `blocked: true/false` |
| `POST /key/delete` | `{deleted: count}`, not a boolean |

Only create/update/delete frontend response types are connected to the generated contract. Related view declarations now represent actual null values. Request serialization and runtime behavior remain unchanged. Other list/detail clients and the old aggregate view are only partially migrated; legacy display fields do not prove server support. No generated handler or replacement client is installed in production.

Excluded: rotation/regeneration, reset spend, bulk editing, aliases, health, team/model/log APIs and the legacy whole-API `schema.d.ts`. Existing rotation E2E is supplementary compatibility coverage, not a new rotation contract.

## Verified semantics

- Defaults to a personal key for the current user; explicit personal ownership requires a user ID. Team derives from membership; teamless users can create keys. Authorization, template visibility and parent model restrictions remain enforced by the actual service.
- Blank creation budget does not reserve personal funds. Update omission preserves, null/empty string clears the separate key cap, zero blocks this key. Personal/team/org caps still apply; multiple keys do not preallocate or reset personal spend.
- Budgets accept finite nonnegative numbers or parseable strings; rates require nonnegative int32 integers. OpenAPI does not reimplement full string parsing semantics. Real HTTP tests cover invalid inputs.
- Empty models or the all-proxy-models/all-team-models sentinel restores inheritance without widening parent access. Omitted route_template_id preserves selection; null/empty clears it. Regression checks effective template provenance and actual inference.
- Empty creation duration means no expiry. Omitted/null/empty update duration preserves existing expiry, rather than clearing it. Nonempty duration is a positive integer plus s/m/h/d, rejecting overflow.
- Only llm_api keys and llm_api_routes are supported. Management keys, automatic rotation and grace periods are unsupported; fixed response fields describe restrictions, not successful implementations.
- Non-creation responses must not expose `key` or stored hashes. Ordinary list/detail token is masked; personal list token is a record ID. Use the ID for detail queries, not a masked token for inference.
- Empty budget_reservations may be null; populated entries are the actual BudgetReservation structure, not BudgetReservationView from a different management page. budget_pending is their total.
- scope=personal is the logged-in owner only, even for admins; user_id filters cannot widen it. Ordinary list management scope remains unchanged.
- Page 1..100000 and size 1..100 default to 1/50. Invalid pagination/sorting returns 400; empty results have one total page. The legacy key_hash filter actually compares record ID.
- Delete authorizes all targets first, then deletes individually. Mixed unauthorized targets prevent starting deletion, but a later storage failure can leave partial deletions. It is not a batch transaction; repeated deletion returns 404.
- Error codes must match HTTP statuses. Unknown statuses, HTML and redirects cannot pass response validation. No default success response exists.

## Explicit gaps

1. The unused legacy keyInfoCall sends `{keys:[...]}` although POST /v2/key/info requires one key. Real regression confirms 400; this contract does not invent batch support or modify that function.
2. setKeyBlockedState substitutes the requested state when a 200 response lacks blocked; its existing unit test expects this. Tracked in [#154](https://github.com/qiniu/xhub/issues/154). Normal real backend/browser behavior is verified; that stub test is not evidence of persistence.
3. Legacy organization_id/status/expand/return_full_object/include_team_keys/include_created_by_keys parameters are ignored, not documented as effective filters.
4. Legacy create/delete/detail parsing remains permissive. The supported contract is a single JSON object; it does not promise every undeclared/malformed input is rejected. Strict update malformed-input 400 and preservation are tested against the real service.

## Verification and maintenance

Generator negative tests cover missing/broken sources, unknown routes and drift in all four outputs. Go tests validate shapes, statuses, omission/null/zero and credential-field rejection. Negative TS fixtures reject missing mutation targets, batch detail, management keys and wrong response types. Real HTTP/PostgreSQL tests cover eight operations, isolation, reservations, storage failure, template inheritance, billing, block/unblock and revocation. EN/zh browser tests cover multiple personal keys sharing budget with console inference, plus create/block/unblock/expiry/rotate/delete.

```bash
npm --prefix frontend run check:api:keys
npm --prefix frontend run check:api:users
npm --prefix frontend run check:api:suppliers
node --test frontend/scripts/gen-key-contract.test.mjs frontend/scripts/gen-user-contract.test.mjs frontend/scripts/gen-supplier-contract.test.mjs scripts/test_quality_workflow.cjs
go test -race ./contracts/...
go vet ./contracts/... ./cmd/regression
bash scripts/regression.sh '^Test(KeyContract|SharedPersonalKeyBudgetHTTP|PersonalKeysIsolation|ModelLimitChain|TeamlessPersonalKeys)'
E2E_RUN_DIR="$PWD/.e2e/key-contract-final" bash scripts/e2e.sh \
  scenarios/regression/shared-key-budget.test.ts \
  scenarios/regression/key-lifecycle-en.test.ts \
  scenarios/regression/key-lifecycle-zh-CN.test.ts
```

The independent Key API contract CI job enforces drift/types, validator tests and real database regressions with logs and cleanup. E2E remains in release verification. Regenerate with `npm --prefix frontend run gen:api:keys`; check mode never repairs drift. First-time tool downloads need network; generated references work offline.

External boundaries: upstreams are controlled local HTTP services, not production vendors or identities. Common 503/409 shapes are declared but not fault-injected for every operation. Production load and concurrent deletion failures are not covered. Reverting this batch requires no database rollback or production configuration change.

## Executed verification (2026-10-24)

Four Go contract packages passed with race checks; 16 generator/workflow tests passed, with 9 workflow checks rerun after the final selector update; 138 frontend tests passed with zero failures. Final backend selection: eight passed leaves, zero failed, one external-vendor live subtest intentionally skipped in offline mode and not counted as passed. The earlier six-case contract/budget/isolation selection had zero skips. Four EN/zh E2E cases passed with zero failures/skips and both test/cleanup exit codes zero; these represent two bilingual scenarios, not all 415 project scenarios.

All three generation drift/type-negative checks, full frontend typecheck and Go vet passed. ESLint remains at 217 historical errors under the unchanged 289 budget, not zero static debt. Initial build errors exposed old non-null view declarations; types were corrected. An initial component assertion looked for an input instead of the actual plaintext pre element; correcting that assertion yielded all 138 passing. No business validation or failure gate was relaxed.

Additional commands run in frontend:

```bash
npx tsc --noEmit --incremental false
npx vitest run src/components/networking.test.ts \
  src/components/organisms/create_key_button.test.tsx \
  src/components/organisms/create_key_button.integration.test.tsx \
  src/components/templates/keyEditFormValues.test.ts \
  src/components/templates/key_info_view.test.tsx \
  'src/app/(dashboard)/hooks/keys/useSetKeyBlockedState.test.ts' \
  --reporter=json --outputFile=../.verification/key-contract/frontend-unit.json
npx eslint . --quiet --format json --output-file ../.verification/key-contract/eslint.json --pass-on-unpruned-suppressions
node scripts/check-eslint-error-budget.mjs ../.verification/key-contract/eslint.json 289
```

Local evidence: `.verification/key-contract/frontend-unit.json`, `unit-go.log`, `generator-tests.log`, `workflow-final.log`, `full-typecheck.log`, `vet.log`, `eslint-budget.log`; backend `.e2e/runs/regression-J1Uxq05D/regression.log`; browser `.e2e/key-contract-final/html/index.html`, `results.json`, `cleanup-status.json`. Raw reports stay untracked; CI reruns checks and uploads artifacts.
