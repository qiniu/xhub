package dialogue

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// parseUnifiedDialogue 将已投影的用户 OpenAI、Anthropic、Gemini/Vertex 对话解码成共享表示（图片仅三种文本协议）。参数为协议及正文；返回对话或字段错误。
// 调用：ParseDialogue 的共享投影或 Google 解析分支；不支持的能力明确拒绝，无外部副作用。
func parseUnifiedDialogue(protocol string, body map[string]any) (Dialogue, error) {
	if protocol == "gemini" || protocol == "vertex" {
		return parseGoogleDialogue(body)
	}
	d := Dialogue{Options: map[string]any{}}
	if !strings.Contains("|openai-chat|openai-responses|anthropic-messages|", "|"+protocol+"|") {
		return d, fmt.Errorf("unregistered dialogue protocol %s", protocol)
	}
	if v, exists := body["stream"]; exists {
		var ok bool
		d.Stream, ok = v.(bool)
		if !ok {
			return d, fmt.Errorf("stream must be boolean")
		}
	}
	allowed := map[string]bool{}
	for _, k := range []string{"model", "stream", "stream_options", "tools", "tool_choice", "max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p"} {
		allowed[k] = true
	}
	switch protocol {
	case "openai-chat":
		allowed["messages"] = true
	case "openai-responses":
		allowed["input"], allowed["instructions"] = true, true
	case "anthropic-messages":
		allowed["messages"], allowed["system"] = true, true
	}
	if value, exists := body["stream_options"]; exists {
		if err := dialogueFields(dialogueMap(value), "include_usage"); err != nil {
			return d, err
		}
		if _, ok := dialogueMap(value)["include_usage"].(bool); !ok {
			return d, fmt.Errorf("stream_options.include_usage must be boolean")
		}
	}
	for k, v := range body {
		if !allowed[k] && v != nil {
			return d, fmt.Errorf("unsupported unified capability %s; submit full history or use a compatible native endpoint", k)
		}
	}
	for _, k := range []string{"temperature", "top_p"} {
		if v, ok := body[k]; ok {
			n, valid := dialogueNumber(v)
			if !valid || n < 0 || (k == "top_p" && n > 1) || (k == "temperature" && n > 2) {
				return d, fmt.Errorf("invalid %s", k)
			}
			d.Options[k] = v
		}
	}
	for _, k := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if v, ok := body[k]; ok {
			n, valid := dialogueNumber(v)
			if !valid || n <= 0 || n != math.Trunc(n) {
				return d, fmt.Errorf("invalid %s", k)
			}
			if _, exists := d.Options["max_tokens"]; exists {
				return d, fmt.Errorf("token limits are mutually exclusive")
			}
			d.Options["max_tokens"] = v
		}
	}
	if v, ok := body["tool_choice"]; ok {
		d.Options["tool_choice"] = v
	}
	if protocol == "openai-responses" {
		if s := dialogueString(body["instructions"]); s != "" {
			d.Turns = append(d.Turns, Turn{Role: "system", Text: s})
		}
	}
	if protocol == "anthropic-messages" {
		s, e := textContent(body["system"])
		if e != nil {
			return d, e
		}
		if s != "" {
			d.Turns = append(d.Turns, Turn{Role: "system", Text: s})
		}
	}
	raw := body["messages"]
	if protocol == "openai-responses" {
		raw = body["input"]
		if s, ok := raw.(string); ok {
			raw = []any{map[string]any{"role": "user", "content": s}}
		}
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return d, fmt.Errorf("nonempty conversation is required")
	}
	for _, item := range list {
		m := dialogueMap(item)
		if m == nil {
			return d, fmt.Errorf("conversation item must be an object")
		}
		fields := []string{"role", "content"}
		if protocol == "openai-chat" {
			fields = append(fields, "tool_call_id", "tool_calls", "reasoning_content", "reasoning_items")
		}
		if protocol == "openai-responses" {
			fields = append(fields, "type", "call_id", "name", "arguments", "output", "id", "status", "summary")
		}
		if err := dialogueFields(m, fields...); err != nil {
			return d, err
		}
		role := dialogueString(m["role"])
		kind := dialogueString(m["type"])
		if protocol == "openai-responses" && kind != "" && kind != "message" && kind != "function_call" && kind != "function_call_output" && kind != "reasoning" {
			return d, fmt.Errorf("unsupported input item %s", kind)
		}
		if protocol == "openai-responses" && kind == "reasoning" {
			var text string
			if value, exists := m["summary"]; exists {
				if _, ok := value.([]any); !ok {
					return d, fmt.Errorf("reasoning summary must be an array")
				}
			}
			for _, part := range dialogueList(m["summary"]) {
				block := dialogueMap(part)
				if block["type"] != "summary_text" {
					return d, fmt.Errorf("unsupported reasoning summary")
				}
				value, ok := block["text"].(string)
				if !ok {
					return d, fmt.Errorf("reasoning summary text must be a string")
				}
				text += value
			}
			if text != "" {
				d.Turns = append(d.Turns, Turn{Role: "reasoning", Reasoning: text})
			}
			continue
		}
		if protocol == "openai-responses" && kind == "function_call" {
			call := Turn{ID: dialogueString(m["call_id"]), Name: dialogueString(m["name"]), Arguments: dialogueString(m["arguments"])}
			// Responses 按项保存同轮并行调用，Chat 必须在一条 assistant.tool_calls 中声明整组。
			// 仅并入相邻 assistant；遇到工具结果、用户或推理项便重新起组，不跨已经完成的工具轮次。
			if n := len(d.Turns); n > 0 && d.Turns[n-1].Role == "assistant" && d.Turns[n-1].Native == nil {
				d.Turns[n-1].Calls = append(d.Turns[n-1].Calls, call)
			} else {
				d.Turns = append(d.Turns, Turn{Role: "assistant", Calls: []Turn{call}})
			}
			continue
		}
		if protocol == "openai-responses" && kind == "function_call_output" {
			s, e := textContent(m["output"])
			if e != nil {
				return d, e
			}
			d.Turns = append(d.Turns, Turn{Role: "tool", ID: dialogueString(m["call_id"]), Text: s})
			continue
		}
		if !strings.Contains("|system|developer|user|assistant|tool|", "|"+role+"|") {
			return d, fmt.Errorf("unsupported message role %s", role)
		}
		if value := m["reasoning_content"]; value != nil {
			if _, ok := value.(string); !ok {
				return d, fmt.Errorf("reasoning_content must be a string")
			}
		}
		turn := Turn{Role: role, ID: dialogueString(m["tool_call_id"]), Reasoning: dialogueString(m["reasoning_content"])}
		if protocol == "openai-chat" {
			reasoning, err := chatReasoningReplay(m["reasoning_items"], role)
			if err != nil {
				return d, err
			}
			if len(reasoning) > 0 {
				// 扩展已经携带原始摘要，不能再把 reasoning_content 合成为一份重复的推理项。
				d.Turns = append(d.Turns, reasoning...)
				turn.Reasoning = ""
			}
		}
		if protocol == "anthropic-messages" {
			if parts, ok := m["content"].([]any); ok {
				hasImage := false
				for _, part := range parts {
					if dialogueMap(part)["type"] == "image" {
						hasImage = true
					}
				}
				for _, p := range parts {
					b := dialogueMap(p)
					fields := []string{"type", "text", "id", "name", "input", "tool_use_id", "content", "is_error"}
					if b["type"] == "thinking" {
						fields = []string{"type", "thinking", "signature"}
					} else if b["type"] == "image" {
						fields = []string{"type", "source"}
					} else if b["type"] == "redacted_thinking" {
						fields = []string{"type", "data"}
					}
					if err := dialogueFields(b, fields...); err != nil {
						return d, err
					}
					switch dialogueString(b["type"]) {
					case "thinking", "redacted_thinking":
						reasoning, err := messagesReasoningReplay(b, role)
						if err != nil {
							return d, err
						}
						if reasoning.Native != nil {
							// 每份密文独立保留；不能合并两个推理项的密文，也不能把它们重放给 Chat 或原厂 Anthropic。
							if turn.Text != "" || turn.Reasoning != "" || len(turn.Calls) > 0 {
								d.Turns = append(d.Turns, turn)
								turn = Turn{Role: role}
							}
							d.Turns = append(d.Turns, reasoning)
						} else {
							turn.Reasoning += reasoning.Reasoning
						}
					case "text":
						if _, ok := b["text"].(string); !ok {
							return d, fmt.Errorf("content text must be string")
						}
						turn.Text += dialogueString(b["text"])
						if hasImage {
							turn.Content = append(turn.Content, ContentPart{Type: "text", Text: dialogueString(b["text"])})
						}
					case "image":
						image, e := parseImagePart(b, protocol, role)
						if e != nil {
							return d, e
						}
						turn.Content = append(turn.Content, image)
					case "tool_use":
						a, e := json.Marshal(b["input"])
						if e != nil {
							return d, e
						}
						turn.Calls = append(turn.Calls, Turn{ID: dialogueString(b["id"]), Name: dialogueString(b["name"]), Arguments: string(a)})
					case "tool_result":
						if b["is_error"] == true {
							return d, fmt.Errorf("unsupported tool result is_error")
						}
						s, e := textContent(b["content"])
						if e != nil {
							return d, e
						}
						d.Turns = append(d.Turns, Turn{Role: "tool", ID: dialogueString(b["tool_use_id"]), Text: s})
					default:
						return d, fmt.Errorf("unsupported content capability %v", b["type"])
					}
				}
			} else {
				s, e := textContent(m["content"])
				if e != nil {
					return d, e
				}
				turn.Text = s
			}
		} else {
			s, content, e := messageContent(m["content"], protocol, role)
			if e != nil {
				return d, e
			}
			turn.Text, turn.Content = s, content
			if value, exists := m["tool_calls"]; exists {
				if _, ok := value.([]any); !ok {
					return d, fmt.Errorf("tool_calls must be an array")
				}
			}
			for _, rawCall := range dialogueList(m["tool_calls"]) {
				c := dialogueMap(rawCall)
				if c["type"] != "function" {
					return d, fmt.Errorf("only function tools are supported")
				}
				if err := dialogueFields(c, "id", "type", "function"); err != nil {
					return d, err
				}
				f := dialogueMap(c["function"])
				if err := dialogueFields(f, "name", "arguments"); err != nil {
					return d, err
				}
				turn.Calls = append(turn.Calls, Turn{ID: dialogueString(c["id"]), Name: dialogueString(f["name"]), Arguments: dialogueString(f["arguments"])})
			}
		}
		if turn.Text != "" || len(turn.Content) > 0 || turn.Reasoning != "" || len(turn.Calls) > 0 || role == "tool" {
			d.Turns = append(d.Turns, turn)
		}
	}
	if value, exists := body["tools"]; exists {
		if _, ok := value.([]any); !ok {
			return d, fmt.Errorf("tools must be an array")
		}
	}
	for _, rawTool := range dialogueList(body["tools"]) {
		tool := dialogueMap(rawTool)
		f := tool
		if protocol == "openai-chat" {
			if tool["type"] != "function" {
				return d, fmt.Errorf("only function tools are supported")
			}
			if err := dialogueFields(tool, "type", "function"); err != nil {
				return d, err
			}
			f = dialogueMap(tool["function"])
		}
		if protocol == "openai-responses" && tool["type"] != "function" {
			return d, fmt.Errorf("only function tools are supported")
		}
		if err := dialogueFields(f, "type", "name", "parameters", "input_schema", "description", "strict"); err != nil {
			return d, err
		}
		parameters := f["parameters"]
		if protocol == "anthropic-messages" {
			parameters = f["input_schema"]
		}
		if _, ok := parameters.(map[string]any); !ok {
			return d, fmt.Errorf("tool schema must be an object")
		}
		if v, exists := f["strict"]; exists {
			if _, ok := v.(bool); !ok {
				return d, fmt.Errorf("tool strict must be boolean")
			}
		}
		if dialogueString(f["name"]) == "" {
			return d, fmt.Errorf("tool name is required")
		}
		normalized := map[string]any{"name": f["name"], "parameters": parameters}
		if v, ok := f["description"]; ok {
			normalized["description"] = v
		}
		if v, ok := f["strict"]; ok {
			normalized["strict"] = v
		}
		d.Tools = append(d.Tools, normalized)
	}
	for _, t := range d.Turns {
		if t.Role == "tool" && t.ID == "" {
			return d, fmt.Errorf("tool result ID is required")
		}
		for _, c := range t.Calls {
			if c.ID == "" || c.Name == "" || !json.Valid([]byte(c.Arguments)) {
				return d, fmt.Errorf("tool call requires ID, name and valid JSON arguments")
			}
		}
	}
	if len(d.Turns) == 0 {
		return d, fmt.Errorf("conversation has no supported content")
	}
	return d, nil
}

