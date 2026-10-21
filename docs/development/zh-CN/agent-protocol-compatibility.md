# Agent 协议兼容对照与验收（Issue #9）

有限额 API Key 还受[硬额度与输出上限](hard-budgets.md)约束：协议可转换不代表本次费用有可信上界，请同时检查部署计价配置。

本轮范围是 GPT、Grok、DeepSeek 的文本、函数工具、公开/加密推理及 Responses / Chat Completions；用户图片输入的协议支持见下方识图说明；音视频、图片生成及其他厂商本轮不认证。模型名称不是协议能力声明，供应商目录中的模型也不等于当前 Key 有可用渠道。

## LiteLLM 的做法与本次复用

参考官方 LiteLLM `e889d9d6533ac30e776aa3b3780f99fa73e07460`，并对照公司旧库 `f09e043`（基于 v1.95.40）。Python 实现没有直接成为 Go 服务依赖；本次手工移植相关映射规则，保留 MIT 授权，见 [来源与授权](../../../third_party/litellm/NOTICE.md)。这不意味着 XHub 自动拥有 LiteLLM 的全部适配器。

| 处理点 | LiteLLM 参考实现 | XHub 本轮处理 |
| --- | --- | --- |
| 原生与桥接分层 | `responses/main.py` 优先原生配置，否则进入 Completion 桥接 | 在现有统一执行链内区分同协议保留与跨协议投影；复用部署协议登记、HTTP 构造、SDK SSE 解码器 |
| 原生 Responses | OpenAI / xAI 的 Responses 适配器 | 保留 `reasoning`、`include`、`text`、缓存键、输出项、密文和原生 SSE 扩展字段；仅公开模型名被重写 |
| 跨协议参数 | `litellm_completion_transformation/transformation.py` | `reasoning.effort` ↔ `reasoning_effort`，`text.format` ↔ `response_format`，保留函数调用 ID / JSON 参数及公开推理文本 |
| DeepSeek 工具续轮 | `llms/deepseek/chat/transformation.py` 保留 `reasoning_content` | 原生 Chat 全量保留；Chat ↔ Responses 用 `summary_text` 表达公开推理并在工具续轮还原，不伪造空格推理 |
| 未知或无法表达的能力 | 支持参数表及可选 `drop_params` | 同协议扩展交上游验证；跨协议未实现的能力在选路前明确拒绝，不静默删除 |
| 状态与治理 | 适配器之外的 Proxy / Router 链 | 保留现有身份、额度、护栏、路由、日志和计费；没有把标准入口改接绕过统一护栏的 bypass 链 |

当前 DeepSeek 官方协议已比所参考的旧适配器丰富。例如新版文档提供原生 Responses 和 `low/high/max` effort 映射；旧 LiteLLM DeepSeek 代码仍有将 effort 归为 thinking 开/关的逻辑。本轮保留原生值，由已配置供应商验证，不能为了“照搬成熟代码”抹掉新协议能力。

本轮进一步对照官方 `35970c3eacb9f4f8746af215586f23f3a2a17941`，移植 namespace/custom 工具双向映射、可选推理提示和 usage 必需明细补齐，并修复 Messages 流式末帧遗漏输入 Token。Codex 的 `additional_tools` 内联声明、公开推理 `context` 范围投影是 XHub 的补充实现。`current_turn` 保留最后一条用户消息后的公开推理，`auto/all_turns` 保留可用完整历史；不解密或制造原厂密文。客户端跟踪元数据只作格式校验，不替代鉴权身份。

## 已实现的边界

