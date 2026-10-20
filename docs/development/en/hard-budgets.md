# API key hard budgets

When a key, user, team, organization, or project has a budget limit, XHub reserves an upper bound for each actual upstream attempt in a PostgreSQL transaction before sending it. Gateway instances share these durable reservations. Retries need separate reservations. Every applicable scope must have enough available budget.

## Output caps and available balance

A budgeted request without an output limit defaults to **4096 tokens**, reduced further by a smaller trusted deployment output limit. Valid explicit limits are retained. Conflicting limits or values above the deployment limit are rejected. The cap is written into the final upstream JSON after history restoration, guardrails, and protocol conversion, including native paths.

| Upstream protocol | Output field |
| --- | --- |
| Responses | `max_output_tokens` |
| Messages | `max_tokens` |
| Chat Completions | A trusted declaration of `max_completion_tokens`, or supported `max_tokens` |

When Chat supports both fields, XHub uses `max_completion_tokens`, which includes hidden reasoning. A deployment declaring only legacy `max_tokens` rejects explicit reasoning by default. When the supplier explicitly guarantees that this field includes hidden reasoning, an administrator may set boolean `model_info.max_tokens_includes_reasoning: true` and a nonempty `model_info.max_tokens_semantics_source` documentation reference. A parameter name, provider name, or one successful response does not establish that semantic contract; string `"true"` is not accepted. Messages `thinking.budget_tokens` must be below the total output cap. Reaching the cap can produce an incomplete or length-limited result; clients must handle that outcome.

Input is reserved against the **entire trusted input window**, without estimating tokens from character counts. Even a short prompt may therefore need a substantial available balance. A reservation is not recorded spend. Complete, reliable measured usage is charged at the frozen rates for that attempt, and the remaining reservation is released in the same transaction.

Admission headers expose `X-XHub-Max-Output-Tokens`, `X-XHub-Budget-Input-Bound`, `X-XHub-Budget-Reserved`, `X-XHub-Budget-Reservation-Id`, and `X-XHub-Budget-State`. Headers cannot track settlement after streaming starts; read the key details for the final state.

## Trusted deployment metadata

The input bound is read from `max_input_tokens` or `context_length`, checking deployment `model_info`, then `litellm_params`, then the matching model catalog. Chat also needs trusted `supported_parameters`. A model name or protocol compatibility label does not establish these capabilities.

The following is a **fictional configuration example**, not actual GPT, DeepSeek, Grok, or Claude specifications or pricing. Administrators must use verified provider limits and prices. Writing a smaller input bound does not make the provider enforce it. Keep requests blocked when reliable information is unavailable.

```json
{
  "model_name": "budgeted-text-example",
  "litellm_params": {
    "model": "provider-text-model",
    "input_cost_per_token": 0.000001,
    "output_cost_per_token": 0.000002,
    "cache_read_input_token_cost": 0.0000001
  },
  "model_info": {
    "pricing_source": "manual",
    "max_input_tokens": 32768,
    "max_output_tokens": 8192,
    "supported_parameters": ["max_completion_tokens"]
  }
}
```

Without cache writes, this example reserves at least `32768 × 0.000001 + 4096 × 0.000002 = 0.04096 USD` for a 4096-token output cap. This is not the final bill. Flat token prices are USD per token. Manual pricing must be complete; missing fields are not silently filled from the catalog. In catalog mode, null price fields mean a cleared override and use the complete catalog contract; non-null invalid overrides still reject. Cache reads, writes, and one-hour writes need their respective rates. Incomplete or inconsistent tiers, unsupported conditions, and unknown billing dimensions cannot be treated as free.

This version supports bounded text generation, client tool round trips, and user images charged at uniform input-token rates. Images reserve the trusted full input window without estimating pixel tokens; `input_images_included` is frozen in the price snapshot. Image-token details must be subsets of total input tokens and are not charged twice; contradictory or out-of-bound usage retains an unresolved reservation. Native Messages also supports images returned by client tools such as Read; bridging tool-result images to other protocols remains explicitly unsupported. Separate image fees, image generation, audio/video, server tools/search, remote history references, unknown billing dimensions, and uncertified fields are explicitly rejected under strict budgets. Requests with no limit anywhere in their hierarchy keep the existing compatibility path. Removing limits is not evidence that hard budgets work.

