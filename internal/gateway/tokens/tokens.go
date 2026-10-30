// tokens.go counts tokens locally and lists the OpenAI parameters a model
// supports. A failed count returns an error instead of reporting zero.

package tokens

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/llm/estimate"
	"github.com/sunqirui1987/xhub/internal/util"
)

var logTraceOnceTokens sync.Once

// tokenCounter serves POST /utils/token_counter. A body without prompt, messages, or contents returns 400. The count matches the local tokenizer and does not call a provider.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 tokens.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *handler) tokenCounter(w http.ResponseWriter, r *http.Request) {
	logTraceOnceTokens.Do(func() { util.Trace("enter gateway.tokenCounter") })

	util.SetCallID(w, util.CallID())
	if s.host.RequireMixed(w, r) == nil {
		return
	}
	body := readMap(r)
	model := str(body["model"])
	prompt, _ := body["prompt"].(string)
	messages, _ := body["messages"].([]any)
	_, hasContents := body["contents"]
	if prompt == "" && messages == nil && !hasContents {
		util.WriteError(w, 400, "invalid_request", "prompt or messages or contents must be provided")
		return
	}
	var msgs []map[string]any
	for _, item := range messages {
		if m, ok := item.(map[string]any); ok {
			msgs = append(msgs, m)
		}
	}
	if messages != nil && msgs == nil {
		msgs = []map[string]any{}
	}
	used := s.modelUsedForCount(model)
	total, kind, err := estimate.CountTokens(used, prompt, msgs)
	if err != nil {
		util.WriteError(w, 500, "internal", err.Error())
		return
	}
	util.WriteJSON(w, 200, map[string]any{
		"total_tokens":   total,
		"request_model":  model,
		"model_used":     used,
		"tokenizer_type": kind,
	})
}

// modelUsedForCount 从宿主提供的独立目录选择计数所用型号，不再由调用方嵌套加锁。
// 参数 requestModel（string）：模型名。对外名用来选部署，上游名写进转发正文。
// 返回 string（string）：用来计数的部署上游模型名。没有对上的部署时用请求里的模型名。
// 调用：仅在 tokens.go 内使用
// 测试：无直接单测
func (s *handler) modelUsedForCount(requestModel string) string {
	for _, m := range s.host.Models() {
		if m.ModelName != requestModel {
			continue
		}
		return estimate.ModelUsedForCount(requestModel, str(m.LiteLLMParams["model"]))
	}
	return requestModel
}

// supportedOpenAIParams serves GET /utils/supported_openai_params with a model query.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 tokens.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *handler) supportedOpenAIParams(w http.ResponseWriter, r *http.Request) {
	util.SetCallID(w, util.CallID())
	if s.host.RequireMixed(w, r) == nil {
		return
	}
	model := r.URL.Query().Get("model")
	if model == "" {
		util.WriteError(w, 400, "invalid_request", "model required")
		return
	}
	used := s.modelUsedForCount(model)
	_, known := catalog.CostMap()[used]
	body := map[string]any{
		"supported_openai_params": estimate.OpenAISupportedParams(used, known),
	}
	if saved, err := s.store.GetKV("transform", "last"); err == nil {
		body["last_transform"] = saved
	}
	util.WriteJSON(w, 200, body)
}

// transformRequest stores the rewritten request so the transform page can read it back from the parameter list.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文。
// 调用：仅在 tokens.go 内使用
// 测试：无直接单测
// 返回：无。状态码和正文写进调用方的响应。
func (s *handler) transformRequest(w http.ResponseWriter, r *http.Request) {
	util.SetCallID(w, util.CallID())
	if s.host.RequireMixed(w, r) == nil {
		return
	}
	body := readMap(r)
	model := str(body["model"])
	if model == "" {
		util.WriteError(w, 400, "invalid_request", "model required")
		return
	}
	out := map[string]any{
		"model":       model,
		"transformed": true,
		"name":        str(body["name"]),
		"request":     body,
	}
	raw, _ := json.Marshal(out)
	if err := s.store.PutKV("transform", "last", string(raw)); err != nil {
		util.WriteError(w, 500, "internal", err.Error())
		return
	}
	util.WriteJSON(w, 200, out)
}

// Host 提供混合鉴权和受锁保护的部署读取，不暴露整个 Server。
type Host interface {
	RequireMixed(http.ResponseWriter, *http.Request) *auth.Principal
	Models() []config.ModelEntry
}

// Store 只提供转换记录所需的 KV 读写，错误由 HTTP 处理器返回。
type Store interface {
	GetKV(string, string) (map[string]any, error)
	PutKV(string, string, string) error
}

// handler 组合鉴权、模型快照和转换存储依赖。
type handler struct {
	host  Host
	store Store
}

// Module serves the local token counter and the supported-parameter list.
// 参数 host：鉴权和模型快照接口；store：转换记录的 KV 存储。
// 返回 util.Module（util.Module）：可挂到网关上的模块。
// 调用：仅在 routes.go 内使用
// 测试：无直接单测
func Module(host Host, store Store) util.Module {
	s := &handler{host: host, store: store}
	return util.Bind("tokens", func(reg util.Registrar) {
		reg.Handle("POST /utils/token_counter", s.tokenCounter)
		reg.Handle("GET /utils/supported_openai_params", s.supportedOpenAIParams)
		reg.Handle("POST /utils/transform_request", s.transformRequest)
	})
}
