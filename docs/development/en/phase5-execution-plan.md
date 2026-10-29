# Phase 5: execution-chain plan

Date: 2026-10-29. Proposal #241. The plan is accepted; P0/P1 have started. See the [baseline and first-batch evidence](phase5-execution-baseline.md) for implementation and verification status. The starting main revision is `09273d4d90f24990b55b2199284e64f11b81cd29`.

## Outcome and scope

Give parsing, configuration selection, each upstream attempt, output, history and settlement explicit owners while preserving existing URLs, credentials, protocols, authorization, budgets and billing. Keep the Go modular monolith and existing packages. Reuse RoutePlan, RouteTarget, CallNote, ForwardResponse and host interfaces before introducing types. Do not build a new general-purpose execution framework.

The fixed scope is P0–P6. New protocols, model capabilities, management UI redesign, database migrations and changes to authorization or budget rules are outside this phase. SSO and historical supplier issues remain separate. Confirmed bugs get their own evidence and tracking; unsafe behavior blocks the affected batch rather than silently expanding this refactor.

Each batch delivers reviewable code, real tests, a Chinese PR description and rollback evidence. Merge and deployment are separate decisions. This plan does not authorize automatic publishing.

## Verified starting points

- `gateway/bypass.go` matches registered native operations; some public Google calls enter the adapted execution path.
- `execution/official.go` parses the inbound body in ServeBypass and again in serveBypassCreate. The second parse error is ignored after the first parse succeeded.
- `transport/bypass_body.go` retains raw bytes, a field projection and multipart parts. Payload rewrites the model and removes gateway controls without treating a decoded map as the lossless body.
- `transport/bypass_forward.go` parses the actual outbound body for stream measurement. That body may already include model and budget changes; it is not interchangeable with the inbound projection.
- `execution/serve.go` combines identity, history, guardrails, caching, routing, retry, conversion, output and recording. Extraction must preserve their order.
- `dataplane/contract/host.go` already defines routing and observation contracts. Avoid parallel broad interfaces or duplicate logging models.

These are code observations, not proof of a user-visible incident. P0 records callers, mutable state, test mappings and gaps.

## Fixed work packages

| Package | Deliverable | Required evidence | Rollback |
| --- | --- | --- | --- |
| P0 | Entry/caller inventory, behavior and side-effect matrix, configuration read points, test mapping and performance baseline | Each scoped behavior has evidence or an explicit gap; reversible mutations prove critical assertions detect damage | Tests and documentation only |
| P1 | Pass the parsed Bypass body to creation; distinguish raw, routing and outbound measurement inputs | JSON precision/extensions, multipart bytes/order/headers, model rewrite, gateway field removal and error boundaries preserved | Revert carrier/signature changes |
| P2 | Explicit request configuration view and mutable-state ownership | Request consistency and existing hot-update, revocation, disablement and credential-rotation behavior | Revert reading adapters, not user configuration |
| P3 | Minimal common facts for one attempt using existing results | Upstream HTTP, client response commitment, protocol terminal, errors, usage and timing stay distinct | Revert result adapters without changing stored log schema |
| P4 | Extract equivalent timeout, retry and resource-release operations | No replay after response commitment, ambiguous async creation or required-parent pinning; no resource leaks | Restore one boundary at a time |
| P5 | Consolidate history/logging/settlement orchestration and remove proven-unused branches | Required history saved before terminal publication; failures remain failures; polling does not double-charge; late results cannot overwrite new ownership | Revert orchestration, never bills/history/tasks |
| P6 | Combined candidate acceptance, compatibility/coverage/cleanup reports and final checklist | Required matrix passes without hidden skips, zero matches or failed cleanup | Merge and release independently reviewed |

Dependencies are sequential: P0 → P1 → P2 → P3 → P4 → P5 → P6. Usually one PR per package; P4/P5 may split by entry path while retaining the same fixed scope. PR count is not completion percentage. No calendar estimate is promised before measurement.

## Request ownership and invariants

For a streaming Responses request followed by previous_response_id continuation: authenticate and parse; recover authorized history; apply existing budget/guardrail checks; resolve configuration and routing; recheck actual targets for every attempt; perform native forwarding or supported conversion; evaluate commitment/terminal/error facts; preserve the existing order of history persistence, output, logging, settlement and cleanup.

