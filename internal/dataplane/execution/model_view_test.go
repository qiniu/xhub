package execution

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	providerregistry "github.com/sunqirui1987/xhub/internal/provider/registry"
	"github.com/sunqirui1987/xhub/internal/router"
)

// modelViewHost 在策略读取边界模拟管理更新，隔离测试目录选择与模板校验的同版本契约。
// 只覆盖 logHost 的策略读取，不模拟生产权限或预算；真实安全行为由 HTTP regression 验证。
type modelViewHost struct {
	*logHost
	captured []config.ModelEntry
}

// RouteSettingsFor 捕获执行器传入的目录，再更新进程目录；参数 p 无额外授权意义，models 为选路输入。
// 返回内置策略，无存储副作用；测试以确定的函数边界替代时间等待，验证候选没有二次读取全局目录。
func (h *modelViewHost) RouteSettingsFor(_ *auth.Principal, models []config.ModelEntry) router.RouteSettings {
	h.captured = models
	h.cfg.ModelList[0].LiteLLMParams["model"] = "updated-model"
	return router.BuiltinSettings()
}

// TestExecutionUsesValidatedModelView 验证标准和原生入口的候选与策略校验共用目录，后续请求读取更新。
// 前置本地 HTTP 上游及内存宿主；分别检查两次真实出站型号，不覆盖鉴权实现；Cleanup 关闭上游。
func TestExecutionUsesValidatedModelView(t *testing.T) {
	for _, bypass := range []bool{false, true} {
		name := "standard"
		if bypass {
			name = "bypass"
		}
		t.Run(name, func(t *testing.T) {
			var received []string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				received = append(received, body["model"].(string))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"chat_view","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			}))
			t.Cleanup(up.Close)
			cfg := &config.Config{ModelList: []config.ModelEntry{{ModelName: "public", LiteLLMParams: map[string]any{"model": "original-model", "api_base": up.URL + "/v1", "api_key": "fixture", "custom_llm_provider": "openai"}, ModelInfo: map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"chat", "bypass:openai-chat"}}}}}
			h := &modelViewHost{logHost: newLogHost(cfg, up.Client())}
			for _, expected := range []string{"original-model", "updated-model"} {
				rec := httptest.NewRecorder()
				path := "/v1/chat/completions"
				if bypass {
					path = "/bypass/openai/v1/chat/completions"
				}
				req := httptest.NewRequest("POST", path, bytes.NewBufferString(`{"model":"public","messages":[{"role":"user","content":"Reply briefly."}]}`))
				req.Header.Set("Content-Type", "application/json")
				if bypass {
					hit, ok := providerregistry.Match("POST", path)
					if !ok {
						t.Fatal("测试原生路径未登记")
					}
					ServeBypass(h, rec, req, hit)
				} else {
					Serve(h, rec, req, "chat")
				}
				if rec.Code != 200 || len(received) == 0 || received[len(received)-1] != expected || h.captured[0].ParamString("model", "") != expected {
					t.Fatalf("策略/执行目录混读: status=%d wire=%v body=%s", rec.Code, received, rec.Body.String())
				}
			}
			if len(received) != 2 {
				t.Fatalf("意外重复调用: %v", received)
			}
		})
	}
}
