package dialogue

import (
	"encoding/json"
	"fmt"
	"math"

	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// nativeMessagesStream 在同协议透传旁累积可校验的原生快照；签名和密文只原样保存，不解释或重新签发。
// 每请求独占，内容块按 Anthropic 顺序开始/结束；终态前不得提交会话归属。
type nativeMessagesStream struct {
	message     map[string]any
	blocks      []any
	active      int
	partialJSON string
	usage       map[string]any
	stop        string
}

// consume 校验并累积一条 Messages 原生事件，返回终态结果、是否完成和错误。
// pipeNativeDialogue 调用；参数为本次已解码事件，允许修改私有快照；不写 HTTP、不访问存储。
// 非法块索引、错类型增量、未闭合工具参数、无结束原因或不完整生命周期都拒绝，不能发布成功历史。
func (s *nativeMessagesStream) consume(event map[string]any) (llmdialogue.DialogueResult, bool, error) {
	fail := func(reason string) (llmdialogue.DialogueResult, bool, error) {
		return llmdialogue.DialogueResult{}, false, fmt.Errorf("invalid native Messages stream: %s", reason)
	}
	typ := streamString(event["type"])
	if typ == "ping" {
		return llmdialogue.DialogueResult{}, false, nil
	}
	if typ == "error" || event["error"] != nil {
		return fail("upstream error")
	}
	if typ == "message_start" {
		m := streamMap(event["message"])
		if s.message != nil || streamString(m["id"]) == "" || m["role"] != "assistant" || m["type"] != "message" {
			return fail("invalid message_start")
		}
		if content, ok := m["content"].([]any); !ok || len(content) != 0 {
			return fail("message_start content must be empty")
		}
		s.message = m
		// 合法拒绝可不产生任何内容块；必须回构 [] 而不是 nil 编码出的 null，否则终态会被自己的解析器拒绝。
		s.blocks = []any{}
		s.active = -1
		s.usage = llmdialogue.MergeDialogueUsage(nil, streamMap(m["usage"]))
		return llmdialogue.DialogueResult{}, false, nil
	}
	if s.message == nil {
		return fail("event before message_start")
	}
	switch typ {
	case "content_block_start", "content_block_delta", "content_block_stop":
		number, ok := event["index"].(float64)
		if !ok || number < 0 || number != math.Trunc(number) {
			return fail("invalid content index")
		}
		index := int(number)
		if typ == "content_block_start" {
			block := streamMap(event["content_block"])
			if s.active != -1 || index != len(s.blocks) || block == nil || s.stop != "" {
				return fail("content blocks must open in order")
			}
			switch block["type"] {
			case "text":
				if _, ok := block["text"].(string); !ok {
					return fail("text must be a string")
				}
			case "thinking":
				if _, ok := block["thinking"].(string); !ok {
					return fail("thinking must be a string")
				}
			case "redacted_thinking":
				if data, ok := block["data"].(string); !ok || data == "" {
					return fail("redacted data must be nonempty")
				}
			case "tool_use":
				if streamString(block["id"]) == "" || streamString(block["name"]) == "" || streamMap(block["input"]) == nil {
					return fail("invalid tool start")
				}
			default:
				return fail("unsupported content block")
			}
			s.active = index
			s.partialJSON = ""
			s.blocks = append(s.blocks, block)
		} else {
			if s.active != index || index >= len(s.blocks) {
				return fail("content block is not open")
			}
			block := streamMap(s.blocks[index])
			if typ == "content_block_stop" {
				if block["type"] == "tool_use" && s.partialJSON != "" {
					var input map[string]any
					if json.Unmarshal([]byte(s.partialJSON), &input) != nil || input == nil {
						return fail("invalid tool arguments")
					}
					block["input"] = input
				}
				s.active = -1
				s.partialJSON = ""
			} else {
				d := streamMap(event["delta"])
				field, key := "", ""
				switch d["type"] {
				case "text_delta":
					if block["type"] != "text" {
						return fail("text delta on other block")
					}
					field, key = "text", "text"
				case "thinking_delta":
					if block["type"] != "thinking" {
						return fail("thinking delta on other block")
					}
					field, key = "thinking", "thinking"
				case "signature_delta":
					if block["type"] != "thinking" {
						return fail("signature delta on other block")
					}
					field, key = "signature", "signature"
				case "input_json_delta":
					if block["type"] != "tool_use" {
						return fail("tool delta on other block")
					}
					key = "partial_json"
				default:
					return fail("unsupported content delta")
				}
				value, ok := d[key].(string)
				if !ok {
					return fail("delta must be a string")
				}
				if field == "" {
					s.partialJSON += value
				} else {
					old := block[field]
					if old != nil {
						if _, ok := old.(string); !ok {
							return fail("invalid initial content")
						}
					}
					block[field] = streamString(old) + value
				}
			}
		}
	case "message_delta":
		if s.active != -1 {
			return fail("message delta before block stop")
		}
		delta := streamMap(event["delta"])
		if value := delta["stop_reason"]; value != nil {
			reason, ok := value.(string)
			if !ok || reason == "" {
				return fail("invalid stop_reason")
			}
			if s.stop != "" && s.stop != reason {
				return fail("conflicting stop_reason")
			}
			s.stop = reason
		}
		for k, v := range delta {
			s.message[k] = v
		}
		s.usage = llmdialogue.MergeDialogueUsage(s.usage, streamMap(event["usage"]))
	case "message_stop":
		if s.active != -1 || s.stop == "" {
			return fail("message_stop without completed blocks and stop_reason")
		}
		s.message["content"], s.message["usage"] = s.blocks, s.usage
		raw, err := json.Marshal(s.message)
		if err != nil {
			return llmdialogue.DialogueResult{}, false, err
		}
		result, err := llmdialogue.ParseDialogueResult("anthropic-messages", raw)
		return result, err == nil, err
	default:
		return fail("unsupported event")
	}
	return llmdialogue.DialogueResult{}, false, nil
}
