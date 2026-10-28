# Legacy identity operation boundaries (Phase 4 C2)

These catalog aliases have no token issuer, revocation/introspection executor, invitation consumer or JWT mapping executor. A saved configuration object was not a successful identity operation: `POST /token` wrote ordinary KV, and `POST /v3/login/exchange` did not consume a login code or create a session. They now fail explicitly instead of returning misleading HTTP 200.

## Exact scope

| Method | Path |
| --- | --- |
| `GET` | `/authorize` |
| `GET` | `/discover` |
| `GET` | `/enabled` |
| `GET` | `/invitation/info` |
| `GET` | `/jwt/key/mapping/info` |
| `GET` | `/jwt/key/mapping/list` |
| `GET` | `/onboarding/get_token` |
| `GET` | `/token/generate` |
| `POST` | `/delete` |
| `POST` | `/introspect` |
| `POST` | `/invitation/delete` |
| `POST` | `/invitation/new` |
| `POST` | `/invitation/update` |
| `POST` | `/jwt/key/mapping/delete` |
| `POST` | `/jwt/key/mapping/new` |
| `POST` | `/jwt/key/mapping/update` |
| `POST` | `/onboarding/claim_token` |
| `POST` | `/register` |
| `POST` | `/revoke` |
| `POST` | `/test/connection` |
| `POST` | `/token` |
| `POST` | `/v3/login/exchange` |

Matching uses the exact method and path, allowing one trailing slash. No broad token/auth prefix is blocked.

## Compatibility

- Existing authorization is preserved: anonymous 401, ordinary user 403; an administrator with valid JSON receives 501, `error.type=unsupported_operation`, `Cache-Control: no-store`.
- Malformed JSON and top-level JSON-RPC envelopes still return 400 first.
- No same-name KV reads/writes, input code/token echo, `Set-Cookie`, or model upstream calls. Existing KV is retained without migration; it does not represent working invitations or tokens.
- Integrations relying on the previous 200 will now fail explicitly. No replacement invitation/JWT executor is introduced.
- Dedicated `/login`, `/v2/login`, `/v3/login`, logout, `/auth/me`, password reset and `/auth/enterprise/...` handlers remain unchanged, as do the unsupported `/authorize/flow`, `/authorize/complete` and MCP OAuth handlers. Cross-worker login-code exchange is not enabled. Permission and budget rules are unchanged.

## Verification

```sh
go test ./internal/gateway/family ./internal/gateway -count=1
bash scripts/regression.sh '^Test(IdentityAliasBoundary|EnterpriseSAMLHTTPFlow|UserContractPassword|CustomKeyFailurePreservesSession|TeamlessSessionBudgetHTTP)$'
E2E_PORTABLE_CONSOLE=1 bash scripts/e2e.sh scenarios/regression/identity-alias-en.test.ts scenarios/regression/identity-alias-zh-CN.test.ts
```

The regression uses real HTTP and isolated PostgreSQL, checks all aliases with three identities, compares the entire KV snapshot, and verifies no upstream calls. Separate tests exercise signed SAML, password login, session isolation and teamless budgets. The bilingual browser flow logs in, observes explicit alias failures, signs in as an ordinary user with a 500 budget, sends an English prompt through the playground to the local upstream and sees its response. Temporary users/keys and the private schema are cleaned up.

The gateway and database are real; SAML IdP and model upstream use deterministic local fixtures. This does not certify a production IdP, mail delivery or an external OAuth provider. Regression coverage is collected by CI and both browser cases are in the release gate. The 70% threshold is unchanged. No merge or deployment is part of this change.
