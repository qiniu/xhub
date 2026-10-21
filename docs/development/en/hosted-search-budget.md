# Hosted web search: token-only billing

Native Responses requests declaring `web_search`, `web_search_2025_08_26`, `web_search_preview`, or `web_search_preview_2025_03_11` use **token-only soft budgets**. No supplier certification profile or search price is required. Existing authentication, model access restrictions and available-balance admission remain active.

The gateway preserves the search tools and caller-provided limits. It does not add `max_tool_calls` or `max_output_tokens`, remove search, or create a hard-budget reservation for these requests. Complete measured input/output tokens, including reported reasoning and cache tokens, use the deployment's normal token pricing. Search/tool charges are excluded even when upstream reports them or the deployment has a search price. Raw upstream usage and search results remain in responses and logs.

A request can spend more than the balance remaining when it started; concurrent search requests do **not** have a hard spending guarantee. Completed token charges count toward subsequent admission. Missing or contradictory token usage remains unpriced rather than being estimated. Search output exceeding a caller limit is charged using actual reported tokens and does not create a new uncertain reservation.

Observable response headers:

- `X-XHub-Budget-State: token_only`
- `X-XHub-Billing-Scope: tokens_only`
- `X-XHub-Budget-Guarantee: soft`
- `X-XHub-Tool-Costs: excluded`

The usage price snapshot stores the same scope and guarantee under `budget`. Streaming clients receive the policy headers before generation. Tool usage is retained as upstream evidence, not charged. Supported public search history can be continued through native Responses; cross-protocol conversion of search history remains unsupported.

Soft budgets now apply to all model requests. The budget layer no longer rejects hosted tools using an allowlist; protocol conversion and upstream capability restrictions still apply. Existing uncertain reservations are not automatically released or modified.

Regression uses local HTTP suppliers and a private PostgreSQL schema; browser tests create and clean up a model and limited key in both supported UI languages. These prove gateway policy, not upstream search availability, quality or compliance with client-provided limits.
