package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAnthropicTokenCountContract 验证真实网关计数的路径、模型映射、鉴权、零费用与错误持久化。
// 参数 t 为回归上下文；前置隔离 PostgreSQL 与本地上游，覆盖两种入口和原厂拒绝；harness 清理 schema，defer 关闭服务。
func TestAnthropicTokenCountContract(t *testing.T) {
	for _, code := range []int{200, 402, 403, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if r.URL.Path != "/v1/messages/count_tokens" || body["model"] != "vendor-claude" || r.Header.Get("X-Api-Key") != "supplier-secret" || r.Header.Get("Authorization") != "" {
					t.Error("计数原生契约或凭据错误")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				if code == 200 {
					fmt.Fprint(w, `{"input_tokens":123}`)
				} else {
					fmt.Fprint(w, `{"error":{"type":"vendor_denied","message":"Token count refused"}}`)
				}
			}))
			defer up.Close()
			dep := chatDeployment("count-public")
			dep.LiteLLMParams["model"] = "vendor-claude"
			dep.LiteLLMParams["api_base"] = up.URL
			dep.LiteLLMParams["api_key"] = "supplier-secret"
			dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
			dep.ModelInfo["transport"] = "bypass_anthropic_messages"
			dep.ModelInfo["endpoint_types"] = []string{"messages", "bypass:anthropic-messages"}
			h := newHarness(t, dep)
			admin := h.adminSession()
			scope := h.openScope(t, admin, "token-count")
			deniedKey := h.modelKey(t, scope.session, scope.teamID, "count-denied", "another-model")
			if got := h.do("GET", "/v1/models/count-public", deniedKey, nil); got.status != 404 {
				t.Fatalf("不可见模型泄漏: %s", got.describe())
			}
			if got := h.do("POST", "/v1/messages/count_tokens", deniedKey, map[string]any{"model": "count-public", "messages": []any{}}); got.status != 401 {
				t.Fatalf("计数绕过模型权限: %s", got.describe())
			}
			if got := h.do("POST", "/v1/messages/count_tokens", scope.key, map[string]any{}); got.status != 400 {
				t.Fatalf("缺少模型未拒绝: %s", got.describe())
			}
			discovered := h.ok("GET", "/v1/models/count-public", scope.key, nil).json()
			if discovered["id"] != "count-public" || discovered["object"] != "model" || len(discovered) != 4 {
				t.Fatalf("模型发现元数据不正确: %+v", discovered)
			}
			for _, tc := range []struct {
				path, key string
				code      int
			}{{"/v1/models/missing", scope.key, 404}, {"/v1/models/count-public", "", 401}} {
				if got := h.do("GET", tc.path, tc.key, nil); got.status != tc.code {
					t.Fatalf("模型查询隔离错误: %s", got.describe())
				}
			}
			for _, path := range []string{"/v1/messages/count_tokens", "/bypass/anthropic/v1/messages/count_tokens"} {
				res := h.do("POST", path, scope.key, map[string]any{"model": "count-public", "messages": []any{map[string]any{"role": "user", "content": "Count this input."}}})
				if res.status != code {
					t.Fatalf("计数状态与上游不一致: %s", res.describe())
				}
				id := res.header("X-Litellm-Call-Id")
				detail := h.ok("GET", "/spend/logs/ui/"+id, admin, nil).json()
				if stringField(detail, "request_id") != id {
					t.Fatalf("计数日志未关联: %+v", detail)
				}
				if firstFloat(detail, "spend") != 0 || firstFloat(detail, "prompt_tokens") != 0 || firstFloat(detail, "completion_tokens") != 0 {
					t.Fatalf("计数不得按生成计费: %+v", detail)
				}
				if code != 200 && (detail["error"] != res.text() || detail["metadata"].(map[string]any)["http_status"] != float64(code)) {
					t.Fatalf("日志状态与客户端错误不一致: %+v", detail)
				}
			}
			if res := h.do("POST", "/v1/messages/count_tokens", "", map[string]any{"model": "count-public"}); res.status != 401 {
				t.Fatalf("匿名计数未拒绝: %s", res.describe())
			}
		})
	}
}
