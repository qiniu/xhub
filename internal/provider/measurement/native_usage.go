package measurement

import (
	"encoding/json"
	"strings"

	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// NativeUsage 按原生协议建立用量提取器，只记录事实，不在传输层决定价格。
// 参数 protocol：注册表中的协议标识；返回函数的 doc 是上游响应，request 是原始请求字段。
// 返回：可供目录计价的用量；缺少事实时带 pricing_blocked，明确失败时返回 nil。
// 调用：原生传输注册、NativeStreamUsage。价格由 catalog 和部署规则决定。
// 测试：native_usage_test.go。
func NativeUsage(protocol string) func(map[string]any, map[string]any) map[string]any {
	return func(doc, request map[string]any) map[string]any {
		if doc["error"] != nil {
			return nil
		}
		if protocol == "openai-responses" {
			if status, _ := doc["status"].(string); status != "" && status != "completed" && !nativeResponsesTerminal(doc, "incomplete") {
				return map[string]any{"pricing_blocked": "response_not_completed"}
			}
		}
		if protocol == "gemini" || protocol == "vertex" {
			source, _ := doc["usageMetadata"].(map[string]any)
			if len(source) == 0 {
				return map[string]any{"pricing_blocked": "upstream_usage_missing"}
			}
			return map[string]any{"prompt_tokens": source["promptTokenCount"], "completion_tokens": nativeNumber(source["candidatesTokenCount"]) + nativeNumber(source["thoughtsTokenCount"]), "prompt_tokens_details": map[string]any{"cached_tokens": source["cachedContentTokenCount"]}}
		}
		source, _ := doc["usage"].(map[string]any)
		// 先保留原始计数冲突；原生透传不会经过统一流的 observeUsage。
		out := llmdialogue.MergeDialogueUsage(nil, source)
		if protocol == "openai-responses" {
			out = llmdialogue.ResponsesSearchUsage(doc, out)
		}
		if protocol == "openai-responses" && (doc["status"] == "completed" || doc["status"] == "incomplete") {
			// Responses 终态必须自带完整计数，不能拿前面的部分用量补齐退款依据。
			// 保留原始字段供硬额度核价；只复制独立终态归一化得到的阻断原因。
			if reason := llmdialogue.NormalizeDialogueUsage(source)["pricing_blocked"]; reason != nil {
				out["pricing_blocked"] = reason
			}
		}
		if protocol == "openai-images" {
			// Images 协议中的 output_tokens 表示图片输出 token。把这个上游事实
			// 显式带给目录计价器，避免在同时存在多个输出 token 变体时猜价格。
			out["output_variant"] = "image"
			if doc["type"] == "image_generation.completed" || doc["type"] == "image_edit.completed" {
				out["images"] = 1
			}
			if data, ok := doc["data"].([]any); ok {
				out["images"] = len(data)
			}
			quality, _ := doc["quality"].(string)
			size, _ := doc["size"].(string)
			if quality == "" {
				quality, _ = request["quality"].(string)
			}
			if size == "" {
				size, _ = request["size"].(string)
			}
			if quality == "auto" || size == "auto" {
				quality, size = "", ""
				out["pricing_blocked"] = "image_variant_unknown"
			}
			if size != "" {
				out["image_variant"] = size
			}
			if quality != "" && size != "" {
				out["image_variant"] = quality + "_" + size
			}
		}
		if len(out) == 0 {
			return map[string]any{"pricing_blocked": "upstream_usage_missing"}
		}
		return out
	}
}

// NativeStreamUsage 读取完整 SSE 记录中的用量，不改写发送给客户端的字节。
// 参数 protocol：原生协议；raw：包含空行分隔符的事件；request：请求中的图片规格等事实。
// 返回：本批事件报告的用量；无事实的中间事件返回 nil，终态缺失事实保留计价阻断原因。
// 调用：dataplane.forwardOfficial。图片的跨事件累加由转发器完成。
// 测试：native_usage_test.go。
func NativeStreamUsage(protocol string, raw []byte, request map[string]any) map[string]any {
	out := map[string]any{}
	source := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, event := range strings.Split(source, "\n\n") {
		var data []string
		var eventType string
		for _, line := range strings.Split(event, "\n") {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		var doc map[string]any
		if json.Unmarshal([]byte(strings.Join(data, "\n")), &doc) != nil {
			continue
		}
		typ, _ := doc["type"].(string)
		if typ == "" {
			typ = eventType
			doc["type"] = typ // 事件头与 JSON type 都是协议事件名，统一后才能提取图片完成事实。
		}
		terminal := typ == "response.completed" || (protocol == "openai-responses" && typ == "response.incomplete") || typ == "image_generation.completed" || typ == "image_edit.completed"
		if protocol == "openai-images" && !terminal {
			continue // 部分图片事件不是生成结果，不能提前计入图片或规格。
		}
		for _, key := range []string{"response", "message"} {
			if nested, ok := doc[key].(map[string]any); ok {
				doc = nested
				break
			}
		}
		facts := NativeUsage(protocol)(doc, request)
		for k, v := range facts {
			if k == "pricing_blocked" && !terminal && v != "upstream_usage_conflict" && v != "upstream_usage_invalid" {
				delete(facts, k) // 中间事件允许没有用量；终态未知规格必须阻止结算。
			}
		}
		// 按帧更新别名快照；后帧的正常累计增长不能与前帧旧名字拼成假矛盾。
		out = llmdialogue.MergeDialogueUsage(out, facts)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NativeStreamState 保存一次原生流的完成、失败及图片完成事件数。
// 流正常 EOF 不代表业务成功；必须观察到协议正常终态且没有失败，明确输出上限属于正常终态。
type NativeStreamState struct {
	Completed        bool
	Failed           bool
	Images           int
	ResponseID       string // 正常结束的 Responses 终态报告的存储响应 ID。
	ResponseTerminal bool   // 完整且合法的 Responses 完成/长度终态，可立即停止读取，无需等待 EOF。
}

// Observe 消费一个完整 SSE 事件，更新状态但不修改事件内容。
// 参数 protocol：协议标识；raw：原始事件。返回：无，结果写入接收者。
// 调用：dataplane.forwardOfficial。错误状态只累积，不会被后续完成事件覆盖。
// 测试：native_usage_test.go。
func (s *NativeStreamState) Observe(protocol string, raw []byte) {
	var data []string
	var event string
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	var doc map[string]any
	payload := strings.TrimSpace(strings.Join(data, "\n"))
	if protocol == "openai-chat" && payload == "[DONE]" {
		s.Completed = true
		return
	}
	// 终态事件头不能掩盖坏 JSON，否则不完整输出会被记为成功并触发计费或续接。
	if json.Unmarshal([]byte(payload), &doc) != nil || doc == nil {
		return
	}
	if typ, _ := doc["type"].(string); typ != "" {
		event = typ
	}
	if event == "error" || event == "response.failed" || (event == "response.incomplete" && protocol != "openai-responses") || doc["error"] != nil {
		s.Failed = true
	}
	switch protocol {
	case "gemini", "vertex":
		if candidates, ok := doc["candidates"].([]any); ok {
			for _, candidate := range candidates {
				item, _ := candidate.(map[string]any)
				if reason, _ := item["finishReason"].(string); reason != "" {
					s.Completed = true
				}
			}
		}
	case "openai-chat":
		if strings.TrimSpace(strings.Join(data, "\n")) == "[DONE]" {
			s.Completed = true
		}
	case "openai-responses":
		response, _ := doc["response"].(map[string]any)
		if event == "response.incomplete" {
			// 输出上限是正常协议终态，不等于传输失败；其他 incomplete 原因继续阻断结算。
			if nativeResponsesTerminal(response, "incomplete") {
				s.Completed = true
				s.ResponseTerminal = true
				s.ResponseID, _ = response["id"].(string)
			} else {
				s.Failed = true
			}
		}
		if event == "response.completed" {
			s.ResponseTerminal = nativeResponsesTerminal(response, "completed")
			s.Completed = true
			if response, ok := doc["response"].(map[string]any); ok {
				s.ResponseID, _ = response["id"].(string)
			}
		}
	case "anthropic-messages":
		if event == "message_stop" {
			s.Completed = true
		}
	case "openai-images":
		if event == "image_generation.completed" || event == "image_edit.completed" {
			s.Completed = true
			s.Images++
		}
	}
}

// nativeNumber 读取原生响应中的数值；参数为解码字段，返回数字或零。
// 调用：Gemini 实测候选与思考 token 汇总；不存在或非数字不推测用量，无副作用。
func nativeNumber(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		n, _ := v.Float64()
		return n
	}
	return 0
}
