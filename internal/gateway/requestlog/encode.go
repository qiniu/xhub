package requestlog

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Encode 为日志详情生成请求消息、响应和代理请求文档，并掩盖请求头及地址中的常见认证信息。
// 参数 r 为入站请求，reqBody 和 respBody 分别是原始请求与响应字节；返回供日志详情读取的三份 JSON。
// 正文不进行全面脱敏，可能包含敏感内容；调用方必须先确定存储策略和日志访问权限。
// 调用：日志正文存储路径；测试：encode_test.go 和 prompt_response_test.go。
func Encode(r *http.Request, reqBody, respBody []byte) (messages, response, proxy string) {
	reqDoc := jsonDocument(reqBody)
	respDoc := jsonDocument(respBody)
	if reqDoc == nil && len(reqBody) > 0 {
		reqDoc = map[string]any{"body": string(reqBody)}
	}
	if respDoc == nil && len(respBody) > 0 {
		respDoc = map[string]any{"body": string(respBody)}
	}
	if assembled := assembleLoggedResponse(respBody, respDoc); assembled != nil {
		respDoc = assembled
	}
	messagesDoc := reqDoc
	if m, ok := reqDoc.(map[string]any); ok {
		if msgs, exists := m["messages"]; exists {
			messagesDoc = msgs
		}
	}
	headers := map[string]string{}
	method, path := "", ""
	if r != nil {
		method = r.Method
		path = redactRequestURI(r)
		for k, vals := range r.Header {
			headers[k] = strings.Join(vals, ", ")
		}
		redactHeaders(headers)
	}
	proxyDoc := map[string]any{
		"method":  method,
		"url":     path,
		"headers": headers,
		"body":    reqDoc,
	}
	return mustJSON(messagesDoc), mustJSON(respDoc), mustJSON(proxyDoc)
}

// assembleLoggedResponse turns a stored event stream into the final response document the log drawer can read. A chat or Responses stream is not one JSON value, so it used to be kept as a raw body and the output panel stayed empty.
// 参数 raw（[]byte）：原始文本或 JSON 字节；parsed（any）：assembleLogged响应接到的动态值。类型在函数体内收窄。
// 返回 any（any）：assembleLogged响应。没有合格值时为 nil。
// 调用：仅在 spend.go 内使用
// 测试：prompt_response_test.go
func assembleLoggedResponse(raw []byte, parsed any) any {
	text := ""
	switch doc := parsed.(type) {
	case map[string]any:
		if body, ok := doc["body"].(string); ok {
			text = body
		}
	}
	if text == "" && looksLikeEventStream(raw) {
		text = string(raw)
	}
	if text == "" {
		return nil
	}
	var completed map[string]any
	var outputText strings.Builder
	var chat strings.Builder
	var reasoning strings.Builder
	tools := map[int]*streamTool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "data:")
		line = strings.TrimSpace(line)
		if line == "" || line == "[DONE]" {
			continue
		}
		var doc map[string]any
		if json.Unmarshal([]byte(line), &doc) != nil {
			continue
		}
		if doc["type"] == "response.completed" {
			if resp, ok := doc["response"].(map[string]any); ok {
				completed = resp
			}
		}
		if doc["type"] == "response.output_text.delta" {
			if delta, ok := doc["delta"].(string); ok {
				outputText.WriteString(delta)
			}
		}
		if choices, ok := doc["choices"].([]any); ok && len(choices) > 0 {
			choice, _ := choices[0].(map[string]any)
			if delta, ok := choice["delta"].(map[string]any); ok {
				if part, ok := delta["content"].(string); ok {
					chat.WriteString(part)
				}
				if part, ok := delta["reasoning_content"].(string); ok {
					reasoning.WriteString(part)
				}
				appendStreamTools(tools, delta["tool_calls"])
			}
			if msg, ok := choice["message"].(map[string]any); ok {
				if part, ok := msg["content"].(string); ok && part != "" {
					chat.Reset()
					chat.WriteString(part)
				}
			}
		}
	}
	if completed != nil {
		return completed
	}
	if outputText.Len() > 0 {
		return map[string]any{
			"output": []any{map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": outputText.String()}},
			}},
		}
	}
	if chat.Len() > 0 || reasoning.Len() > 0 || len(tools) > 0 {
		msg := map[string]any{"role": "assistant", "content": chat.String()}
		if reasoning.Len() > 0 {
			msg["reasoning_content"] = reasoning.String()
		}
		if calls := streamToolCalls(tools); len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		return map[string]any{"choices": []any{map[string]any{"message": msg}}}
	}
	return nil
}

type streamTool struct {
	id   string
	name string
	args strings.Builder
}

