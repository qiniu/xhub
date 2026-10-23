package gateway

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"

	dpexecution "github.com/sunqirui1987/xhub/internal/dataplane/execution"
	"github.com/sunqirui1987/xhub/internal/util"
)

// promptProbeRequest 是已保存文本提示词的测试契约；模型可显式覆盖保存值，变量仅替换模板，历史按原文追加。
// 本入口只支持非流式文本聊天；不将未实现的Dotprompt、历史版本或工具调用假装执行成功。
type promptProbeRequest struct {
	PromptID  string               `json:"prompt_id"`
	Model     string               `json:"model,omitempty"`
	Variables map[string]string    `json:"prompt_variables,omitempty"`
	History   []promptProbeMessage `json:"conversation_history,omitempty"`
	Stream    bool                 `json:"stream,omitempty"`
}

// promptProbeMessage 描述测试模板或历史中的纯文本消息，角色沿Chat协议发送；不接受会被静默丢弃的多模态或工具字段。
type promptProbeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var promptProbeVariable = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// promptTest 为管理界面执行已保存提示词；参数w/r沿用登录会话、请求头和取消上下文，结果直接写入w。
// 先检查管理权限及保存模板，再调用既有chat数据面以复用模型权限、预算、护栏、路由、日志和计费。
// 不另取供应商密钥、不选默认付费模型，也不拼造成功文案；读库失败和未支持输入均明确返回失败。
func (s *Server) promptTest(w http.ResponseWriter, r *http.Request) {
	if s.requireManage(w, r) == nil {
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		util.WriteError(w, 400, "invalid_request", "invalid prompt test request body")
		return
	}
	req, err := parsePromptProbeRequest(raw)
	if err != nil {
		util.WriteError(w, 400, "invalid_request", err.Error())
		return
	}
	if s.Store == nil {
		util.WriteError(w, 503, "unavailable", "prompt store unavailable")
		return
	}
	saved, err := s.Store.GetKV("prompts", req.PromptID)
	if errors.Is(err, sql.ErrNoRows) {
		util.WriteError(w, 404, "not_found", "prompt not found")
		return
	}
	if err != nil {
		// 不把SQL、DSN或提示词正文带入错误响应和日志。
		util.Error("prompt test store failure error_type=%T", err)
		util.WriteError(w, 500, "internal", "prompt store failure")
		return
	}
	body, err := buildPromptProbeChat(saved, req)
	if err != nil {
		util.WriteError(w, 400, "invalid_request", err.Error())
		return
	}
	chatRaw, err := json.Marshal(body)
	if err != nil {
		util.WriteError(w, 500, "internal", "could not encode prompt test request")
		return
	}
	// 只替换本次请求正文；保留/prompts/test路径、身份、取消和关联头，并禁响应缓存，不能用旧成功掩盖当前上游故障。
	chatRequest := r.Clone(dpexecution.WithoutResponseCache(r.Context()))
	chatRequest.Body = io.NopCloser(bytes.NewReader(chatRaw))
	chatRequest.ContentLength = int64(len(chatRaw))
	chatRequest.GetBody = nil
	chatRequest.Header.Set("Content-Type", "application/json")
	chatRequest.Header.Del("Content-Length")
	s.chat(w, chatRequest)
}