| 能力 | Responses → 原生 Responses | Chat → 原生 Chat | Responses ↔ Chat |
| --- | --- | --- | --- |
| 文本、函数工具、工具结果 | 支持 | 支持 | 支持 |
| JSON / SSE 与实测 usage | 支持，保留原生事件 | 支持，保留 `reasoning_content` 增量 | 转换共享文本/工具/公开推理事件 |
| 推理 effort | 原样保留，合法值由模型决定 | 原样保留 | 映射字段；不会替模型接受不支持的值 |
| 推理摘要 / 加密项 | 原生保留 | 公开推理保留 | 公开文本可转换；Responses 输出的密文通过 Chat `reasoning_items` 扩展原样回传；直接把 Responses 密文历史投影成普通 Chat 仍拒绝 |
| JSON Schema | 原生保留 | 原生保留 | 转换格式结构；最终能力仍由上游决定 |
| Codex custom / namespace 工具、`additional_tools` | 原生保留 | 函数表达 | 单层 namespace 名称双向映射，custom 字符串通过函数 JSON 包装，内联声明投影为工具；grammar 仅为提示约束 |
| 拒绝响应（refusal） | 原生保留，允许原生续轮 | 原生保留 | 明确拒绝未实现的转换，不能生成空白成功 |
| 多候选 `n > 1` / `background: true` | 外发前拒绝 | 多候选外发前拒绝 | 不支持 |
| 用户图片输入 | 原生保留 URL / Base64 / file_id | 原生保留 image_url | 三种文本协议之间保留图文顺序、URL/Base64/MIME；详见下文 |
| 音视频、图片生成、其他原生工具服务 | 本轮不认证 | 本轮不认证 | 本轮不认证 |

标准端点仍为 `/v1/responses`、`/v1/chat/completions`，员工使用 XHub 虚拟密钥和公开模型别名。部署中的 `bypass_openai_responses` / `bypass_openai_chat` 是现有**上游执行方式名称**，不表示员工必须使用 `/bypass/...`；该对外路径仍需单独启用。

`store` / `previous_response_id` 沿用 XHub 已有的受控临时历史机制：上游 `store=false`，网关在 PostgreSQL 持久保存身份、模型、入口隔离历史；本轮把原生输出项和密文也纳入其中。它不是 OpenAI 长期 Response 存储，不承诺 GET / DELETE / 后台 cancel 生命周期。HTTP 客户端取消会取消上游请求；缺终态或失败流不能发布成功续接历史。`store=false` 不等于网关完全不保留临时历史。

以下强制绑定适用于 Responses / Chat 封装及服务端历史引用，不适用于客户端完整回传的原生 Messages 历史。提交这些加密历史时，必须具有本身份已知的会话部署归属；不能因权重、冷却、目标禁用或共享缓存故障切换供应商连接。无法确认归属时应发起新会话。

固定续接还会核对首次外发时已解析凭据的连接指纹。保留部署 ID 修改地址、上游型号、传输、凭据名或同名凭据中的密钥后，旧续接返回 409；普通请求更新路由偏好也不会重新绑定旧密文。新版本部署前缓存的归属若没有连接指纹，需要开启新会话；缓存只保存哈希，不保存密钥明文。

Claude Code 使用 `/v1/messages`，并非 Responses 客户端。公开推理现在按 LiteLLM 的规则转换为无厂商签名的 `thinking` 块，工具续轮还原为 Chat 的 `reasoning_content` 或 Responses 的独立推理摘要项。流式 thinking / text 块依次关闭，交错工具参数在完成校验后逐个发送，避免向已关闭块追加内容。Codex 历史中的 `encrypted_content: null` 仅是空占位，可以投影。Responses → Messages 进一步参考 LiteLLM `dba86bec28cb483c9937470c5d950f5e03699d3b`：有摘要的密文封装在 `thinking.signature`，无摘要时放入 `redacted_thinking.data`，统一使用 `xhub_responses_reasoning:` 前缀；它不是 Anthropic 原厂签名。工具续轮按原字节还原为 Responses reasoning 项，省略重建项 ID，并要求同一调用方、公开模型、会话和供应商连接。流式在推理项结束时发送完整封装，先发送签名再关闭内容块；终态再复核公开内容并补缺失项。供应商可在终态重新加密同一推理，因此不要求密文字节相等，已发送的封装保持原样。Messages 封装仍不能作为 Chat 输入或转到原生 Anthropic、其他账户，真实厂商签名继续要求原生通道。本轮额外验证现有 Messages 桥接：文本、函数工具、缓存提示、客户端归属提示、adaptive effort，以及 `clear_thinking_20251015 + keep=all`（保持历史、不执行清理）。缓存提示不保证跨供应商命中，`metadata.user_id` 不替代网关身份。手工 thinking budget、其他清理规则、未知 safeguards / 输出格式及带签名推理不属于本轮跨协议能力，会明确拒绝。真实 2.1.300 CLI 对不支持的 safeguards 会自行重试不带该字段的请求。

## 真实供应商验收：2026-10-15

