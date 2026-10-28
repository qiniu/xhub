# Service auxiliary capability boundaries (stage 4 C1)

Related: [#223](https://github.com/qiniu/xhub/issues/223), [closure audit #186](https://github.com/qiniu/xhub/pull/186). This change rejects 26 unavailable auxiliary operations; it does not implement a policy executor, version store, attachment graph, or credential migration.

Previously `POST /policies/test-pipeline` persisted a configuration named `test-pipeline` and returned 200 without executing a pipeline. The exact operations below now return 501 with `error.type=unsupported_operation` and `Cache-Control: no-store`, after existing management authentication, JSON and JSON-RPC validation. They neither access public KV nor call upstreams or echo input.

Anonymous 401, ordinary-user 403 and invalid JSON 400 remain. Basic policy/prompt/credential CRUD, `/policy/list`, `/policy/info/{policy_name}`, prompt execution, local guards, model calls and resource names such as `status`, `versions`, and `migrate-encryption` remain supported as before. No changes to authorization, budget algorithms, schemas or the 70% gate.

The current policy page only mounts the basic configuration form. The old simulator component is not a current page entry; API checks are not presented as simulator UI testing. Clients depending on these false-success operations now receive explicit 501 errors. Unavailable operations are not added to Swagger's supported contracts.

## Exact scope

- `DELETE /policies/attachments/{attachment_id}`
- `DELETE /policies/name/{policy_name}/all-versions`
- `GET /credentials/by_model/{model_id}`
- `GET /credentials/migrate-encryption/check`
- `GET /policies/attachments/list`
- `GET /policies/attachments/{attachment_id}`
- `GET /policies/compare`
- `GET /policies/name/{policy_name}/versions`
- `GET /policies/usage/overview`
- `GET /policies/{policy_id}/resolved-guardrails`
- `GET /policy/templates`
- `GET /prompts/{prompt_id}/versions`
- `POST /credentials/migrate-encryption`
- `POST /policies/attachments`
- `POST /policies/attachments/estimate-impact`
- `POST /policies/name/{policy_name}/versions`
- `POST /policies/resolve`
- `POST /policies/test-pipeline`
- `POST /policy/templates/enrich`
- `POST /policy/templates/enrich/stream`
- `POST /policy/templates/suggest`
- `POST /policy/templates/test`
- `POST /policy/test`
- `POST /policy/validate`
- `POST /utils/test_policies_and_guardrails`
- `PUT /policies/{policy_id}/status`

## Evidence boundaries

Real HTTP/PostgreSQL regression failed on all 26 operations before the fix. Collision fixtures verify rejection leaves all KV unchanged. Units cover methods, templates, trailing slashes, empty segments, extra paths and ordinary resource IDs. Regression retains authentication/JSON, CRUD, real local inference and exhausted-budget tests. Bilingual browser tests create/reload a policy, verify API errors and preserved configuration with zero upstream calls, then complete an actual playground conversation. Independent fixtures remove policies and the runner drops schemas. Local fixtures do not prove production model, SSO or reverse-proxy health. No merge or deployment. Commands and reports are recorded in the PR.
