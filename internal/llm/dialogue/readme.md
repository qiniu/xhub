# dialogue

[简体中文](readme_cn.md)

Native field preservation for matching protocols; dialogue codecs and strict capability validation for bridges.

Matching OpenAI protocols retain Agent fields/results in `Dialogue.Native` / `DialogueResult.Native`. Bridges use validated projections and reject non-equivalent encrypted reasoning, tools or extensions. See the [compatibility matrix](../../../docs/development/en/agent-protocol-compatibility.md) for boundaries, LiteLLM sources and live supplier results.

## Integration from other modules

Integration here means other Go packages in this repository. Go internal import rules restrict imports from outside the repository; HTTP clients use mounted gateway endpoints.

Select an explicit source protocol, parse and check errors, then encode for the destination protocol and upstream model. Use the corresponding result codecs for responses; dataplane handles network execution.

| Entry point or resource | Usage and result |
| --- | --- |
| ParseDialogue(protocol, body) | Retain source protocol plus a validated shared projection |
| EncodeDialogue(dialogue, protocol, model) | Encode a request map for the target protocol |
| ParseDialogueResult / EncodeDialogueResult | Decode and encode response facts |

### Integration demo

Save the code as `demo/main.go` inside the repository and run `go run ./demo` from the repository root. Remove the demo directory after use.

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

Prints 1 demo-model 4096 with no credentials or network. Protocol IDs are openai-chat, openai-responses, anthropic-messages, gemini, and vertex.

### Errors, state, and side effects

Unsupported fields, roles, and tools must fail rather than silently losing capabilities. Consume gateway control fields upstream; use native forwarding for full vendor extensions.

## Source and verification

Code entry points: [dialogue_google.go](dialogue_google.go), [model.go](model.go), [request.go](request.go), [response.go](response.go), [usage.go](usage.go), [validation.go](validation.go)

Unit tests live beside source: [dialogue_google_test.go](dialogue_google_test.go), [dialogue_sdk_test.go](dialogue_sdk_test.go), [dialogue_test.go](dialogue_test.go)

go test ./internal/llm/dialogue/...

model defines normalized values; request/response encode and decode; validation checks fields; usage normalizes reported facts; dialogue_google handles Google. No network calls.

Backend regressions live in cmd/regression; browser flows in frontend/e2e. Offline tests use local HTTP upstreams and do not establish production credential or vendor availability.

## Coverage gate

Run scripts/core-codec-coverage.sh using bash. Registry and dialogue codec must each reach 95% statement coverage. Test failures, missing evidence, or a lower percentage fail the command. The profile uses package unit tests without adding backend regression or browser coverage.

## Codex to native Messages parameter boundaries

This change accepts only parameters with a defined mapping. It does not establish support for the default Codex reasoning mode:

- `strict:false` becomes an ordinary Messages tool with the same schema; `strict:true` is still rejected.
- `parallel_tool_calls` becomes the inverse `tool_choice.disable_parallel_tool_use`, preserving named and forced tool choices. No parallel flag is needed without tools or with `none`. Source: [Anthropic parallel tool use](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use).
- `text.format.type=text` is normalized to the default text output. Structured formats and `verbosity` remain unsupported.
- `prompt_cache_key` must be a string. This OpenAI cache-routing hint is not forwarded across protocols, does not guarantee a cache hit, and does not automatically enable `cache_control` with potential cache-write charges. `prompt_cache_retention` is still rejected because there is no equivalent retention guarantee.
- Only explicit `reasoning.effort=none` (Chat `reasoning_effort=none`) maps to `thinking.type=disabled`; this is usable only when the selected upstream model supports disabling thinking. Absent/null reasoning, an empty reasoning object, or valid summary hints without effort leave `thinking` unspecified and preserve the upstream default. Some models require adaptive thinking and reject `disabled`, so the bridge must not inject it for ordinary text requests. Invalid summaries and non-null `reasoning.context` are rejected. Optional summaries and `include=reasoning.encrypted_content` never fabricate reasoning or ciphertext. Native Messages requests keep their original settings. Automatic signed thinking returned by the upstream remains an unsupported cross-protocol boundary tracked in #56; preserving the default does not solve that response/replay limitation.
- Nonzero reasoning efforts such as `low` are still rejected before the upstream call. Native Anthropic signed thinking does not yet have a complete response, streaming, and tool-replay bridge. Enabling adaptive thinking would risk failing after a paid upstream call; silently disabling reasoning or fabricating signatures is not an acceptable substitute.

Same-protocol requests retain their original fields. Unit tests cover parameter mappings, boundaries, and early rejection. A real Codex test with explicit `none` must not be reported as acceptance of its default reasoning mode.

## Messages refusal responses and continuation

Native Messages preserves `stop_reason=refusal`, `stop_details`, empty `content:[]`, reported usage and the complete `message_stop` lifecycle. A valid refusal is a model outcome, not a broken stream.

When the response has no partial output and `stop_details.explanation` contains an actual explanation, Chat receives `message.refusal` (streaming `delta.refusal`) and Responses receives a typed refusal content block with the standard refusal event lifecycle. No explanation is fabricated. Missing/empty explanations, partial text/tools/thinking, malformed details and truncated streams fail explicitly when they cannot be represented across protocols; they never become an empty successful answer.

For subsequent requests, the single standard public Chat/Responses refusal generated by this bridge is projected into assistant history text, preserving the exact explanation. This enables a new question on the same Messages upstream or another model without treating public refusal text as connection-bound opaque state. The current response retains its explicit refusal marker; native request bodies remain unchanged. Valid mixed refusal/output or empty refusal history remains available natively and is rejected only across protocols. Unexpected refusal block fields, invalid field types and non-assistant refusal history are rejected during parsing.

Sources: [Anthropic refusals](https://platform.claude.com/docs/en/build-with-claude/refusals-and-fallback), [OpenAI streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events). Reported token usage is retained; these mappings do not infer upstream invoice charges from refusal categories.

## Native Responses stream options

Responses `stream_options` does not use the Chat field allowlist or require `include_usage`.
Native requests preserve `reasoning_summary_delivery` (such as `sequential_cutoff`) and upstream extensions.
The gateway validates the options object and basic types of known fields; the upstream determines extension support.
Options other than `include_usage` require native Responses: cross-protocol conversion fails explicitly instead of silently discarding delivery semantics.
Chat keeps its existing `include_usage` validation. Tests cover native JSON/SSE, invalid types, upstream rejection and bridge boundaries.