使用已授权的 Fenno（GPT/Grok）与七牛（DeepSeek）连接，在本机独立网关与数据库执行；生产容器、模型、组织和余额没有改动。四项指 Responses JSON/SSE、Chat JSON/SSE。通过只证明该时点该 Key 的这些请求，不代表全部参数或未来版本均兼容。

| 供应商 | 上游模型 ID | 四项结果 |
| --- | --- | --- |
| Fenno | `gpt-5.5` | 0/4；上游 503，直连两个端点同样 503 |
| Fenno | `gpt-5.6-terra`、`gpt-5.6-sol`、`codex-auto-review`、`gpt-6-astra`、`gpt-6-sol`、`gpt-6.1-sol` | 每个 4/4 |
| Fenno | `grok-4.5`、`grok-4.6` | 每个 4/4 |
| 七牛 | `deepseek/deepseek-v4.1-flash` | 4/4 |
| 七牛 | `deepseek/deepseek-v4-pro-0813` | 4/4 |
| 七牛 | `deepseek/deepseek-v4-flash-20260731` | 4/4 |

接口矩阵共 **44/48 通过，4 项 GPT-5.5 可用性失败**；网关按现有错误契约将上游 503 返回为 502。原始响应保留，探针将正常 `error: null` 识别为无错误。供应商提供的名称不代表独立官方型号认证。七牛该地址的 `/responses` 实测 404，因此 DeepSeek 部署选用 Chat，由 XHub 桥接 Responses/Messages；此前 Fenno 的 DeepSeek 503 不再当作七牛结果。

固定版本 Codex **0.162.1** 与 Claude Code **2.1.300** 均实际执行读取临时文件 → 工具结果回传 → 回答文件内独立标记；标记未出现在提示词中。

| 本机部署 | Codex | Claude Code |
| --- | --- | --- |
| `gpt-5.6-sol`（原生 Responses） | 通过 | 通过 |
| `gpt-5.6-sol-chat`（Chat 桥接） | 冷却结束后重测通过 | 通过 |
| `grok-4.6`（原生 Responses） | 通过 | 通过 |
| `grok-4.6-chat`（Chat 桥接） | 通过 | 通过 |
| `qiniu-deepseek-v4.1-flash`（Chat 桥接） | 通过 | 通过 |
| `qiniu-deepseek-v4-pro-0813`（Chat 桥接） | 通过 | 通过 |
| `qiniu-deepseek-v4-flash-20260731`（Chat 桥接） | 通过 | 通过 |

客户端组合首轮 13/14 通过；GPT Chat 部署因此前上游流错误进入冷却，结束后同配置重测成功，最终 14/14 完成工具闭环。保留首轮失败证据，不把重试成功当作供应商永不失败的保证。此前 GPT/Grok 两种部署的 Responses/Chat/Messages JSON/SSE 验收为 24/24，七牛三个模型三种端点 JSON/SSE 验收为 18/18（包含限流后的串行重测）。测试费率仅用于观察记账，不代表供应商实际价格。

本次文件工具验收关闭服务端网页搜索和外部连接器；泛用 Chat 桥接不会悄悄丢弃这些工具来伪造完整兼容。客户端继续保留推理强度、摘要和 include 请求。原生网页搜索、图片、签名/密文跨厂商重放、长会话压缩不在认证范围。Next 控制台 `/gw` 代理超时改为 10 分钟，网关与客户端原有截止时间仍各自生效；新增真实等待 31 秒的非流式浏览器回归。

固定离线夹具只下发读取测试文件的命令，协议验收不依赖宿主只读沙箱实现；真实供应商测试仍使用只读沙箱。CLI 重试有上限，超时清理整个进程组。Windows CLI 进程组验收本轮未覆盖。

本轮实际验证：internal 单元 687 通过、4 跳过；后台 regression 225 通过、12 跳过（含两个真实 CLI × 两种上游的 4 组离线工具闭环）；浏览器 E2E 4/4；上游夹具单元 14/14；Linux 发布工作流测试 11/11。额外别名冲突修复后，相关单元 50/50 与后台工具桥接回归再次通过。macOS 自带 Bash 3.2 的发布脚本测试有一个既有空数组兼容失败，Linux/CI Bash 环境全部通过。

## 回归与复现

