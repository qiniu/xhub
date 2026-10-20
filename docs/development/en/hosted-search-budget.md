# Responses web search with hard budgets

XHub can retain modern `web_search` declarations under the explicitly configured `openai_responses_search_actions` budget contract. It sets an upstream `max_tool_calls`, reserves search and token costs, and settles measured search actions from the final response.

Enable this only for a verified native Responses deployment. The actual supplier, including any reseller, must guarantee response-wide `max_output_tokens`, a total built-in call limit, a cumulative billed-input bound including search content, and pricing per successful `search` action (not separate charges for `open_page` or `find_in_page`). A model name or one successful request does not establish these guarantees.

Example structure; these illustrative values are not supplier certification:

```json
{
  "litellm_params": {"search_context_cost_per_query": 0.01},
  "model_info": {
    "web_search_support": "supported",
    "web_search_budget_profile": "openai_responses_search_actions",
    "web_search_budget_source": "Supplier contract URL and revision covering limits, cumulative input and search pricing",
    "web_search_budget_max_calls": 1,
    "web_search_budget_input_tokens": 262144
  }
}
```

Complete token prices and model limits remain required. The input bound covers cumulative billed input over all internal turns, must cover the model input window, and must not be guessed by multiplying the window by call count. Tiered token rates, preview search and other hosted tools are not supported by this contract. The default request cap is one call; explicit caps must be integers from 1 through 16 and cannot exceed the verified deployment cap. `X-XHub-Max-Tool-Calls` accompanies the existing output and reservation headers.

Settlement distinguishes total tool calls from charged search actions. Multiple queries in one action and previous search history do not produce duplicate search charges. Unused search costs zero. Missing facts, conflicting counts, duplicate IDs, unknown actions or exceeded bounds preserve the reservation for reconciliation. Streaming intermediate events are not accumulated as extra calls.

`web_search_fallback=drop_optional` remains an explicit downgrade for transports without search support; it does not enable search budgeting. This change neither silently removes search nor disables hard budgets.

Limited tests on 2026-10-20 found one Qiniu GPT search respected the requested caps; that does not certify its cumulative input or pricing contract. Fenno Grok exceeded both requested tool-call and output caps, so this contract must not be enabled there. No production deployment was automatically changed.

References: [Responses parameters](https://developers.openai.com/api/reference/python/resources/responses/methods/create), [search actions](https://developers.openai.com/api/docs/guides/tools-web-search), [xAI tool usage](https://docs.x.ai/developers/tools/tool-usage-details). xAI billing categories and turn limits are not equivalent to this OpenAI contract.

Adding a manual search price selects manual pricing: configure complete input/output token rates and applicable cache rates together, rather than assuming catalog token prices will remain active.
