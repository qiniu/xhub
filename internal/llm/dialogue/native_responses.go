package dialogue

import (
	"encoding/json"
	"fmt"
)

// ParseNativeResponses 校验原生 Responses 信封并保存独立正文，不生成跨协议投影。
// 参数 body 为治理后的请求；返回原生对话或结构错误。灰度执行器及历史恢复调用，未知扩展交由上游处理。
// 只校验网关自身依赖的形状；异步生命周期仍不支持，不改变历史归属、公开推理及预算规则。
func ParseNativeResponses(body map[string]any) (Dialogue, error) {
	native, err := cloneObject(body)
	if err != nil {
		return Dialogue{}, err
	}
	for _, key := range []string{"stream", "store", "background"} {
		if value, exists := native[key]; exists {
			flag, ok := value.(bool)
			if !ok {
				return Dialogue{}, fmt.Errorf("%s must be boolean", key)
			}
			if key == "background" && flag {
				return Dialogue{}, fmt.Errorf("background Responses require a native lifecycle endpoint")
			}
		}
	}
	switch input := native["input"].(type) {
	case string:
		if input == "" {
			return Dialogue{}, fmt.Errorf("nonempty conversation is required")
		}
	case []any:
		if len(input) == 0 {
			return Dialogue{}, fmt.Errorf("nonempty conversation is required")
		}
		for _, item := range input {
			if _, ok := item.(map[string]any); !ok {
				return Dialogue{}, fmt.Errorf("conversation item must be an object")
			}
		}
	default:
		return Dialogue{}, fmt.Errorf("input must be a string or array")
	}
	for _, key := range []string{"tools"} {
		if value, exists := native[key]; exists {
			if _, ok := value.([]any); !ok {
				return Dialogue{}, fmt.Errorf("%s must be an array", key)
			}
		}
	}
	normalizeLegacyResponsesTextIDs(native)
	normalizeResponsesMessageMetadata(native)
	stream, _ := native["stream"].(bool)
	return Dialogue{Source: "openai-responses", Native: native, Stream: stream, BridgeError: "native Responses requires explicit protocol conversion"}, nil
}

// ParseNativeResponsesResult 校验原生终态信封并提取计费用量，不要求输出能够投影为 Chat。
// 参数 raw 为完整 JSON；返回保留全部输出的结果或终态错误。同步响应和 SSE 观测调用，无持久化副作用。
func ParseNativeResponsesResult(raw []byte) (DialogueResult, error) {
	var native map[string]any
	if err := json.Unmarshal(raw, &native); err != nil {
		return DialogueResult{}, err
	}
	if native["error"] != nil {
		return DialogueResult{}, fmt.Errorf("upstream dialogue error")
	}
	id, ok := native["id"].(string)
	if !ok || id == "" {
		return DialogueResult{}, fmt.Errorf("invalid upstream response ID")
	}
	status, _ := native["status"].(string)
	if status != "completed" && status != "incomplete" {
		return DialogueResult{}, fmt.Errorf("upstream response is not complete")
	}
	output, ok := native["output"].([]any)
	if !ok {
		return DialogueResult{}, fmt.Errorf("invalid upstream output")
	}
	for _, item := range output {
		if _, ok := item.(map[string]any); !ok {
			return DialogueResult{}, fmt.Errorf("invalid upstream output item")
		}
	}
	if value := native["usage"]; value != nil {
		if _, ok := value.(map[string]any); !ok {
			return DialogueResult{}, fmt.Errorf("invalid upstream usage")
		}
	}
	stop := "stop"
	if status == "incomplete" {
		stop = "length"
	}
	return DialogueResult{ID: id, Stop: stop, Source: "openai-responses", Native: native, Usage: NormalizeDialogueUsage(dialogueMap(native["usage"])), BridgeError: "native Responses result requires explicit protocol conversion"}, nil
}
