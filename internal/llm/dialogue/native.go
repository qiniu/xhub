package dialogue

// 映射依据及 MIT 授权见 third_party/litellm/NOTICE.md；不包含 enterprise 代码或 Python 运行时依赖。

import (
	"encoding/json"
	"fmt"
)

// cloneObject 复制 JSON 对象供协议投影与模型改名使用；参数为原始正文，返回独立对象或编码错误。
// 请求/响应适配器调用；不共享嵌套引用，不允许非 JSON 值，无外部副作用。
func cloneObject(body map[string]any) (map[string]any, error) {
	if body == nil {
		return nil, fmt.Errorf("JSON object required")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}

// ParseDialogue 同时保存原协议正文和可桥接的文本、用户图片和函数投影，参数为入口协议与护栏后的正文。
// 统一入口调用；同协议交由上游校验新增字段，跨协议仅允许已映射的能力，返回输入错误或私有对话。
// 参考 LiteLLM 的原生适配优先、桥接后备分层，不按模型名称猜测协议；未知能力不会被悄悄删除后外发。
// Responses 规范已识别的完整公开历史：旧桥接 ID 和 Grok 公开摘要元数据；其他原生项、密文和引用保留身份。
func ParseDialogue(protocol string, body map[string]any) (Dialogue, error) {
	if protocol != "openai-responses" && protocol != "openai-chat" && protocol != "anthropic-messages" {
		return parseUnifiedDialogue(protocol, body)
	}
	native, err := cloneObject(body)
	if err != nil {
		return Dialogue{}, err
	}
	if protocol == "openai-responses" {
		normalizeLegacyResponsesTextIDs(native)
		normalizeResponsesMessageMetadata(native)
	}
	// 后台异步生成需要检索/取消生命周期，当前标准入口只有受控的临时续接，必须在外发前拒绝。
	if value := native["background"]; value != nil {
		flag, ok := value.(bool)
		if !ok {
			return Dialogue{}, fmt.Errorf("background must be boolean")
		}
		if flag {
			return Dialogue{}, fmt.Errorf("background Responses require a native lifecycle endpoint")
		}
	}
	if value := native["n"]; value != nil {
		number, ok := dialogueNumber(value)
		if !ok || number != 1 {
			return Dialogue{}, fmt.Errorf("only one completion is supported")
		}
	}
	projected := map[string]any{}
	for _, key := range []string{"model", "stream", "stream_options", "tools", "tool_choice", "max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p"} {
		if v, ok := native[key]; ok {
			projected[key] = v
		}
	}
	key := "messages"
	if protocol == "openai-responses" {
		key = "input"
		projected["instructions"] = native["instructions"]
	}
	if protocol == "anthropic-messages" {
		projected["system"] = stripCacheHints(native["system"])
	}
	value := native[key]
	if text, ok := value.(string); ok && protocol == "openai-responses" {
		value = []any{map[string]any{"role": "user", "content": text}}
	}
	if value == nil {
		return parseUnifiedDialogue(protocol, projected)
	}
	if protocol == "openai-chat" && (native["input"] != nil || native["previous_response_id"] != nil) {
		return Dialogue{}, fmt.Errorf("unsupported unified capability: Responses fields on Chat")
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return Dialogue{}, fmt.Errorf("nonempty conversation is required")
	}
	var bridgeError string
	var turns []any
	responseTools := append([]any(nil), dialogueList(native["tools"])...)
	for _, item := range items {
		m := dialogueMap(item)
		if m == nil {
			return Dialogue{}, fmt.Errorf("conversation item must be an object")
		}
		cp, _ := cloneObject(m)
		if protocol == "openai-chat" {
			if err := projectChatToolIndices(cp); err != nil {
				return Dialogue{}, err
			}
		}
		if protocol == "anthropic-messages" {
			for _, value := range dialogueList(cp["content"]) {
				if err := projectMessagesCaller(dialogueMap(value)); err != nil {
					return Dialogue{}, err
				}
			}
		}
		if protocol == "openai-responses" {
			switch dialogueString(cp["type"]) {
			case "additional_tools":
				// Codex 会把延迟工具声明放在输入项里；它们是工具定义，不是用户消息，必须与顶层工具一同双向映射。
				definitions, ok := cp["tools"].([]any)
				if !ok {
					return Dialogue{}, fmt.Errorf("additional_tools requires tools array")
				}
				responseTools = append(responseTools, definitions...)
				continue
			case "web_search_call":
				if !ValidWebSearchHistory(cp) {
					return Dialogue{}, fmt.Errorf("invalid web search history item")
				}
				// 搜索执行事实只能由原生 Responses 保留；跨协议不伪造工具结果，也不重复执行历史搜索。
				bridgeError = "web search history requires native Responses"
				continue
			case "tool_search_call", "tool_search_output":
				// 客户端执行工具发现等价于一次工具往返；结果中的定义必须在下一次 Chat 请求中真正可调用。
				definitions, err := projectClientSearchHistory(cp)
				if err != nil {
					// 托管搜索历史没有通用 Chat 等价物；保留原生正文，桥接明确拒绝，不假装已经执行搜索。
					bridgeError = err.Error()
					continue
				}
				responseTools = append(responseTools, definitions...)
			case "", "message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output":
				if err := projectAgentCall(cp); err != nil {
					return Dialogue{}, err
				}
			case "reasoning":
				if cp["encrypted_content"] != nil {
					bridgeError = "encrypted reasoning history"
					continue
				}
				// 官方客户端会为公开推理补 encrypted_content:null；共享投影移除空占位，原生正文仍保留原值。
				delete(cp, "encrypted_content")
			case "item_reference", "compaction":
				bridgeError = "input item " + dialogueString(cp["type"])
				continue
			default:
				return Dialogue{}, fmt.Errorf("unsupported input item %s", dialogueString(cp["type"]))

			}
		}
		for _, key := range []string{"phase", "namespace"} {
			if cp[key] != nil {
				bridgeError = "input " + key
				delete(cp, key)
			}
		}
		if cp["refusal"] != nil {
			bridgeError = "refusal history"
			delete(cp, "refusal")
		}
		if parts, ok := cp["content"].([]any); ok {
			content := []any{}
			for _, part := range parts {
				if dialogueMap(part)["type"] == "refusal" {
					bridgeError = "refusal history"
				} else {
					content = append(content, part)
				}
			}
			cp["content"] = content
		}
		cp["content"] = stripCacheHints(cp["content"])
		if protocol == "anthropic-messages" {
			// cache_control 是缓存性能提示，桥接不能保证命中；保留原文、工具结果及其错误文本。
			for _, part := range dialogueList(cp["content"]) {
				block := dialogueMap(part)
				if block["is_error"] == true && block["tool_use_id"] != nil {
					text, e := textContent(block["content"])
					if e != nil {
						return Dialogue{}, e
					}
					block["content"] = "[tool_error] " + text
					block["is_error"] = false
				}
			}
		}
		turns = append(turns, cp)
	}
	projected[key] = turns
	var tools []any
	var bindings map[string]ToolBinding
	if protocol == "openai-responses" {
		var unsupported string
		tools, bindings, unsupported, err = projectAgentTools(responseTools)
		if err != nil {
			return Dialogue{}, err
		}
		if unsupported != "" {
			bridgeError = unsupported
		}
		// 指定的 namespace/custom 工具选择也必须使用相同的 Chat 函数名，避免选择一个未注册名称。
		if choice := dialogueMap(native["tool_choice"]); choice != nil {
			if choice["type"] == "function" || choice["type"] == "custom" {
				projected["tool_choice"] = map[string]any{"type": "function", "name": qualifiedToolName(dialogueString(choice["namespace"]), dialogueString(choice["name"]))}
			} else if choice["type"] == "tool_search" && bindings["tool_search"].ClientSearch {
				projected["tool_choice"] = map[string]any{"type": "function", "name": "tool_search"}
			}
		}
	}
	for _, value := range dialogueList(native["tools"]) {
		if protocol == "openai-responses" {
			break
		}
		tool := dialogueMap(value)
		if tool == nil {
			return Dialogue{}, fmt.Errorf("tool must be an object")
		}
		cp, _ := cloneObject(tool)
		delete(cp, "cache_control")
		tools = append(tools, cp)
	}
	if _, exists := native["tools"]; exists {
		if _, ok := native["tools"].([]any); !ok {
			return Dialogue{}, fmt.Errorf("tools must be an array")
		}
		projected["tools"] = tools
	} else if len(tools) > 0 {
		projected["tools"] = tools
	}
	// 这里只校验共享结构。无法投影的不透明项仍保留在原正文，不能伪造为用户文本。
	d, err := parseUnifiedDialogue(protocol, projected)
	if err != nil {
		return Dialogue{}, err
	}
	for _, key := range []string{"reasoning", "text", "response_format", "metadata", "thinking", "output_config"} {
		if v := native[key]; v != nil && dialogueMap(v) == nil {
			return Dialogue{}, fmt.Errorf("%s must be an object", key)
		}
	}
	if v := native["include"]; v != nil {
		list, ok := v.([]any)
		if !ok {
			return Dialogue{}, fmt.Errorf("include must be an array")
		}
		for _, v := range list {
			if _, ok := v.(string); !ok {
				return Dialogue{}, fmt.Errorf("include values must be strings")
			}
		}
	}
	for _, key := range []string{"parallel_tool_calls", "store"} {
		if v := native[key]; v != nil {
			if _, ok := v.(bool); !ok {
				return Dialogue{}, fmt.Errorf("%s must be boolean", key)
			}
		}
	}
	d.Source, d.Native, d.BridgeError, d.ToolBindings = protocol, native, bridgeError, bindings
	return d, nil
}

// stripCacheHints 为文本投影复制内容并去除 Anthropic 的缓存提示；参数为 system 或 content。
// ParseDialogue 调用，返回原文本或复制后的数组；只影响跨协议投影，同协议原文完整保留。
// 缓存提示不影响生成语义，不能把 thinking、图像等内容当作缓存提示删除。
func stripCacheHints(value any) any {
	list, ok := value.([]any)
	if !ok {
		return value
	}
	out := make([]any, 0, len(list))
	for _, value := range list {
		if m := dialogueMap(value); m != nil {
			cp, _ := cloneObject(m)
			delete(cp, "cache_control")
			out = append(out, cp)
		} else {
			out = append(out, value)
		}
	}
	return out
}

// bridgeOptions 将已知扩展参数映射到目标协议，参数为带原文的对话、目标协议及已编码的共享正文。
// EncodeDialogue 调用，返回映射后正文或明确的能力错误；只做参数转换，不发送请求。
// 参照 LiteLLM Responses/Chat 桥接中的 reasoning_effort 与 response_format 映射；不支持的字段拒绝而非 drop_params。
func bridgeOptions(d Dialogue, target string, out map[string]any) (map[string]any, error) {
	core := map[string]bool{}
	for _, k := range []string{"model", "stream", "stream_options", "tools", "tool_choice", "max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p", "messages", "input", "instructions", "system"} {
		core[k] = true
	}
	for key, value := range d.Native {
		if core[key] || value == nil {
			continue
		}
		switch key {
		case "background", "n":
			// false 后台开关和单候选已在解析时验证，跨协议不产生异步任务或多候选语义。
		case "store":
			if value != false {
				return nil, fmt.Errorf("store requires native protocol")
			}
		case "reasoning":
			r := dialogueMap(value)
			for k, v := range r {
				if k == "context" {
					// Chat 的公开推理已在 EncodeDialogue 按 active/all turns 范围投影，不能将该 Responses 字段直接发给上游。
					continue
				}
				if k == "summary" && v != nil {
					if !validReasoningSummary(v) {
						return nil, fmt.Errorf("invalid reasoning summary")
					}
					// LiteLLM 同样拆分 effort 与摘要提示；Chat 保留公开推理，但没有原生摘要粒度参数。
					continue
				}
				if k != "effort" && v != nil {
					return nil, fmt.Errorf("reasoning.%s requires native Responses", k)
				}
			}
			if target != "openai-chat" {
				return nil, fmt.Errorf("reasoning requires native Responses or Chat")
			}
			out["reasoning_effort"] = r["effort"]
		case "reasoning_effort":
			if target != "openai-responses" {
				return nil, fmt.Errorf("reasoning_effort requires OpenAI protocol")
			}
			out["reasoning"] = map[string]any{"effort": value}
		case "text":
			if target != "openai-chat" {
				return nil, fmt.Errorf("text options require native Responses or Chat")
			}
			t := dialogueMap(value)
			if format, exists := t["format"]; exists && dialogueMap(format) == nil {
				return nil, fmt.Errorf("text.format must be an object")
			}
			if format := dialogueMap(t["format"]); format["type"] == "text" && len(format) != 1 {
				return nil, fmt.Errorf("unsupported text.format options")
			}
			for k := range t {
				if k != "format" && k != "verbosity" {
					return nil, fmt.Errorf("unsupported text.%s bridge", k)
				}
			}
			if v := t["verbosity"]; v != nil {
				out["verbosity"] = v
			}
			if f := dialogueMap(t["format"]); f != nil && f["type"] != "text" {
				if f["type"] == "json_schema" {
					schema, _ := cloneObject(f)
					delete(schema, "type")
					out["response_format"] = map[string]any{"type": "json_schema", "json_schema": schema}
				} else if f["type"] == "json_object" {
					out["response_format"] = f
				} else {
					return nil, fmt.Errorf("unsupported text.format bridge")
				}
			}
		case "response_format":
			if target != "openai-responses" {
				return nil, fmt.Errorf("response_format requires OpenAI protocol")
			}
			f := dialogueMap(value)
			if f["type"] == "json_schema" {
				schema, _ := cloneObject(dialogueMap(f["json_schema"]))
				if schema == nil {
					return nil, fmt.Errorf("json_schema must be an object")
				}
				schema["type"] = "json_schema"
				out["text"] = map[string]any{"format": schema}
			} else {
				out["text"] = map[string]any{"format": f}
			}
		case "include":
			for _, item := range dialogueList(value) {
				if item != "reasoning.encrypted_content" {
					return nil, fmt.Errorf("include requires native Responses")
				}
			}
			// include 是可选返回字段请求，不是已提交的密文；不伪造密文，真正的密文历史仍由 ParseDialogue 拒绝。
		case "client_metadata":
			if err := agentClientMetadata(value); err != nil {
				return nil, err
			}
		case "metadata":
			if target != "openai-chat" && target != "openai-responses" {
				return nil, fmt.Errorf("metadata requires OpenAI protocol")
			}
			if d.Source == "anthropic-messages" {
				if err := dialogueFields(dialogueMap(value), "user_id"); err != nil {
					return nil, err
				}
			}
			if d.Source != "anthropic-messages" && (target == "openai-chat" || target == "openai-responses") {
				out[key] = value
			}
			// Anthropic metadata.user_id 是客户端归属提示，身份始终由虚拟密钥决定，不转换成网关用户。
		case "parallel_tool_calls", "prompt_cache_key", "prompt_cache_retention", "service_tier", "safety_identifier", "user":
			if target != "openai-chat" && target != "openai-responses" {
				return nil, fmt.Errorf("%s requires OpenAI protocol", key)
			}
			out[key] = value
		case "context_management":
			// keep=all 是 Claude Code 明确要求保留全部推理，不触发清理，等价于网关默认保留历史。
			config := dialogueMap(value)
			if edits, ok := config["edits"]; ok {
				if _, valid := edits.([]any); !valid {
					return nil, fmt.Errorf("context_management.edits must be an array")
				}
			}
			if err := dialogueFields(config, "edits"); err != nil {
				return nil, err
			}
			for _, value := range dialogueList(config["edits"]) {
				edit := dialogueMap(value)
				if edit["type"] != "clear_thinking_20251015" || edit["keep"] != "all" || len(edit) != 2 {
					return nil, fmt.Errorf("context_management edits require native Messages")
				}
			}
		case "thinking", "output_config":
			if d.Source != "anthropic-messages" || (target != "openai-responses" && target != "openai-chat") {
				return nil, fmt.Errorf("%s requires native Messages", key)
			}
			thinking := dialogueMap(d.Native["thinking"])
			for k, v := range thinking {
				if k != "type" && !(k == "display" && v == "omitted") {
					return nil, fmt.Errorf("thinking.%s requires native Messages", k)
				}
			}
			if err := dialogueFields(dialogueMap(d.Native["output_config"]), "effort"); d.Native["output_config"] != nil && err != nil {
				return nil, err
			}
			if thinking["type"] == "disabled" && dialogueMap(d.Native["output_config"])["effort"] != nil {
				return nil, fmt.Errorf("thinking disabled conflicts with effort")
			}
			if thinking["type"] == "disabled" {
				if target == "openai-chat" {
					out["reasoning_effort"] = "none"
				} else {
					out["reasoning"] = map[string]any{"effort": "none"}
				}
			}
			if typ := thinking["type"]; typ != nil && typ != "adaptive" && typ != "disabled" {
				return nil, fmt.Errorf("thinking budget requires native Messages")
			}
			effort := dialogueMap(d.Native["output_config"])["effort"]
			if effort == nil && thinking["type"] == "adaptive" {
				effort = "medium"
			}
			if effort != nil {
				if target == "openai-chat" {
					out["reasoning_effort"] = effort
				} else {
					out["reasoning"] = map[string]any{"effort": effort}
				}
			}
		default:
			return nil, fmt.Errorf("%s requires native %s", key, d.Source)
		}
	}
	return out, nil
}

// ParseDialogueResult 保留上游原生结果，同时提取文本/函数/用量供日志、计费和可表达的跨协议结果使用。
// 参数为登记协议与 JSON 正文，返回结果或协议错误；同步响应和 SSE 完成事件调用，无外部副作用。
// 推理、加密项及供应商扩展保留到同协议输出；跨协议不能表达时显式报错。
func ParseDialogueResult(protocol string, raw []byte) (DialogueResult, error) {
	if protocol != "openai-responses" && protocol != "openai-chat" && protocol != "anthropic-messages" {
		return parseUnifiedDialogueResult(protocol, raw)
	}
	var native map[string]any
	if err := json.Unmarshal(raw, &native); err != nil {
		return DialogueResult{}, err
	}
	projection, err := cloneObject(native)
	if err != nil {
		return DialogueResult{}, err
	}
	bridgeError := ""
	var messagesReasoning string
	if protocol == "anthropic-messages" {
		var err error
		messagesReasoning, bridgeError, err = projectMessagesResult(projection)
		if err != nil {
			return DialogueResult{}, err
		}
	} else if protocol == "openai-responses" {
		list, ok := projection["output"].([]any)
		if !ok {
			return DialogueResult{}, fmt.Errorf("invalid upstream output")
		}
		items := []any{}
		for _, value := range list {
			item := dialogueMap(value)
			if item == nil {
				return DialogueResult{}, fmt.Errorf("invalid upstream output item")
			}
			switch item["type"] {
			case "message", "function_call":
				if item["namespace"] != nil && item["namespace"] != "" {
					bridgeError = "namespaced function call"
				}
				if item["type"] == "message" {
					content := []any{}
					for _, part := range dialogueList(item["content"]) {
						block := dialogueMap(part)
						if block["type"] == "refusal" {
							if _, ok := block["refusal"].(string); !ok {
								return DialogueResult{}, fmt.Errorf("invalid refusal")
							}
							bridgeError = "refusal output"
						} else {
							content = append(content, part)
						}
					}
					if item["content"] != nil {
						if _, ok := item["content"].([]any); ok {
							item["content"] = content
						}
					}
				}
				items = append(items, item)
			case "reasoning":
				if dialogueString(item["id"]) == "" {
					return DialogueResult{}, fmt.Errorf("reasoning item ID required")
				}
				// Responses 的 store=false 现会默认携带密文。校验后交给 Messages 的不透明封装，不能因此丢弃正常答案。
				if _, err := MessagesReasoningBlock(item); err != nil {
					return DialogueResult{}, err
				}
			default:
				bridgeError = "output item " + dialogueString(item["type"])
			}
		}
		projection["output"] = items
	} else {
		for _, value := range dialogueList(projection["choices"]) {
			message := dialogueMap(dialogueMap(value)["message"])
			if message["refusal"] != nil {
				if _, ok := message["refusal"].(string); !ok {
					return DialogueResult{}, fmt.Errorf("invalid refusal")
				}
				bridgeError = "refusal output"
				delete(message, "refusal")
			}
		}
	}
	encoded, _ := json.Marshal(projection)
	r, err := parseUnifiedDialogueResult(protocol, encoded)
	if err != nil {
		return r, err
	}
	if protocol == "openai-chat" {
		choices := dialogueList(native["choices"])
		if len(choices) == 1 {
			value := dialogueMap(dialogueMap(choices[0])["message"])["reasoning_content"]
			if value != nil {
				if _, ok := value.(string); !ok {
					return DialogueResult{}, fmt.Errorf("invalid upstream reasoning_content")
				}
			}
			r.Reasoning = dialogueString(value)
		}
	} else if protocol == "anthropic-messages" {
		r.Reasoning = messagesReasoning
	} else {
		for _, part := range dialogueList(native["output"]) {
			item := dialogueMap(part)
			if item["type"] == "reasoning" {
				for _, summary := range dialogueList(item["summary"]) {
					r.Reasoning += dialogueString(dialogueMap(summary)["text"])
				}
			}
		}
	}
	if protocol == "openai-responses" {
		r.Usage = ResponsesSearchUsage(native, r.Usage)
	}
	r.Source, r.Native, r.BridgeError = protocol, native, bridgeError
	return r, nil
}