Native selection still depends on registered capabilities, history and governance. This refactor does not send every same-protocol request through Bypass. Bypass retains its existing guardrail/cache/task differences.

- Parsed bodies are request-local, read-only carriers. Each candidate produces a new outbound payload without corrupting the next candidate. File bytes are never converted to text.
- Only eliminate redundant parsing. Retain parsing that observes materially different outbound facts.
- A configuration view must not freeze live authorization, budgets, cooldowns, credential validity or connection ownership. Preserve existing read/check times; changes to hot-update semantics require a separate decision.
- Separate request outcome from attempt outcome. Receiving upstream headers, committing client headers, emitting body data, reaching a successful terminal and cancellation/failure are distinct facts.
- Responses ownership, Messages signed history and asynchronous task contexts retain separate ownership, expiry and connection constraints.
- Transport never decides prices or cross-target fallback. Protocol adapters do not write identity data or settle charges. Shared finalization cannot assume success.
- Preserve unknown native fields, precise numbers and required signatures; rewrite only registered fields. Supported cross-protocol mapping may explicitly reject unsupported content rather than promising arbitrary lossless conversion.
- Preserve raw SSE ordering/framing on native paths and semantic terminals on adapted paths. HTTP 200 and EOF are not completion proofs.
- Keep disable_fallbacks semantics, task settlement keys, pricing-blocked states and measured-versus-estimated usage distinctions.
- Keep the 70% patch gate and real regression coverage upload. Do not extend timeouts, hide initial failures or expand exclusions to pass CI.

## Verification and delivery

The matrix covers JSON/multipart boundaries; registered standard/native protocol operations; synchronous and streaming success; pre-output, headers-only and midstream failures; missing terminal and terminal-without-EOF; cancellation; tool/image/history ownership and expiry; retries/fallback/required parent; hot updates and cache; usage conflicts, unavailable usage, polling and persistence failures.

Use unit tests for deterministic logic, real HTTP/PostgreSQL/Redis regression for service behavior, and Playwright → real gateway → controlled HTTP upstream → isolated database for user-visible flows. Local upstream fixtures do not replace gateway, accounting or authorization code and do not prove external supplier behavior. Cover relevant English/Chinese flows, keep examples in English and preserve user input.

Pure refactors establish passing behavior first, then use reversible local mutations to prove assertions fail. Confirmed bug fixes retain before-failure and after-success evidence. Never inject failures in production or shared data. Dedicated schemas, Redis namespaces and test resources must be cleaned after success and failure. Required skips, dependency failures, zero matches and cleanup failures block acceptance.

Planned commands, not claimed results:

```bash
XHUB_REGRESSION_STRICT=1 go test -race ./internal/dataplane/... ./internal/provider/... -count=1
XHUB_REGRESSION_COVERPROFILE="$PWD/.verification/phase5/regression.out" bash scripts/regression.sh
bash scripts/e2e.sh
```

Each report records revision, exact commands, first failures and reruns, top-level/leaf counts, skips, coverage, cleanup and external boundaries. P0 measures parsing; configuration and lifecycle batches add concurrent workload, first-byte latency, memory and cancellation-release measurements. Set budgets from repeated baseline observations, not invented improvement percentages or line counts.

Follow the LiteLLM reference direction in the [integrity roadmap](interface-integrity-roadmap.md): separate lifecycle, routing and protocol responsibilities, and declare native capabilities. Pin a source revision before borrowing implementation details. This plan does not claim to have verified current LiteLLM internals or adopt its billing rules or Python service.

## Release and completion

Compare only pure parsing/routing results in shadow mode, never duplicate paid upstream calls. Existing native Responses rollout settings must not be assumed to cover the entire chain. Add a minimal server-side switch only where a batch needs runtime selection, with explicit verification; avoid permanent dual implementations for pure helpers.

After separate release authorization: combined staging acceptance → controlled-account GPT synchronous/stream/tool/history validation → billing/log audit → rollout decision. Include continuation of pre-upgrade history. Define observation windows and thresholds from baseline data. Roll back code/image/configuration selection, never accumulated bills, history or tasks.

Track audit state, test evidence, commit, CI, merge and deployment separately. Development completion requires all seven packages and combined acceptance; deployed/external verification are separate milestones. Store per-batch evidence under `.verification/phase5/P<n>/` and provide committed reproducible tests and shared CI artifacts.