- 单元：参数映射、非法类型、加密/公开推理、原生响应、拒绝/截断流、历史与归属隔离。
- 后台：真实 HTTP / PostgreSQL 覆盖标准端点、护栏脱敏、未授权、预算、JSON/SSE、工具续轮、跨身份密文拒绝、原部署禁用后不得回退、取消、账单和清理。
- 浏览器：另有 Messages 专属用例覆盖两种 OpenAI 上游的公开推理、JSON/SSE 工具续轮和日志计费；管理面保存模型 → 调试台实际调用 → 浏览器原生参数 / 流 / 工具续轮 → Chat 桥接 → 日志与计费 → 删除测试部署和凭据；并回归原有 SSE / JSON 三轮续接及 Codex simulator 日志闭环。
- CI：固定 CLI 版本进入 backend regression；新增浏览器场景进入发布校验，失败也上传 CLI 输出和测试报告。

```bash
# 前置：本机 PostgreSQL 与 redis-server；所有数据库数据使用专属 schema 并回收。
go test -p 2 ./internal/... -count=1 -json
XHUB_TEST_CODEX_BIN=/absolute/path/to/codex \
XHUB_TEST_CLAUDE_BIN=/absolute/path/to/claude \
XHUB_AGENT_REPORT_DIR=/absolute/path/to/reports bash scripts/regression.sh -v
E2E_REDIS=1 E2E_PORTABLE_CONSOLE=1 bash scripts/e2e.sh \
  agent-native-protocol.test.ts messages-reasoning.test.ts agent-chat-bridge.test.ts agent-proxy-timeout.test.ts scenarios/legacy/responses-continuation scenarios/legacy/codex-agent
node --test scripts/test_release_workflow.cjs
```

实际供应商测试必须显式设置 `XHUB_AGENT_LIVE=1`、`XHUB_AGENT_LIVE_KEY`、`XHUB_AGENT_LIVE_BASE`（以 `/v1` 结束），模型矩阵还可指定逗号分隔的 `XHUB_AGENT_LIVE_MODELS`（最多 12 个），再运行 `go test ./cmd/regression -run '^Test(LiveTextProtocolMatrix|RealAgentClients)$' -count=1`。真实 CLI 仍需上述二进制路径。此模式会产生供应商费用；CI 不设置这些凭据。不要把密钥放进代码或报告。

