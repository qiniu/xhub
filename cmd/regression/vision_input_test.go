package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// visionRequest 构造含文字前后缀的真实客户请求；参数为协议、模型和流标记，返回对象，无外部数据或副作用。
func visionRequest(protocol, model string, stream bool) map[string]any {
	image := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AQID"}}
	key, text, capField := "messages", "text", "max_tokens"
	if protocol == "openai-responses" {
		key, text, capField = "input", "input_text", "max_output_tokens"
		image = map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AQID"}
	}
	if protocol == "anthropic-messages" {
		image = map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AQID"}}
	}
	return map[string]any{"model": model, "stream": stream, capField: 128, key: []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": text, "text": "Before"}, image, map[string]any{"type": text, "text": "After"}}}}}
}

// TestVisionInputProtocolMatrix 验证三种客户/上游协议在硬额度下的 JSON/SSE 图片完整性、失败契约、护栏和实测账单归属。
// 前置真实网关、独立 PostgreSQL schema 和有限额个人 Key，本地上游独立校验图片字节/MIME/顺序；Cleanup 关闭服务并删除 schema。
func TestVisionInputProtocolMatrix(t *testing.T) {
	protocols := []string{"openai-chat", "openai-responses", "anthropic-messages"}
	transports := []string{"bypass_openai_chat", "bypass_openai_responses", "bypass_anthropic_messages"}
	for index, target := range protocols {
		t.Run(target, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("上游未收到 JSON")
					w.WriteHeader(400)
					return
				}
				key := "messages"
				if target == "openai-responses" {
					key = "input"
				}
				messages, _ := body[key].([]any)
				if len(messages) != 1 {
					t.Error("图片消息丢失或被拆散")
					w.WriteHeader(400)
					return
				}
				parts, _ := messages[0].(map[string]any)["content"].([]any)
				if len(parts) != 3 {
					t.Error("图片前后文字或图片块丢失")
					w.WriteHeader(400)
					return
				}
				first, last := parts[0].(map[string]any), parts[2].(map[string]any)
				if (first["text"] != "Before" && first["text"] != "[REDACTED]") || last["text"] != "After" {
					t.Error("图文次序改变")
					w.WriteHeader(400)
					return
				}
				image := parts[1].(map[string]any)
				valid := false
				switch target {
				case "openai-chat":
					valid = image["type"] == "image_url" && image["image_url"].(map[string]any)["url"] == "data:image/png;base64,AQID"
				case "openai-responses":
					valid = image["type"] == "input_image" && image["image_url"] == "data:image/png;base64,AQID"
				case "anthropic-messages":
					source := image["source"].(map[string]any)
					valid = image["type"] == "image" && source["type"] == "base64" && source["media_type"] == "image/png" && source["data"] == "AQID"
				}
				if !valid {
					t.Error("上游图片字节、MIME 或协议不正确")
					w.WriteHeader(400)
					return
				}
				if body["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, frame := range endpointStreamFrames(target) {
						fmt.Fprintf(w, "data: %s\n\n", frame)
					}
				} else {
					result, err := llmdialogue.EncodeDialogueResult(llmdialogue.DialogueResult{ID: "vision", Text: "matrix-ok", Stop: "stop", Usage: map[string]any{"prompt_tokens": float64(8), "completion_tokens": float64(2)}}, target, "real")
					if err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "application/json")
					w.Write(result)
				}
			}))
			t.Cleanup(upstream.Close)
			dep := hardBudgetDeployment("vision-model")
			dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
			dep.ModelInfo["transport"] = transports[index]
			dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "messages"}
			h := newHarness(t, dep)
			admin := h.adminSession()
			owner := h.openScope(t, admin, "vision-owner")
			// 允许图片不等于放宽硬额度：整窗余额不足仍须在任何供应商请求之前拒绝。
			h.setKeyBudget(t, admin, owner.key, 0.000001)
			if r := h.do("POST", "/v1/messages", owner.key, visionRequest("anthropic-messages", dep.ModelName, false)); r.status != 429 || calls.Load() != 0 {
				t.Fatalf("图片请求绕过余额限制: %s", r.describe())
			}
			h.setKeyBudget(t, admin, owner.key, 1)
			for _, source := range protocols {
				for _, stream := range []bool{false, true} {
					body := visionRequest(source, dep.ModelName, stream)
					path := expectedLogEndpoint(source, false)
					response := h.do("POST", path, owner.key, body)
					if response.status != 200 || !strings.Contains(response.text(), "matrix-ok") {
						t.Fatalf("%s -> %s stream=%v 识图协议失败: %s", source, target, stream, response.describe())
					}
					if response.header("X-XHub-Budget-Input-Bound") != "1024" {
						t.Fatalf("图片请求未经过硬额度整窗预占: %s", response.describe())
					}
					h.flushSpend()
					holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
					if err != nil || len(holds) != 0 {
						t.Fatalf("图片调用结算后仍有预占: %+v %v", holds, err)
					}
					bill := h.ok("GET", "/spend/logs/ui/"+response.header("x-litellm-call-id"), admin, nil).json()
					if bill["status"] != "success" || bill["api_key"] != owner.keyID || bill["spend"] != float64(0.000032) {
						t.Fatalf("图片请求日志/账单归属错误: %v", bill)
					}
				}
			}
			before := calls.Load()

			if r := h.do("POST", "/v1/responses", "", visionRequest("openai-responses", dep.ModelName, false)); r.status != 401 {
				t.Fatal("图片请求绕过鉴权")
			}
			bad := visionRequest("openai-responses", dep.ModelName, false)
			bad["input"].([]any)[0].(map[string]any)["content"] = []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,???"}}
			if r := h.do("POST", "/v1/responses", owner.key, bad); r.status != 400 {
				t.Fatalf("错误图片未返回400: %s", r.describe())
			}
			if calls.Load() != before {
				t.Fatal("错误输入仍发到上游")
			}
			h.putGuardrail(t, "block-vision", guardrailRow("block-vision", kindBlockedWords, []string{"Before"}, ""))
			if r := h.do("POST", "/v1/responses", owner.key, visionRequest("openai-responses", dep.ModelName, false)); r.status != 400 || calls.Load() != before {
				t.Fatal("图片请求绕过文本护栏")
			}
			h.ok("DELETE", "/guardrails/block-vision", admin, nil)
			h.putGuardrail(t, "redact-vision", guardrailRow("redact-vision", kindRedact, []string{"Before"}, "redact"))
			if r := h.do("POST", "/v1/responses", owner.key, visionRequest("openai-responses", dep.ModelName, false)); r.status != 200 {
				t.Fatalf("图文脱敏破坏图片: %s", r.describe())
			}
		})
	}
}
