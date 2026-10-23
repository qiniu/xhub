# Native Responses rollout and rollback

Implements the first stage of Proposal #132 for the standard Responses endpoint. Chat, Messages and existing bypass endpoints keep their current paths. The feature is disabled by default; deploying the version does not migrate users.

## Controls

All three conditions must hold: `general_settings.native_responses.enabled` is boolean `true`; the authenticated user ID is in `user_ids`; and the selected deployment has `model_info.native_responses_mode: enabled` with an actual `openai-responses` transport. Sessions and keys use their authenticated owner, never a client-supplied identity.

```yaml
general_settings:
  native_responses:
    enabled: false
    user_ids: []
```

Administrators can update the policy through `POST /config/update`:

```json
{"general_settings":{"native_responses":{"enabled":true,"user_ids":["test-user-id"]}}}
```

Use the existing `/model/update` API to set `model_info.native_responses_mode` to `legacy` (default), `observe` or `enabled`. The last two require a native Responses transport. Database settings override YAML. Missing/invalid switches, empty allowlists and storage read failures do not enable the feature. With no configured rollout deployments, no extra configuration query is made. Otherwise each Responses request takes one policy snapshot; changes do not switch in-flight execution.

`observe` compares local parser acceptance for allowlisted users and deployments whose public model name directly matches the request. It retains legacy execution, sends no shadow upstream request and writes no additional history/bill. Logs contain only call ID and acceptance booleans. Group aliases without a direct match are not observed. This is not a complete routing/performance comparison system.

## Boundaries

Native envelope validation and native JSON/SSE terminal observation no longer require the Chat projection to represent every extension. Core malformed envelopes, unsupported background lifecycle and invalid terminals still fail. Model mapping, upstream authentication, `store=false` and gateway-only field removal remain unchanged.

Authentication, budget, concurrency, search policy, guardrails, logs and history ownership remain shared. With an active pre-call guardrail, content the existing projection cannot understand fails closed with a policy incompatibility error. Known content still undergoes blocking/redaction. Broader guardrail support requires separate validation.

Each candidate independently selects execution. Non-opted-in deployments and cross-protocol candidates retain legacy parsing; native extensions cannot be silently dropped. Messages signature-reference history keeps its existing bridge. Existing standard-endpoint portable-history behavior, authorized server history, tenant ownership and deployment affinity are preserved. This does not make encrypted histories portable across providers.

`X-XHub-Responses-Path: native|legacy` and call-ID logs identify selected execution (early rejections may lack this header). Invalid native terminals or stream failures after a successful upstream HTTP response do not trigger a second generation or a retry through the legacy executor. Explicit upstream HTTP rejections still use existing routing policies. Network errors, timeouts and truncated bodies after submission do not automatically replay generation because non-execution cannot be established.

## Rollback

Start with a test user/deployment and verify JSON/SSE, tool images, compacted history, guardrails, budgets, logs and continuation before expanding the allowlist. Define error/latency thresholds against a baseline before rollout. Stop immediately for authorization, duplicate billing or history-ownership failures. There is no automatic full rollout.

Send this to the administrative `/config/update` endpoint to disable new requests:

```json
{"general_settings":{"native_responses":{"enabled":false}}}
```

Do not delete the setting to disable it: deletion restores the YAML value. Do not delete history or billing data. In-flight requests finish on their original executor; binary rollback/restarts can interrupt them and require separate storage-compatibility verification.

Fields supported only by the new executor may be rejected again after rollback. History is retained, but universal continuation cannot be promised. Previously supported sessions must continue; never remove tool/image history to disguise incompatibility.

## Validation boundary

Unit tests cover envelopes, terminals, controls, candidate selection and guardrails. Backend regression uses real HTTP and isolated PostgreSQL. English/Chinese browser E2E covers sending through the playground, control changes, logs and continuation. Local upstream fixtures cannot certify a real supplier's extension support; vendor acceptance requires separately authorized test accounts. WebSockets/background jobs and replacing protocol libraries or adding Python services are outside this stage.