// 把流式工具调用增量按 index 拼进已有的工具调用。
// 参数 dst（map[int]*streamTool）：追加流工具使用的map[int]*streamTool；raw（any）：JSON 里读出的动态值。数字、字符串和对象都要接住，类型不符时按零值而不是 panic。
// 返回：无。流式工具调用增量已按 index 拼进已有的工具调用。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func appendStreamTools(dst map[int]*streamTool, raw any) {
	list, _ := raw.([]any)
	for _, item := range list {
		call, _ := item.(map[string]any)
		idx := 0
		switch n := call["index"].(type) {
		case float64:
			idx = int(n)
		case int:
			idx = n
		}
		tool := dst[idx]
		if tool == nil {
			tool = &streamTool{}
			dst[idx] = tool
		}
		if id, ok := call["id"].(string); ok && id != "" {
			tool.id = id
		}
		fn, _ := call["function"].(map[string]any)
		if name, ok := fn["name"].(string); ok && name != "" {
			tool.name = name
		}
		if args, ok := fn["arguments"].(string); ok {
			tool.args.WriteString(args)
		}
	}
}

// 把拼好的流式工具调用收成响应里的 tool_calls 数组。
// 参数 tools（map[int]*streamTool）：流工具Calls使用的map[int]*streamTool。
// 返回 []any（[]any）：把拼好的流式工具调用收成响应里的 tool_calls 数组。没有行时为空切片。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func streamToolCalls(tools map[int]*streamTool) []any {
	if len(tools) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(tools))
	for idx := range tools {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	out := make([]any, 0, len(indexes))
	for _, idx := range indexes {
		tool := tools[idx]
		out = append(out, map[string]any{
			"id":   tool.id,
			"type": "function",
			"function": map[string]any{
				"name":      tool.name,
				"arguments": tool.args.String(),
			},
		})
	}
	return out
}

// 粗略判断正文是不是 SSE，避免把事件流当成普通 JSON 解析。
// 参数 raw（[]byte）：原始正文。可能是 JSON，也可能是 SSE，由调用方按内容解析。
// 返回 bool（bool）：正文像 SSE：含 data:，并且像事件流或聊天块时为真。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func looksLikeEventStream(raw []byte) bool {
	s := string(raw)
	return strings.Contains(s, "data:") && (strings.Contains(s, "event:") || strings.Contains(s, "\"choices\""))
}

// 把正文解析成 JSON。空正文或解析失败时为 nil。
// 参数 raw（[]byte）：原始正文。可能是 JSON，也可能是 SSE，由调用方按内容解析。
// 返回 any（any）：解析后的 JSON。空正文或解析失败时为 nil。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func jsonDocument(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

// 把值序列化成 JSON 文本。nil 或失败时为空串。
// 参数 v（any）：JSON 里读出的动态值。数字、字符串和对象都要接住，类型不符时按零值而不是 panic。
// 返回 string（string）：序列化后的 JSON 文本。值是 nil 或序列化失败时为空串。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func mustJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// 就地打码敏感头。Authorization、Cookie 和各类 api-key 变成星号。
// 参数 h（map[string]string）：字符串到字符串的表。缺键表示这项没有填，不要补成空 JSON。
// 返回：无。Authorization、Cookie 和各类 api-key 已就地换成星号。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func redactHeaders(h map[string]string) {
	for k, v := range h {
		if sensitiveHeader(k) {
			h[k] = "***"
			continue
		}
		h[k] = redactSecretText(v)
	}
}

// 判断这个头是不是凭证或 Cookie。
// 参数 name（string）：HTTP 头名称。
// 返回 bool（bool）：这个头会携带凭证或 Cookie，必须打码时为真。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func sensitiveHeader(name string) bool {
	name = strings.ToLower(name)
	if strings.Contains(name, "token") || strings.Contains(name, "secret") || strings.Contains(name, "password") || strings.Contains(name, "api-key") || strings.Contains(name, "apikey") || strings.Contains(name, "authorization") {
		return true
	}
	switch name {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "x-litellm-api-key", "x-goog-api-key":
		return true
	default:
		return false
	}
}

// redactRequestURI 复制请求路径和查询参数，将常见凭据参数的值替换；r 为原始 HTTP 请求。
// 返回脱敏后的请求 URI；非法查询不保存查询串，不修改实际请求，也不声称识别所有敏感文本。
func redactRequestURI(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	u := *r.URL
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		u.RawQuery = ""
		u.ForceQuery = false
		return u.RequestURI()
	}
	for key, values := range query {
		lower := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "api-key") || strings.Contains(lower, "apikey") || strings.Contains(lower, "authorization") {
			query[key] = []string{"***"}
			continue
		}
		for i, value := range values {
			values[i] = redactSecretText(value)
		}
	}
	u.RawQuery = query.Encode()
	u.ForceQuery = false
	return u.RequestURI()
}

// 短密钥要求单词边界以保留 task-1；较长密钥即使紧贴其他字符也需要掩盖。
var promptSecret = regexp.MustCompile(`(?:\bsk-[A-Za-z0-9_\-]+|sk-[A-Za-z0-9_\-]{16,})`)

// 把文本里的密钥样式替换成星号。
// 参数 s（string）：即将写入日志的一行，可能含 bearer、sk- 或上游 URL。
// 返回 string（string）：打码后的文本。看起来像密钥的片段换成星号。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func redactSecretText(s string) string {
	return promptSecret.ReplaceAllString(s, "***")
}