// EncodeDialogue 按显式上游协议编码图文/工具对话，型号原样传递，不能表达的图片能力明确返回错误。参数为内部表示、协议、真实型号；返回正文或能力错误。
// 调用：统一执行器；工具 ID 和参数不重新生成，Messages 工具结果按相邻角色合并。
func EncodeDialogue(d Dialogue, protocol, model string) (map[string]any, error) {
	// 即使候选与客户端同为 Chat/Messages，扩展携带的 OpenAI 推理也只能送回 Responses；先于原生正文直传检查。
	for _, turn := range d.Turns {
		if turn.Native != nil && turn.NativeProtocol != protocol {
			return nil, fmt.Errorf("opaque history requires %s", turn.NativeProtocol)
		}
	}
	if d.Source != "" {
		if d.Source == protocol {
			out, err := cloneObject(d.Native)
			if err != nil {
				return nil, err
			}
			out["model"] = model
			if protocol == "openai-responses" {
				out["store"] = false
			}
			if protocol == "openai-chat" && d.Stream {
				opts := dialogueMap(out["stream_options"])
				if opts == nil {
					opts = map[string]any{}
				}
				opts["include_usage"] = true
				out["stream_options"] = opts
			}
			return out, nil
		}
		if d.BridgeError != "" {
			return nil, fmt.Errorf("unsupported protocol bridge: %s", d.BridgeError)
		}
		base := d
		base.Source, base.Native = "", nil
		if d.Source == "openai-responses" && protocol == "openai-chat" {
			var err error
			base.Turns, err = projectReasoningContext(d.Turns, dialogueMap(d.Native["reasoning"])["context"])
			if err != nil {
				return nil, err
			}
		}
		out, err := EncodeDialogue(base, protocol, model)
		if err != nil {
			return nil, err
		}
		return bridgeOptions(d, protocol, out)
	}
	if protocol == "gemini" || protocol == "vertex" {
		for _, turn := range d.Turns {
			if len(turn.Content) > 0 {
				return nil, fmt.Errorf("image content requires an implemented image protocol")
			}
		}
		return encodeGoogleDialogue(d)
	}
	out := map[string]any{"model": model, "stream": d.Stream}
	if d.Stream && protocol == "openai-chat" {
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	for k, v := range d.Options {
		if k != "tool_choice" && k != "max_tokens" {
			out[k] = v
		}
	}
	if v, ok := d.Options["max_tokens"]; ok {
		key := "max_tokens"
		if protocol == "openai-responses" {
			key = "max_output_tokens"
		}
		out[key] = v
	} else if protocol == "anthropic-messages" {
		out["max_tokens"] = 4096
	}
	var messages []any
	var system []string
	var pendingReasoning string
	for _, t := range d.Turns {
		if t.Native != nil {
			if t.NativeProtocol != protocol {
				return nil, fmt.Errorf("opaque history requires %s", t.NativeProtocol)
			}
			messages = append(messages, t.Native)
			continue
		}
		if t.Role == "reasoning" && (protocol == "openai-chat" || protocol == "anthropic-messages") {
			pendingReasoning += t.Reasoning
			continue
		}
		switch protocol {
		case "openai-chat":
			m := map[string]any{"role": t.Role, "content": t.Text}
			if len(t.Content) > 0 {
				content, err := encodeImageContent(t.Content, protocol)
				if err != nil {
					return nil, err
				}
				m["content"] = content
			}
			if t.Role == "assistant" && (pendingReasoning != "" || t.Reasoning != "") {
				m["reasoning_content"] = pendingReasoning + t.Reasoning
				pendingReasoning = ""
			}
			if t.Role == "tool" {
				m["tool_call_id"] = t.ID
			}
			if len(t.Calls) > 0 {
				var calls []any
				for _, c := range t.Calls {
					calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": c.Arguments}})
				}
				m["tool_calls"] = calls
			}
			messages = append(messages, m)
		case "openai-responses":
			if t.Reasoning != "" {
				messages = append(messages, map[string]any{"type": "reasoning", "id": fmt.Sprintf("rs_bridge_%d", len(messages)), "summary": []any{map[string]any{"type": "summary_text", "text": t.Reasoning}}})
			}
			if t.Role == "reasoning" {
				continue
			}
			if t.Role == "tool" {
				messages = append(messages, map[string]any{"type": "function_call_output", "call_id": t.ID, "output": t.Text})
				continue
			}
			if len(t.Content) > 0 {
				content, err := encodeImageContent(t.Content, protocol)
				if err != nil {
					return nil, err
				}
				messages = append(messages, map[string]any{"role": t.Role, "content": content})
			} else if t.Text != "" {
				messages = append(messages, map[string]any{"role": t.Role, "content": t.Text})
			}
			for _, c := range t.Calls {
				messages = append(messages, map[string]any{"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": c.Arguments})
			}
		case "anthropic-messages":
			if t.Role == "system" || t.Role == "developer" {
				system = append(system, t.Text)
				continue
			}
			role := t.Role
			var parts []any
			if role == "tool" {
				role = "user"
				parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": t.ID, "content": t.Text})
			} else {
				if role == "assistant" && (pendingReasoning != "" || t.Reasoning != "") {
					parts = append(parts, map[string]any{"type": "thinking", "thinking": pendingReasoning + t.Reasoning, "signature": ""})
					pendingReasoning = ""
				}
				if len(t.Content) > 0 {
					content, err := encodeImageContent(t.Content, protocol)
					if err != nil {
						return nil, err
					}
					parts = append(parts, content...)
				} else if t.Text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": t.Text})
				}
				for _, c := range t.Calls {
					var args any
					if json.Unmarshal([]byte(c.Arguments), &args) != nil {
						return nil, fmt.Errorf("invalid tool arguments")
					}
					parts = append(parts, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": args})
				}
			}
			if len(messages) > 0 && dialogueMap(messages[len(messages)-1])["role"] == role {
				prev := dialogueMap(messages[len(messages)-1])
				prev["content"] = append(dialogueList(prev["content"]), parts...)
			} else {
				messages = append(messages, map[string]any{"role": role, "content": parts})
			}
		default:
			return nil, fmt.Errorf("unregistered dialogue protocol %s", protocol)
		}
	}
	key := "messages"
	if protocol == "openai-responses" {
		key = "input"
		out["store"] = false
	}
	out[key] = messages
	if len(system) > 0 {
		out["system"] = strings.Join(system, "\n")
	}
	if len(d.Tools) > 0 {
		var tools []any
		for _, f := range d.Tools {
			cp := map[string]any{}
			for k, v := range f {
				cp[k] = v
			}
			switch protocol {
			case "openai-chat":
				tools = append(tools, map[string]any{"type": "function", "function": cp})
			case "openai-responses":
				cp["type"] = "function"
				tools = append(tools, cp)
			case "anthropic-messages":
				// strict=false 明确关闭约束，等价于 Messages 的普通工具；strict=true 仍需独立的能力验证。
				if strict, exists := cp["strict"]; exists && strict != false {
					return nil, fmt.Errorf("strict tools cannot be expressed by Messages")
				}
				delete(cp, "strict")
				cp["input_schema"] = cp["parameters"]
				delete(cp, "parameters")
				tools = append(tools, cp)
			}
		}
		out["tools"] = tools
	}
	if choice, ok := d.Options["tool_choice"]; ok {
		s := dialogueString(choice)
		name := ""
		if m := dialogueMap(choice); m != nil {
			kind := dialogueString(m["type"])
			if kind == "auto" {
				s = "auto"
			} else if kind == "any" {
				s = "required"
			} else if kind == "none" {
				s = "none"
			} else {
				name = dialogueString(m["name"])
				if f := dialogueMap(m["function"]); f != nil {
					name = dialogueString(f["name"])
				}
			}
		}
		if name != "" {
			switch protocol {
			case "openai-chat":
				out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": name}}
			case "openai-responses":
				out["tool_choice"] = map[string]any{"type": "function", "name": name}
			default:
				out["tool_choice"] = map[string]any{"type": "tool", "name": name}
			}
		} else if s == "auto" || s == "required" || s == "none" {
			if protocol == "anthropic-messages" {
				if s == "required" {
					s = "any"
				}
				out["tool_choice"] = map[string]any{"type": s}
			} else {
				out["tool_choice"] = s
			}
		} else {
			return nil, fmt.Errorf("unsupported tool_choice")
		}
	}
	if pendingReasoning != "" {
		return nil, fmt.Errorf("reasoning history requires following assistant message")
	}
	return out, nil
}
