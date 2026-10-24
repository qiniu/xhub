# Supplier management contract pilot

Proposal #149, interface-integrity roadmap stage 2. [OpenAPI 3.0.3](../../../contracts/suppliers/openapi.json) is the single source for the [offline API reference](supplier-api.generated.md). This pilot adds development contracts and checks only. It installs no runtime middleware, switches no handler or frontend client, and changes no database or production configuration.

## Scope and evidence

| Area | Contract and verification |
| --- | --- |
| Five operations | GET/POST `/credentials`, GET `/credentials/by_name/{credential_name}`, PATCH/DELETE `/credentials/{credential_name}`, exercised over real HTTP and PostgreSQL. |
| Permissions | Every operation checks unauthenticated 401, ordinary-user 403, and administrator success. The schema validator does not simulate authorization; the real gateway decides access. |
| Responses | Create/update/read return 200; missing detail returns 404; repeated deletion returns 200 with `deleted=0`. Error `code` must equal HTTP status. No default response accepts arbitrary statuses. |
| Failures | Invalid/trailing JSON and JSON-RPC on REST return 400. Real SQL read/write/delete failures return 500, preserve existing data, and recover after fault removal. Store-unavailable 503 has schema-unit coverage only, not a real HTTP fault test in this pilot. |
| User workflow | English/Chinese UI creation, edit, reload, independent list/detail reads, blank-key patch, deployment referencing saved credentials, real local-upstream inference and billing, delete and subsequent 404. |

This is not a whole-API schema. `by_model`, encryption migration, authentication extensions, OAuth, JSON-RPC, model creation and inference protocols are outside these five operations. Inference in E2E demonstrates that saved credentials affect the data plane; it does not claim an inference contract migration.

Path parameters cover ordinary resource names. Slashes, reserved actions (`new/list/info/update/delete/register/runs`), version segments, collection aliases and names containing `settings` are marked outside coverage in the parameter description and `x-unverified-name-cases` because the legacy generic parser treats them specially; no new runtime restriction is added. POST may still save such names: this unresolved naming boundary is documented, not silently changed or declared covered. `token` remains an ordinary supported name and is tested in E2E.

## Compatibility

- Requests are JSON objects with no newly required provider fields. Empty body/object retains existing behavior. Extensions survive, top-level `password` is ignored, and top-level `jsonrpc` is rejected by backend business validation.
- Duplicate POST replaces the record. PATCH merges and upserts missing records. Non-credential top-level fields, including `credential_info`, are replaced rather than deeply merged.
- A missing/non-string create name may generate a name; legacy `id` fallback remains. Detail includes `id`, `credential_name`, and `credential_info`. `created_at` is not falsely constrained to a timestamp because legacy callers can override it.
- Object-valued `credential_values` is omitted from creation responses; reads/updates mask sensitive strings. Other legacy JSON values are possible, so the schema preserves arbitrary JSON instead of claiming strict backend validation. Masking is not a general recursive secret filter: arbitrary extension fields are not a safe place for additional secrets.
- A credential-object patch merges provided keys. Blank, non-string or `**`-containing secret values preserve existing secrets. An omitted/null/non-object credential patch is ignored. Non-secret null/empty values overwrite existing values.
- List `data` and `credentials` contain equivalent arrays. Unsupported pagination is not advertised.
- The UI deletes without a body. Legacy object bodies can supply extra deletion IDs; batch deletion is not transactional. This pilot verifies single-resource deletion, not every possible batch combination.

## Generation and gates

```bash
npm --prefix frontend ci
npm --prefix frontend run gen:api:suppliers
npm --prefix frontend run check:api:suppliers
node --test frontend/scripts/gen-supplier-contract.test.mjs
node --test scripts/test_quality_workflow.cjs
go test -race ./contracts/suppliers
bash scripts/regression.sh '^TestSupplierContract'
E2E_RUN_DIR="$PWD/.e2e/supplier-contract" bash scripts/e2e.sh \
  scenarios/regression/catalog-persistence-en.test.ts \
  scenarios/regression/catalog-persistence-zh-CN.test.ts
```

Go types use pinned `oapi-codegen v2.5.0`; TypeScript uses lockfile-pinned `openapi-typescript`. Markdown references require no CDN or runtime. Initial generation downloads Go tooling/npm packages; offline document reading needs no network. Generated comments use standard generator templates; maintain explanations in the schema rather than editing generated files.

Permissive Go credential objects use `map[string]json.RawMessage` to preserve omitted fields, explicit nulls and unknown extensions. Default generated struct codecs otherwise collapse some omitted fields to null. OpenAPI and real HTTP validation enforce structure; Go compilation alone does not validate these objects. TypeScript retains known-field constraints, checked by positive and negative compilation fixtures. No generated type is wired into the current request path.

`check:api:suppliers` compares four artifacts without writing and compiles the TypeScript fixtures. Missing files, drift and errors fail. Generation checks operations against the route catalog; that catalog is not used as a schema. Generator tests run the actual generators and mutate each temporary output to prove rejection, without repairing drift while claiming success.

The `Supplier API contract` CI job checks generation, validator unit tests and real HTTP/PG regression, retaining evidence. Existing release CI continues to run E2E. The old whole-API `gen:api` still fails closed on incomplete schema sources; this pilot does not remove that safeguard.

Database faults are injected only into isolated schemas. Browser tests use a dedicated local upstream and verify requests and billing; neither static pages nor mocked management APIs count as E2E success. Real supplier accounts, SSO and production concurrency remain outside coverage. Merge adds development gates only; deployment remains a separate decision. Reverting the pilot requires no database rollback.
