# Real prompt and search test contracts

[简体中文](../zh-CN/real-probe-contracts.md) · [API baseline](interface-integrity-baseline.md) · [Development guide](README.md)

This batch addresses [#137](https://github.com/qiniu/xhub/issues/137) and [#138](https://github.com/qiniu/xhub/issues/138). Both tests require management access. Saving configuration does not verify it. The pages read actual saved details and require configuration edits to be saved before testing. Variables, history and search queries apply only to the test request.

## Prompt tests

`POST /prompts/test` reads a saved `prompts` record and invokes the existing Chat data plane. It retains inbound identity, model authorization, budgets, guardrails, routing, usage and billing. An internal diagnostic context bypasses response-cache reads and writes so an earlier success cannot hide an upstream outage; ordinary inference caching remains unchanged.

Save with `POST /prompts`, or update with `PATCH /prompts/{id}`:

```json
{
  "prompt_id": "example-greeting",
  "name": "Greeting",
  "model": "configured-model-id",
  "messages": [{"role": "user", "content": "Say hello to {{name}}."}],
  "config": {"temperature": 0.2, "max_tokens": 128}
}
```

Test:

```json
{
  "prompt_id": "example-greeting",
  "prompt_variables": {"name": "World"},
  "conversation_history": [{"role": "user", "content": "Use one short sentence."}],
  "stream": false
}
```

| Field/behavior | Supported contract |
| --- | --- |
| `prompt_id` | Required saved ID; missing record returns 404, database failure 500 |
| `model` | Optional explicit test override, otherwise the saved model; never choose a paid model automatically |
| `messages` / `content` | Use one saved format: a nonempty text-message array or one user-text string; templates allow system/developer/user/assistant |
| `prompt_variables` | String dictionary, simple `{{name}}` substitution only; missing variables or complex syntax return 400; inserted values are not evaluated again |
| `conversation_history` | User/assistant text appended after the rendered template; history is neither translated nor substituted |
| `config` | Saved temperature, top_p and positive integer max_tokens/max_completion_tokens; unknown fields and invalid values rejected |
| Output | Actual Chat Completion JSON, call ID and usage/billing headers; data-plane errors retain their status instead of a manufactured success message |

This batch supports non-streaming text tests. Dotprompt, version selection, `stream:true`, multimodal and tool templates are explicitly rejected. A saved `version:1` may identify the current record; request-side version selection is unsupported. Retained, unmounted legacy editors need a separate migration and cannot expect SSE from this endpoint. This does not establish support for every legacy editor capability.

Locale changes do not rewrite saved text or user input. Cancellation propagates through the existing context; charging follows data-plane cancellation and accounting rules, rather than assuming cancellation means zero cost.

## Search connection tests

`POST /search_tools/test_connection` accepts exactly one configuration source:

```json
{"search_tool_id":"saved-search-id","query":"Find the official API documentation."}
```

```json
{
  "litellm_params": {
    "search_provider": "tavily",
    "api_key": "example-test-key",
    "api_base": "https://api.tavily.com/search"
  },
  "query": "Find the official API documentation."
}
```

The page tests a saved ID. Inline parameters are temporary and never persisted. `api_base` is a complete endpoint URL; blank uses the defaults below, with no path appended. Userinfo, query and fragment are rejected. Administrators may configure internal HTTP services; ordinary users cannot access this endpoint. Empty keys, masks and unresolved environment references are not valid credentials.

| Provider | Default endpoint | Request and result |
| --- | --- | --- |
| Tavily | `https://api.tavily.com/search` | Bearer; query, max_results=3, search_depth=basic; results title/url/content |
| Perplexity | `https://api.perplexity.ai/search` | Bearer; query, max_results=3; results title/url/snippet |
| Serper | `https://google.serper.dev/search` | X-API-KEY; q, num=3; organic title/link/snippet |

An omitted query defaults to `OpenAI API documentation`; an explicitly empty query is rejected. The probe checks this basic query, not arbitrary retained provider options. Providers may charge for searches; this endpoint does not invent LLM token usage or a search cost.

Success returns `status:success`, `results_count`, normalized `results:[{title,url,snippet}]`, `test_query`, `search_provider` and `upstream_status`. An empty array is valid; missing arrays, error envelopes and invalid items are failures.

Failures use non-2xx status with `status:error`, `error:{code,type,message}`, `error_type`, and `upstream_status` when available. Invalid configuration is 400, missing record 404, storage failure 500/503. Upstream 4xx/5xx retain their HTTP status without echoing the original body. Redirects are refused and mapped to 502; network or invalid-response errors are 502, timeout 504, and cancellation 408 when a response can still be written. The total deadline is at most 10 seconds, retaining a shorter configured client timeout. Successful response bodies are limited to 1 MiB. Redirects are never followed automatically.

Search create/list/detail/update responses recursively mask secrets. Updates merge parameters: omitted, blank or masked keys preserve the stored value, while explicit new keys replace it. Nested headers follow the same rule. Arrays containing masks retain the entire saved array to avoid binding a secret to a different array position. Changing those arrays requires complete unmasked input; the new page does not edit arrays. This is response redaction, not a new database encryption scheme.

## Verification boundaries

- Unit tests cover parsing, templates, variable boundaries, result shape, error classification and masked updates.
- Backend regression uses real HTTP, isolated PostgreSQL schemas and observable local upstreams, including requests, billing, authorization/budget rejection, SQL faults, 401/403/429/5xx, timeout, cancellation, redirect and cleanup.
- Chinese/English browser flows save/read/edit configuration, perform real tests, clear stale success, reject cached diagnostic success, recover and delete resources; usage is checked independently. Management APIs are not intercepted to manufacture success.
- Before-fix cases demonstrate HTTP 200 with zero upstream requests. Initial failures and correction reports are retained.

Local fixtures do not validate commercial provider availability, enterprise reverse proxies or production credentials. No production operations, deployment or new documentation endpoint are included. Protocol references: [Tavily](https://docs.tavily.com/documentation/api-reference/endpoint/search), [Perplexity](https://docs.perplexity.ai/api-reference/search-post), [Serper](https://serper.dev/).