## Context tier pricing

The catalog's `pricing_rules_v2` contains the original tier contract. Legacy flat `rates` / `input_cost_per_token` can be derived from only the first tier and do not describe the entire context window. XHub freezes all reachable tiers, their original input/output ranges, and explicit USD prices when an attempt starts. Reservation uses the worst reachable rate for each billing dimension; settlement selects a frozen tier from measured total input and output. Separate cache reads and writes count toward input context, so a cache hit does not turn a long context into a short-context tier. Catalog changes do not change the price of an attempt already sent upstream.

Tiers must cover the declared input window and output cap completely, without gaps or overlapping interiors. Every price needs an explicit `unit_price_usd`, positive integer `unit_size`, and matching `unit_name`. Explicit zero USD is valid; missing USD is not zero, and a displayed CNY price is never converted using a guessed exchange rate. Conditional prices, unknown billing dimensions, and mixed cache TTLs remain unsupported.

The current feed does not define which adjacent range owns a shared threshold. For example, measured input exactly `272000` matches both `[0, 272000]` and `[272000, 99999999]`. Reservation covers both possibilities. Settlement is safe when the complete rates agree; otherwise XHub retains the reservation as `uncertain` with reason `ambiguous catalog pricing boundary`, without claiming a reliable charge. An administrator must reconcile this against an explicit supplier threshold contract. Do not reduce the declared input window or copy first-tier rates into manual pricing to bypass verification.

## Errors and unresolved reservations

| HTTP / error code | Meaning and action |
| --- | --- |
| `400 invalid_request` | Invalid cap or request parameters; correct the request. |
| `400 unsupported_budget` | No provable cost bound; supply trusted metadata or use supported capabilities. |
| `429 budget_reservation_exceeded` | Available balance cannot cover the bound; reduce a valid output cap or adjust the budget through administration. |
| `503 quota_unavailable` | Quota storage is unavailable or old billing queues could not be drained; restore storage and reconcile the queues. |

Timeouts, cancellation, truncated streams, missing/conflicting/invalid usage, and usage above declared limits do not prove that the provider charged nothing. Submitted attempts retain an unresolved reservation. They are not automatically refunded on timeout, and unknown cost is not reported as reliably zero. If a provider violates its declared bound, XHub preserves the reservation and evidence; it cannot control what the provider charges its own account.

A Responses result with `status=incomplete` and `incomplete_details.reason=max_output_tokens` is an explicit output-cap terminal. Native bypass preserves this status and the original output. Complete, reliable usage in that terminal is charged at the measured cost and the remaining reservation is released, even when the cap truncates a tool-argument string. Clients must not execute incomplete arguments. A complete Responses terminal does not require the upstream HTTP connection to close; missing or unreliable terminal usage still retains an unresolved reservation.

Authorized key details expose `budget_pending` and `budget_reservations`, with `reserved`, `submitted`, `settling`, and `uncertain` states. Attempt IDs and reasons support reconciliation with upstream bills. There is no one-click release or automatic TTL cleanup. For unresolved attempts, stop affected calls, preserve snapshots and upstream bills, and use an operational recovery procedure reviewed for those records. Do not bulk-delete reservations.

If usage is complete but the billing transaction fails, an independent `usage_pending` snapshot supports idempotent replay. The reservation is released only after the actual billing transaction commits. Whole-PostgreSQL unavailability remains a persistence limitation tracked by Issue #35; existing reservations stay conservative and unverified usage is not considered settled.

## Upgrade and configuration changes

1. Stop new affected calls, wait for in-flight attempts, and reconcile/drain old Redis billing entries and PostgreSQL `usage_pending`. Flushing Redis does not settle bills.
2. Upgrade every gateway node to the reservation implementation. Mixed deployment with old writers cannot provide this guarantee.
3. Verify model bounds, output-cap support, and complete rates before enabling limits. Changing an unlimited scope to a limited one also requires draining in-flight calls; earlier unlimited attempts cannot acquire a cost bound retroactively.
4. Test a dedicated limited key for default caps, concurrent rejection, measured settlement, and key details before restoring traffic.

Changes between existing finite limits must cover both settled spend and reservations. For organizations with fixed budget periods, old-period attempts remain available for reconciliation but do not occupy the organization's new period. Other scopes, including users and keys, retain their own budget rules.