参考：[LiteLLM Responses](https://docs.litellm.ai/docs/response_api)、[官方推理指南](https://developers.openai.com/api/docs/guides/reasoning)、[DeepSeek Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode/)、[Claude Code 网关](https://code.claude.com/docs/en/llm-gateway)。适配器的固定源码版本见授权目录。

Claude Code 原生会话：Messages 优先使用网关 `X-Session-Id` / `session_id`，随后读取 `X-Claude-Code-Session-Id`，或从 `metadata.user_id` 的 JSON 字符串 / 旧 `user_…_account_…_session_…` 格式中提取会话 ID；两类明确会话的日志来源均记为 `explicit`。这参考 LiteLLM 同一官方快照的会话提取规则，避免工具续轮修改首条用户内容或缓存提示后丢失密文归属。普通用户名、设备和账号标识不会被当作会话；原生头必须为 1–256 字节的 ASCII 字母、数字、连字符或下划线，冲突头拒绝，无法识别的可选 metadata 仍按原规则推断。调用方、模型、入口和原连接校验保持有效。原生 Messages 完整历史不依赖旧会话归属；跨协议封装及服务端引用仍需归属验证。


## Chat 的 Responses 推理扩展

进一步参考 LiteLLM `dba86bec28cb483c9937470c5d950f5e03699d3b` 的 `completion_extras/litellm_responses_transformation/transformation.py`：Chat 客户端调用 Responses 上游时，正常返回 `content`、`tool_calls` 和 usage，公开摘要放在 `reasoning_content`，推理项的原始 `id`、`summary`、`encrypted_content` 放在助手消息的 `reasoning_items` 扩展数组。密文不会被解密或当作可见文本。

普通客户端可以忽略此扩展，继续发送可见消息和工具结果，但无法保留该推理状态；这不保证与完整推理历史有相同的效果。需要保持推理的客户端应原样保留助手的 `reasoning_items`，并回传响应头 `X-Session-Id`，随后添加 `role: tool` 的工具结果。扩展只接受 assistant 上的推理数组；仅可送回同一身份、公开模型、入口、会话和供应商连接的 Responses 部署，未知/失效归属及更换连接会拒绝。不会把它直接发送到原生 Chat、Anthropic 或其他厂商。

流式扩展只在上游完整终态校验后随结束 chunk 发布，随后正常返回 usage 和 `[DONE]`；终态重新加密时保留终态的有效值。工具归属先于终态发布。上游失败、损坏推理或截断流不会伪造正常完成；显式 `response.incomplete` 保留 `finish_reason: length`，不会因含工具调用而变成正常结束。这是 LiteLLM 风格扩展，不是 OpenAI Chat 标准字段，也不会给忽略它的客户端暗中补上推理记忆。

## 可选联网搜索与客户端工具发现

Chat 部署默认拒绝不能兑现的 `web_search`。管理员可设置 `model_info.web_search_support=unsupported` 与 `model_info.web_search_fallback=drop_optional`，仅移除可选搜索声明；强制搜索仍拒绝。降级通过 `X-XHub-Dropped-Capabilities: web_search` 和日志 `dropped_capabilities` 明示，不提供真正的联网搜索。原生 Responses 部署维持原有行为，除非明确声明不支持搜索。

`tool_search` 与联网搜索不同：`execution=client` 用于让 Codex 发现其本机/MCP 工具。XHub 将它映射为普通 Chat 函数，返回对象参数的 `tool_search_call`；客户端执行发现后回传 `tool_search_output`，其中的工具定义加载到下一轮 Chat 请求。JSON/SSE 都保留 `call_id`、命名空间和结果历史；原生请求不改写。网关不执行 MCP 工具，不支持通用 Chat 部署上的服务端托管工具搜索。`defer_loading` 只作为加载性能提示处理，不能授予额外权限。

custom 工具继续使用 LiteLLM 风格的 `content` 字符串包装，兼容七牛 DeepSeek 实际返回的单字段 `input` 字符串；不改写代码、不猜测其他参数，不接受非字符串或含额外字段的歧义包装。与 LiteLLM 的原文回退相比，此边界更严格。真实 Codex 0.163.0 的 code-mode、普通函数模式，以及通过 `tool_search` 发现只读 MCP 工具的流程，均以七牛 `deepseek/deepseek-v4.1-flash` 验收；该结果不代表所有模型、任意 MCP 或原生联网搜索均已认证。

参考实现固定版本及差异见 `third_party/litellm/NOTICE.md`；客户端搜索结构见 [OpenAI Tool search](https://developers.openai.com/api/docs/guides/tools-tool-search)。


## 用户图片输入（Issue #15）

标准 Responses、Chat Completions、Messages 入口支持用户消息中的图片：
`input_image.image_url`、`image_url.url`、`image.source`（`base64` 或 `url`）。
同协议保留原生请求；跨协议采用有序文本/图片表示，保留图片及其前后文字、多图顺序、
PNG/JPEG/GIF/WebP 的 MIME 和 Base64 字节。图片单独输入也不会因文本为空被丢弃。
这不增加模型的视觉能力，最终图片格式、尺寸、远程 URL 可达性及识别质量由上游决定。

OpenAI `detail` 的 `auto/low/high/original` 在 Responses ↔ Chat 之间保持原值。
Messages 没有等价精度字段，桥接显式 `low/high/original` 会报能力错误；默认或 `auto` 可以转换。
Responses 的 `file_id` 仅在原生 Responses 保留，不下载文件，也不将供应商私有引用假装成 URL。
原生 Messages 支持用户 `tool_result` 中的图片及图文混合结果（例如 Claude Code Read），保留调用 ID、顺序和错误标记；无法等价转换的工具图片跨协议桥接明确拒绝。音视频、PDF、Google 视觉桥接或图片生成仍不属于本轮支持范围。
有限额度 Key 的用户图片及原生工具图片按可信整窗、统一输入 token 价格预占和结算；独立图片收费仍拒绝，详见[硬额度](hard-budgets.md)。

网关不会为图片 URL 发起下载、重定向或读取本机文件；仅转发 HTTP(S) URL 或校验过的 Base64 data URL。
文本护栏仍处理图片旁的文字，图片不转换成伪文本；计费使用上游实测 token。
纯文本日志/护栏不具备图片 OCR 或图片内容审查能力。
测试包括三协议九种路由的同步/流式、非法输入和鉴权、文本拦截/脱敏、日志计费，以及中英文浏览器上传闭环。
真实供应商图片识别结果见本 PR 验收记录，不以 HTTP 200 代替识别结果正确。

字段参考：[OpenAI 图片输入](https://developers.openai.com/api/docs/guides/images-vision)。
LiteLLM 来源、函数与许可证见 `third_party/litellm/NOTICE.md`。

### 原生 Agent 历史保留

标准 Messages 入口在原生 Anthropic 部署上保留 `thinking.signature`、`redacted_thinking.data` 和 `tool_use.caller={"type":"direct"}`，同步与流式回复均可原样续轮。完整原生 Messages 历史不再要求网关保存旧部署归属：改公开模型名或上游 ID 后仍正常鉴权和选路，签名原样交给上游验证，不解密、不删除思考块。上游仍可能拒绝不兼容的签名，不保证跨供应商迁移。Responses 封装及服务端历史引用仍执行原连接归属校验。程序化工具 caller 仍明确拒绝。原生流只有在内容块、工具参数和消息终态完整时才记录可续轮归属。

Chat SDK 累积或供应商回复中的非负整数 `tool_calls[].index` 可原样回传；同协议保留该字段，跨协议仅移除位置元数据，关联仍使用调用 ID。Responses 一轮中的连续函数调用在 Chat 投影中合并为同一条 assistant 消息，工具结果按 ID 关联并保留原顺序；用户、工具结果或独立推理项构成分组边界。

### Responses 调用原生 Messages 的签名推理

管理员可在模型编辑页将 `messages_reasoning_mode` 设置为 `adaptive`，用于已实测支持 adaptive thinking 的原生 Messages 部署。此时 Responses 的 `reasoning.effort` 支持 `low/medium/high/max`，映射到 `thinking.type=adaptive` 和同值 `output_config.effort`。缺省配置不开放这项跨协议能力；不传 effort 保留上游默认行为，显式 `none` 仍请求禁用推理，上游可以拒绝。该开关不代表 Chat 推理桥接、所有 Claude 型号或所有渠道已认证，也不修改请求的输出上限。

带签名或 redacted 块的完整助手消息保存在服务端，Responses 输出使用 `encrypted_content` 中的随机 `xhub_messages_v1:` 引用。它是 XHub 的短期引用，不是 OpenAI 密文格式。客户端应原样回传该 reasoning 项及其后同轮公开文字和工具项，再追加工具结果；工具项不能删改或重排。XHub 校验后恢复单个完整 Messages assistant，保留原生块和签名字节，并按原调用 ID 连接工具结果。也支持 `previous_response_id`，包括签名工具轮之后普通文字回复的继续对话。

引用绑定调用身份、公开模型、会话及实际供应商连接，保存到 PostgreSQL，不自动过期，随数据库备份保留。缓存过期和进程重启不再丢失历史。更换 Key、别名、会话、连接凭据仍须验证归属。有效旧缓存会按需导入，已过期记录无法自动恢复。它不允许私有推理跨模型迁移。同步和流式都在发布完成项前确认引用、父历史及归属保存成功；保存失败明确报错，保留待核对额度与已观察用量，不重新调用已完成的上游。SSE 支持首个 thinking 在公开正文/工具前出现；首个 thinking 晚于已发送公开内容时明确失败，不能补插或改写已发送的输出顺序。

Messages 文本块的 `citations` 缺省、`null` 或空数组不阻止协议转换；原生 Messages 保留这些字段。非空引用仍须明确可表达，当前跨协议拒绝，避免丢掉引用事实。

标准 Messages 接受普通函数工具上的布尔 `defer_loading`，同协议保留原值。`false` 可按普通函数桥接；`true` 只允许原生 Messages，不能静默转换为立即加载。这仅覆盖工具定义元数据，不代表已支持服务器工具搜索、`tool_reference` 或跨协议发现生命周期。

原生 Claude Messages、OpenAI Responses 和 Chat 透传入口接受客户端完整提供的签名/密文历史，不要求旧模型或部署归属，原始字段交由供应商验证。修改公开名后仍须使用当前授权名称。`previous_response_id` 及任务查询继续验证原身份和连接；XHub随机历史引用或桥接签名不能直接交给原生透传入口。此行为不保证供应商接受其他账户或模型生成的签名，且不改变Key鉴权、模型授权和预算。
