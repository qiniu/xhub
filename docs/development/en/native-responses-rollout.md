# Native Responses rollout and rollback

Implements the first stage of Proposal #132 for the standard Responses endpoint. Chat, Messages and existing bypass endpoints keep their current paths. The feature is disabled by default; deploying the version does not migrate users.

## Controls

One account policy controls rollout. There is no deployment-specific switch or observation mode. Same-protocol Responses uses native handling; cross-protocol requests use the existing bridge, based on the deployment's actual transport rather than the model name.

```yaml
general_settings:
  native_responses:
    enabled: false
    all_users: false
    user_ids: []
```

Administrators update `POST /config/update`. Verify the actual XHub user ID for the pilot account in user management (not the alias or LDAP ID), then allow only that account:

```json
{"general_settings":{"native_responses":{"enabled":true,"all_users":false,"user_ids":["VERIFIED_XHUB_USER_ID"]}}}
```

UI sessions and personal keys use their authenticated owner, never a client-supplied identity. Other users retain the legacy path. After validation, explicitly enable all users:

```json
{"general_settings":{"native_responses":{"enabled":true,"all_users":true,"user_ids":[]}}}
```

An empty allowlist never implies full rollout. `all_users` must be boolean true and `enabled` must also be true. For eligible accounts, the selected `openai-responses` transport uses native handling; other protocols still use the existing bridge. Gateway controls remain active; this is not byte-for-byte forwarding. No management UI or hard-coded production identity is added.

Database settings override YAML; storage read failures close the gate. When native Responses transports exist, each standard Responses request reads the current policy. Changes affect subsequent requests, not in-flight execution. This policy does not change `/bypass/openai/v1/responses`, Chat or Claude endpoints. Bypass retains its protocol and history rules, skips the standard pre-call text guardrail chain, and still enforces authentication, budgets and logging.

## Boundaries

Native envelope validation and native JSON/SSE terminal observation no longer require the Chat projection to represent every extension. Core malformed envelopes, unsupported background lifecycle and invalid terminals still fail. Model mapping, upstream authentication, `store=false` and gateway-only field removal remain unchanged.

Authentication, budget, concurrency, search policy, guardrails, logs and history ownership remain shared. With an active pre-call guardrail, content the existing projection cannot understand fails closed with a policy incompatibility error. Known content still undergoes blocking/redaction. Broader guardrail support requires separate validation.

Each candidate independently selects execution. Non-allowlisted users and cross-protocol candidates retain legacy parsing; native extensions cannot be silently dropped. Messages signature-reference history keeps its existing bridge. Existing standard-endpoint portable-history behavior, authorized server history, tenant ownership and deployment affinity are preserved. This does not make encrypted histories portable across providers.

`X-XHub-Responses-Path: native|legacy` and call-ID logs identify selected execution (early rejections may lack this header). Invalid native terminals or stream failures after a successful upstream HTTP response do not trigger a second generation or a retry through the legacy executor. Explicit upstream HTTP rejections still use existing routing policies. Network errors, timeouts and truncated bodies after submission do not automatically replay generation because non-execution cannot be established.

## Rollback

Start with a test user and verify JSON/SSE, tool images, compacted history, guardrails, budgets, logs and continuation before expanding the allowlist. Define error/latency thresholds against a baseline before rollout. Stop immediately for authorization, duplicate billing or history-ownership failures. There is no automatic full rollout.

Send this to the administrative `/config/update` endpoint to disable new requests:

```json
{"general_settings":{"native_responses":{"enabled":false}}}
```

Do not delete the setting to disable it: deletion restores the YAML value. Do not delete history or billing data. In-flight requests finish on their original executor; binary rollback/restarts can interrupt them and require separate storage-compatibility verification.

Fields supported only by the new executor may be rejected again after rollback. History is retained, but universal continuation cannot be promised. Previously supported sessions must continue; never remove tool/image history to disguise incompatibility.

## Validation boundary

Unit tests cover envelopes, terminals, controls, candidate selection and guardrails. Backend regression uses real HTTP and isolated PostgreSQL. English/Chinese browser E2E covers sending through the playground, control changes, logs and continuation. Local upstream fixtures cannot certify a real supplier's extension support; vendor acceptance requires separately authorized test accounts. WebSockets/background jobs and replacing protocol libraries or adding Python services are outside this stage.
