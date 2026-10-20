package regression

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHardBudgetAutomaticCacheTTL 验证真实有限额Key在无缓存声明时按上游实测5m/1h分桶结算。
// 前置隔离PG与本地Messages供应商，覆盖三协议JSON/SSE、精确扣费、预占释放、非法计量保留和显式TTL冲突。
// 所有正文为合成英文，harness清理schema、用户、Key，httptest服务由Cleanup关闭，不访问付费上游。
func TestHardBudgetAutomaticCacheTTL(t *testing.T) {
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	for _, tc := range []struct {
		name, protocol    string
		five, hour        int
		missing, explicit bool
	}{
		{"hour", "messages", 0, 5, false, false},
		{"mixed", "messages", 2, 3, false, false},
		{"mixed_chat", "chat", 2, 3, false, false},
		{"mixed_responses", "responses", 2, 3, false, false},
		{"missing_buckets", "messages", 2, 3, true, false},
		{"explicit_mismatch", "messages", 0, 5, false, true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				raw := map[string]any{"input_tokens": 8, "output_tokens": 2, "cache_creation_input_tokens": tc.five + tc.hour, "cache_creation": map[string]any{"ephemeral_5m_input_tokens": tc.five, "ephemeral_1h_input_tokens": tc.hour}}
				if tc.missing {
					delete(raw, "cache_creation")
				}
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !tc.explicit && body["cache_control"] != nil {
						t.Error("网关擅自改写缓存期限")
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						encoded, _ := json.Marshal(raw)
						frames := strings.Replace(streamTerminalFrames("messages"), `"usage":{"input_tokens":8,"output_tokens":0}`, `"usage":`+string(encoded), 1)
						_, _ = io.WriteString(w, frames)
					} else {
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_auto_cache", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "TERMINAL_OK"}}, "stop_reason": "end_turn", "usage": raw})
					}
				}))
				t.Cleanup(up.Close)
				dep := hardBudgetDeployment("automatic-cache-" + tc.name)
				dep.LiteLLMParams["api_base"] = up.URL
				dep.LiteLLMParams["custom_llm_provider"] = "anthropic"
				dep.LiteLLMParams["input_cost_per_token"] = 1e-6
				dep.LiteLLMParams["output_cost_per_token"] = 2e-6
				dep.LiteLLMParams["cache_creation_input_token_cost"] = 3e-6
				dep.LiteLLMParams["cache_creation_input_token_cost_above_1hr"] = 4e-6
				dep.ModelInfo["transport"] = "bypass_anthropic_messages"
				dep.ModelInfo["endpoint_types"] = []string{"messages", "chat", "responses"}
				h := newHarness(t, dep)
				admin := h.adminSession()
				owner := h.openScope(t, admin, dep.ModelName)
				h.setKeyBudget(t, admin, owner.key, 1)
				body := chatRequest(dep.ModelName, "Verify automatic cache billing.")
				body["stream"] = stream
				body["max_tokens"] = 64
				path := "/v1/messages"
				if tc.protocol == "chat" {
					path = "/v1/chat/completions"
				}
				if tc.protocol == "responses" {
					path = "/v1/responses"
					delete(body, "messages")
					delete(body, "max_tokens")
					body["input"] = "Verify automatic cache billing."
					body["max_output_tokens"] = 64
				}
				if tc.explicit {
					body["cache_control"] = map[string]any{"type": "ephemeral", "ttl": "5m"}
				}
				got := h.ok(http.MethodPost, path, owner.key, body)
				if !strings.Contains(got.text(), "TERMINAL_OK") {
					t.Fatalf("缓存写入阻断正文: %s", got.describe())
				}
				key, err := h.db.GetKey(t.Context(), owner.keyID)
				if err != nil {
					t.Fatal(err)
				}
				holds, err := h.db.PendingKeyBudget(t.Context(), owner.keyID)
				if err != nil {
					t.Fatal(err)
				}
				if tc.missing || tc.explicit {
					if len(holds) != 1 || holds[0].State != "uncertain" || key.Spend != 0 {
						t.Fatalf("缓存不确定事实被错误结算: holds=%+v spend=%g", holds, key.Spend)
					}
				} else {
					expected := 12e-6 + float64(tc.five)*3e-6 + float64(tc.hour)*4e-6
					if len(holds) != 0 || !nearlyEqual(key.Spend, expected) {
						t.Fatalf("缓存TTL实测费用或预占释放错误: holds=%+v spend=%g want=%g", holds, key.Spend, expected)
					}
				}
			})
		}
	}
}
