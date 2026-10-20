# 联网搜索：仅 token 计费

原生 Responses 请求声明 `web_search`、`web_search_2025_08_26`、`web_search_preview` 或 `web_search_preview_2025_03_11` 时，采用**仅 token 计费的软额度**。无需供应商搜索认证配置或搜索单价；身份鉴权、模型权限和已有余额准入仍生效。

网关保留搜索工具及客户端传入的上限，不额外注入 `max_tool_calls` / `max_output_tokens`，不移除搜索，不为该请求创建硬额度预占。按上游完整实测的输入、输出及缓存 token 使用部署原有 token 价格结算；输出含上游报告的推理 token。即使上游报告搜索次数或部署配置了搜索单价，也不额外收取工具费。原始 usage 和搜索结果保留在响应及日志中。

单次请求可能超过开始时的剩余额度；并发搜索请求**不保证硬额度**。实际 token 消费影响之后的余额准入。用量缺失或矛盾时标记未定价，不猜测费用。上游搜索输出超过客户端上限时，按实际报告 token 收费，不新增 uncertain 预占。

响应头明确说明策略：

- `X-XHub-Budget-State: token_only`
- `X-XHub-Billing-Scope: tokens_only`
- `X-XHub-Budget-Guarantee: soft`
- `X-XHub-Tool-Costs: excluded`

用量价格快照的 `budget` 同步记录计费范围与额度保证。流式响应在生成前返回这些策略头。工具计量保留作上游证据，不参与收费。合法公开搜索历史可以在原生 Responses 中续轮；跨协议转换搜索历史仍不支持。

本例外不放开图片生成、代码执行和服务端工具发现等其他 hosted tools。无搜索请求仍采用原有硬额度预占；历史 uncertain 记录不会被释放或修改。

后台 regression 使用本地 HTTP 上游及独立 PostgreSQL schema；中英文浏览器测试创建模型和有限额 Key，并在结束后清理。这些测试验证网关策略，不保证上游搜索可用性、质量或遵守客户端限制。
