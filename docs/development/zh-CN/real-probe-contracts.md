# 提示词和搜索测试的真实执行契约

[English](../en/real-probe-contracts.md) · [接口基线](interface-integrity-baseline.md) · [开发指南](README.md)

本批处理 [#137](https://github.com/qiniu/xhub/issues/137)、[#138](https://github.com/qiniu/xhub/issues/138)。两种测试都要求管理身份；保存配置本身不代表探测成功。当前页面从真实详情读取配置，编辑后必须保存，测试结果只对应这份已保存配置。变量、历史及搜索查询仅用于本次调用。

## 提示词测试

`POST /prompts/test` 读取 `prompts` 目录中的记录，调用既有 Chat 数据面。沿用入站身份、模型权限、预算、护栏、路由、用量和费用记录；内部诊断上下文跳过响应缓存的读取及写入，防止旧成功掩盖上游故障。普通推理缓存不受影响。

保存示例（`POST /prompts`，已有记录使用 `PATCH /prompts/{id}`）：

```json
{
  "prompt_id": "example-greeting",
  "name": "Greeting",
  "model": "configured-model-id",
  "messages": [{"role": "user", "content": "Say hello to {{name}}."}],
  "config": {"temperature": 0.2, "max_tokens": 128}
}
```

测试示例：

```json
{
  "prompt_id": "example-greeting",
  "prompt_variables": {"name": "World"},
  "conversation_history": [{"role": "user", "content": "Use one short sentence."}],
  "stream": false
}
```

| 字段/行为 | 本批契约 |
| --- | --- |
| `prompt_id` | 必填，读取已保存对象；不存在返回 404，数据库故障返回 500 |
| `model` | 可在测试请求显式覆盖，否则取保存记录；必须明确型号，不自动选付费模型 |
| `messages` / `content` | 保存记录使用其一：非空文本消息数组，或一段 user 文本；模板支持 system/developer/user/assistant |
| `prompt_variables` | 字符串字典；仅支持简单 `{{name}}`，缺变量或复杂语法返回 400；插入值不再次当作模板执行 |
| `conversation_history` | 仅 user/assistant 纯文本，追加到渲染后的模板；历史原文不替换、不翻译 |
| `config` | 保存记录可含 temperature、top_p、正整数 max_tokens/max_completion_tokens；未知字段或无效值拒绝 |
| 输出 | 实际 Chat Completion JSON、关联调用 ID、用量/费用头；失败沿用数据面状态和错误，不合成成功文案 |

本批只支持非流式文本测试。Dotprompt、版本选择、`stream:true`、多模态或工具模板明确拒绝，不能据此宣称所有旧编辑器能力已经实现。保存的 `version:1` 可作为当前记录标识；测试请求中的版本选择不支持。保留的未挂载旧编辑器需要另行迁移，不能发送 SSE 请求后期待本入口返回 SSE。

已保存普通文本模板的内容和输入语言不会被界面切换语言所改写。调用取消沿用原上下文，实际用量遵循数据面的取消与结算规则，不承诺取消等于免费。

## 搜索连接测试

`POST /search_tools/test_connection` 支持二选一：

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

界面使用已保存 ID；临时配置只用于本次请求，不写数据库。`api_base` 是完整搜索端点 URL，留空采用下表默认值，不自动追加路径；拒绝 userinfo、query、fragment。允许管理员配置内部 HTTP 服务，不把该入口开放给普通用户。环境变量占位符、空 Key 或掩码不当作真实凭据发送。

| 供应商 | 默认端点 | 请求及结果 |
| --- | --- | --- |
| Tavily | `https://api.tavily.com/search` | Bearer；query、max_results=3、search_depth=basic；读取 results 的 title/url/content |
| Perplexity | `https://api.perplexity.ai/search` | Bearer；query、max_results=3；读取 results 的 title/url/snippet |
| Serper | `https://google.serper.dev/search` | X-API-KEY；q、num=3；读取 organic 的 title/link/snippet |

查询省略时使用英文 `OpenAI API documentation`，显式空查询拒绝。探测只验证上述基础查询，不执行保留配置中的任意供应商扩展字段。搜索供应商可能对请求收费；本入口不把搜索请求伪装成 LLM token 用量或臆造费用。

成功返回 `status:success`、`results_count`、规范化 `results:[{title,url,snippet}]`、`test_query`、`search_provider` 和 `upstream_status`。空结果数组是有效成功；缺少数组、错误对象、无效条目不是成功。

失败返回非 2xx，包含 `status:error`、`error:{code,type,message}`、`error_type`，已收到上游响应时附 `upstream_status`。配置错误 400、缺对象 404、存储故障 500/503；上游 4xx/5xx 保留 HTTP 状态但不回显原正文。重定向拒绝并按 502 返回；网络故障或坏响应 502，超时 504，客户端取消在仍可写回时按 408 处理。连接总时限最多 10 秒（沿用更短客户端限制），成功响应最多 1 MiB，不自动跟随重定向。

搜索配置创建、列表、详情和更新响应都递归遮盖秘密。编辑参数逐字段合并，省略、空白或回传掩码的 Key 保留原值；明确输入新 Key 才替换。嵌套 headers 同样处理；含掩码的数组整体保留，避免按数组位置错误关联秘密。若要修改这类数组，需提交完整未掩码配置；本批页面不提供数组编辑。此处是响应脱敏，不是新增数据库加密方案。

## 验证与边界

- 纯函数测试检查解析、模板、变量、输入边界、结果形状、错误类型和脱敏合并。
- 后台 regression 使用真实 HTTP、独立 PostgreSQL schema 和可观察本地上游；覆盖实际请求、费用、权限/预算阻断、存储故障、401/403/429/5xx、超时、取消、重定向和资源清理。
- 浏览器 E2E 验证中英文页面的保存/读回/编辑、真实执行、旧成功结果清除、缓存不掩盖故障、恢复、删除及费用记录；不拦截管理 API 制造成功。
- 修复前反例证明旧实现虽然返回 200，但上游请求数为 0；首次失败与修正报告保留。

本地协议夹具不能证明真实付费供应商、企业反向代理或生产凭据可用。本批没有部署、运行生产请求或启用新文档路由。公开协议参考：[Tavily](https://docs.tavily.com/documentation/api-reference/endpoint/search)、[Perplexity](https://docs.perplexity.ai/api-reference/search-post)、[Serper](https://serper.dev/)。