// parsePromptProbeRequest 严格解析测试请求；raw是完整JSON，返回已校验的请求或可公开参数错误，不触发外部调用。
// 测试入口调用它以拒绝无效JSON、未知字段、未实现格式和空ID，避免忽略用户配置却显示成功。
func parsePromptProbeRequest(raw []byte) (promptProbeRequest, error) {
	var req promptProbeRequest
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return req, errors.New("prompt test request must be a JSON object")
	}
	for _, name := range []string{"dotprompt_content", "version", "prompt_version"} {
		if _, exists := fields[name]; exists {
			return req, fmt.Errorf("%s is not supported by prompt tests", name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		return req, errors.New("invalid prompt test fields")
	}
	if strings.TrimSpace(req.PromptID) == "" {
		return req, errors.New("prompt_id required")
	}
	if model, exists := fields["model"]; exists && (bytes.Equal(model, []byte("null")) || strings.TrimSpace(req.Model) == "") {
		return req, errors.New("model must be a non-empty string")
	}
	if req.Stream {
		return req, errors.New("streaming prompt tests are not supported")
	}
	if err := validatePromptProbeMessages(req.History, true); err != nil {
		return req, err
	}
	return req, nil
}

// buildPromptProbeChat 将保存记录和请求渲染为标准Chat正文；仅接受纯文本messages/content、简单变量及明确的模型。
// 参数saved不被修改，req.History不做模板替换；返回正文/参数错误，由promptTest交回现有数据面做准入和实际推理。
func buildPromptProbeChat(saved map[string]any, req promptProbeRequest) (map[string]any, error) {
	for _, key := range []string{"dotprompt_content", "tools", "litellm_params"} {
		if value, exists := saved[key]; exists && value != nil {
			return nil, fmt.Errorf("saved %s is not supported by prompt tests", key)
		}
	}
	if version, exists := saved["version"]; exists && version != float64(1) && version != 1 {
		return nil, errors.New("prompt version selection is not supported")
	}
	model := req.Model
	if model == "" {
		model, _ = saved["model"].(string)
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("model required in saved prompt or test request")
	}
	var messages []promptProbeMessage
	if rawMessages, exists := saved["messages"]; exists {
		if _, alsoContent := saved["content"]; alsoContent {
			return nil, errors.New("saved prompt must use either messages or content")
		}
		raw, err := json.Marshal(rawMessages)
		if err != nil {
			return nil, errors.New("invalid saved prompt messages")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&messages) != nil {
			return nil, errors.New("saved prompt messages must contain only text role and content")
		}
	} else if content, ok := saved["content"].(string); ok {
		messages = []promptProbeMessage{{Role: "user", Content: content}}
	}
	if len(messages) == 0 {
		return nil, errors.New("saved prompt text messages required")
	}
	if err := validatePromptProbeMessages(messages, false); err != nil {
		return nil, err
	}
	for i := range messages {
		rendered, err := renderPromptProbeText(messages[i].Content, req.Variables)
		if err != nil {
			return nil, err
		}
		messages[i].Content = rendered
	}
	messages = append(messages, req.History...)
	body := map[string]any{"model": model, "messages": messages, "stream": false}
	if value, exists := saved["config"]; exists {
		config, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("saved prompt config must be an object")
		}
		if err := applyPromptProbeConfig(body, config); err != nil {
			return nil, err
		}
	}
	return body, nil
}

// validatePromptProbeMessages 校验纯文本角色和内容；history限制为user/assistant，模板还支持system/developer。
// 输入只读、返回首个参数错误；禁止空消息、多模态和工具伪造，空历史允许，模板非空由构建函数检查。
func validatePromptProbeMessages(messages []promptProbeMessage, history bool) error {
	for _, message := range messages {
		valid := message.Role == "user" || message.Role == "assistant"
		if !history {
			valid = valid || message.Role == "system" || message.Role == "developer"
		}
		if !valid || strings.TrimSpace(message.Content) == "" {
			return errors.New("prompt messages require a supported role and non-empty text content")
		}
	}
	return nil
}

// renderPromptProbeText 一次性替换模板中的{{name}}；参数variables为调用方提供的原文值，返回文本或缺变量/不支持语法错误。
// 不递归解释插入值，不支持条件、循环、路径或函数；这些边界显式报错，避免产生看似完成的错误提示词。
func renderPromptProbeText(template string, variables map[string]string) (string, error) {
	remaining := promptProbeVariable.ReplaceAllString(template, "")
	if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
		return "", errors.New("only simple {{name}} prompt variables are supported")
	}
	var missing string
	result := promptProbeVariable.ReplaceAllStringFunc(template, func(token string) string {
		name := promptProbeVariable.FindStringSubmatch(token)[1]
		value, ok := variables[name]
		if !ok && missing == "" {
			missing = name
		}
		return value
	})
	if missing != "" {
		return "", fmt.Errorf("missing prompt variable: %s", missing)
	}
	if strings.TrimSpace(result) == "" {
		return "", errors.New("rendered prompt message must not be empty")
	}
	return result, nil
}

// applyPromptProbeConfig 把已保存的有限文本生成参数复制到chat正文；参数body为待构建正文，config为JSON解码配置。
// 返回不支持或非法数值错误，不能静默丢弃配置；只修改本次body，不发请求或改保存记录。
func applyPromptProbeConfig(body, config map[string]any) error {
	for key, value := range config {
		number, ok := value.(float64)
		if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("invalid prompt config: %s", key)
		}
		switch key {
		case "temperature":
			ok = number >= 0 && number <= 2
		case "top_p":
			ok = number >= 0 && number <= 1
		case "max_tokens", "max_completion_tokens":
			ok = number > 0 && math.Trunc(number) == number
		default:
			return fmt.Errorf("unsupported prompt config: %s", key)
		}
		if !ok {
			return fmt.Errorf("invalid prompt config: %s", key)
		}
		body[key] = value
	}
	if _, first := config["max_tokens"]; first {
		if _, second := config["max_completion_tokens"]; second {
			return errors.New("prompt config must use only one token limit")
		}
	}
	return nil
}
