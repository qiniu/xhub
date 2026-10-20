# 硬额度下的 Responses 联网搜索

硬额度不能把 `web_search` 声明一律视为免费，也不应把支持搜索的请求一律拒绝。XHub 支持显式配置的 `openai_responses_search_actions` 计费契约：保留搜索声明，在实际出站设置 `max_tool_calls`，按完整响应中的收费搜索动作结算。

仅适用于原生 Responses 的现代 `web_search`。部署管理员须确认实际供应商（包括转售渠道）的以下保证：整个 response 的输出总量遵守 `max_output_tokens`；内置工具总次数遵守 `max_tool_calls`；累计计费输入包含搜索内容且存在可信总上界；按成功 `search` 动作收费，`open_page` 和 `find_in_page` 不另外收搜索费。不能仅凭一次成功调用或模型名称推定这些保证。

模型配置示例（数值仅展示格式，不能直接用作真实供应商认证）：

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

仍需原有完整文字价格和模型输入/输出上限。`web_search_budget_input_tokens` 是所有内部轮次的累计计费输入上限，必须不小于模型输入窗；不是单窗大小，也不能用“窗口×工具次数”未经认证地推算。搜索暂不支持阶梯 token 价格、preview 搜索、其他 hosted tools。每次请求默认最多 1 次内置工具调用，可显式设置 1–16，且不能超过部署认证值。返回 `X-XHub-Max-Tool-Calls`、原有输出 cap 和预算预占响应头。

结算只读取完整终态，不累加重复流事件。总工具次数与收费搜索次数分开：搜索动作中的多个查询不重复计费，公开搜索历史也不在下一轮再次收费。未使用搜索时不收搜索费。缺失、矛盾、重复 ID、未知动作或超上限用量会保留原预占等待核对，不退款后继续假装成功结算。

`web_search_fallback=drop_optional` 仍是目标协议不支持搜索时的显式降级策略，不是搜索预算开关；本修复不通过静默删除搜索或关闭硬额度放行。

2026-10-20 的有限实测：七牛 GPT 的一次现代搜索成功且遵守本次两个 cap；这不构成完整累计输入/计费契约认证。Fenno Grok 返回的工具次数和输出量均超过请求 cap，因此不能为该连接启用此契约。未自动修改任何生产部署配置。

参考：[Responses 参数](https://developers.openai.com/api/reference/python/resources/responses/methods/create)、[搜索动作与费用](https://developers.openai.com/api/docs/guides/tools-web-search)、[xAI 工具用量](https://docs.x.ai/developers/tools/tool-usage-details)。xAI 的计费分类和轮次上限不能直接套用 OpenAI 搜索契约。

添加手工搜索价会切换至手工价格来源，因此必须同时配置完整输入/输出 token 单价及适用的缓存价格；不能只补搜索价再默认沿用目录的 token 价格。
