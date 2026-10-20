# dialogue

[English](readme.md)

同协议原生字段保留、跨协议对话编解码与严格能力校验。

OpenAI 同协议使用 `Dialogue.Native` / `DialogueResult.Native` 保留 Agent 字段与原生输出，跨协议只使用已验证投影；不能等价表达的推理密文、工具或扩展明确拒绝。完整范围、LiteLLM 来源及供应商实测见[兼容对照](../../../docs/development/zh-CN/agent-protocol-compatibility.md)。

## 如何由其他模块接入

这里的“对外”指仓库内其他 Go 包。Go 的 `internal` 规则限制了仓库外的直接导入；HTTP 客户端使用已挂载的网关接口。

适配入口明确指定源协议 ParseDialogue，检查错误，再 EncodeDialogue 为目标协议和上游型号。响应使用 ParseDialogueResult/EncodeDialogueResult；网络发送由 dataplane 负责。

| 入口或资源 | 调用方式与返回结果 |
| --- | --- |
| ParseDialogue(protocol, body) | 保存原协议及已验证共享投影 |
| EncodeDialogue(dialogue, protocol, model) | 编码为目标协议请求映射 |
| ParseDialogueResult / EncodeDialogueResult | 响应事实编解码 |

### 接入 demo

将下面代码保存为仓库内的 `demo/main.go`，在仓库根目录执行 `go run ./demo`；运行后删除本次创建的 demo 目录。

```go
package main

import (
	"fmt"
	"github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// main 演示本页 API 的最小调用；无参数和返回值，打印上述预期结果，依赖与副作用见本页说明。
func main() {
	d, err := dialogue.ParseDialogue("openai-responses", map[string]any{"input": "Hello"})
	if err != nil {
		panic(err)
	}
	body, err := dialogue.EncodeDialogue(d, "anthropic-messages", "demo-model")
	if err != nil {
		panic(err)
	}
	fmt.Println(len(d.Turns), body["model"], body["max_tokens"])
}
```

输出 1 demo-model 4096；无需凭据或网络。协议标识为 openai-chat、openai-responses、anthropic-messages、gemini、vertex。

### 错误、状态与副作用

不支持的字段、角色和工具结构必须返回错误，不能静默丢弃能力。网关控制字段由上层先消费；完整原厂扩展走原生透传。

## 源码与验证

代码入口：[dialogue_google.go](dialogue_google.go), [model.go](model.go), [request.go](request.go), [response.go](response.go), [usage.go](usage.go), [validation.go](validation.go)

单元测试与源代码放在一起：[dialogue_google_test.go](dialogue_google_test.go), [dialogue_sdk_test.go](dialogue_sdk_test.go), [dialogue_test.go](dialogue_test.go), [google_boundaries_test.go](google_boundaries_test.go), [request_boundaries_test.go](request_boundaries_test.go), [response_boundaries_test.go](response_boundaries_test.go)

go test ./internal/llm/dialogue/...

model 定义统一表示，request/response 编解码，validation 校验字段，usage 统一用量，dialogue_google 处理 Google 协议。无网络调用。

真实后台回归在 cmd/regression，浏览器流程在 frontend/e2e。离线测试使用本地 HTTP 上游，不证明生产凭据或外部厂商服务可用性。

## 覆盖率检查

运行 scripts/core-codec-coverage.sh（使用 bash），注册表和对话编解码各自必须达到 95% 语句覆盖率。测试失败、缺少报告或低于下限都会返回失败。覆盖率来自各包自身的单元测试，不混入后台回归或浏览器测试。

## Codex 到原生 Messages 的参数边界

本次只放行有明确对应关系的参数，不能将此项表述为 Codex 默认推理模式已完整可用：

- `strict:false` 转为普通 Messages 工具，schema 保持不变；`strict:true` 仍拒绝。
- `parallel_tool_calls` 转为 `tool_choice.disable_parallel_tool_use` 的反值，保持指定工具和强制工具选择。没有工具或选择 `none` 时无需附加并行开关。映射依据：[Anthropic 并行工具文档](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use)。
- `text.format.type=text` 是默认文本输出，可规范化；结构化输出和 `verbosity` 仍拒绝。
- `prompt_cache_key` 只接受字符串，跨协议不转发这个 OpenAI 缓存路由提示，不保证缓存命中，也不自动开启可能增加缓存写入费用的 `cache_control`。`prompt_cache_retention` 没有等价承诺，仍拒绝。
- `reasoning.effort=none`（或 Chat 的 `reasoning_effort=none`）转为 `thinking.type=disabled`。可选摘要和 `include=reasoning.encrypted_content` 不会制造不存在的推理或密文。
- `low` 等非零推理努力仍在调用前拒绝。当前原生 Anthropic 签名 thinking 的跨协议响应、流和工具续轮尚未形成完整可重放链，不能直接启用 adaptive 再等付费响应到达后断流，也不能伪造签名或把低推理偷偷改为关闭。

同协议请求完整保留原始字段。本目录单测覆盖参数映射、边界和发送前拒绝；真实 Codex 默认模式仍需后续签名推理端到端工作，显式 `none` 的测试不得充当默认模式验收。
