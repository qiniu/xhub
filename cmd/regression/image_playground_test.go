package regression

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunqirui1987/xhub/internal/config"
)

// TestImagePlaygroundParameters 验证调试台图片正文经真实普通/Bypass网关到上游仍保留尺寸、数量及扩展字段。
// 参数 t 为测试上下文；前置独立 PostgreSQL、本地严格图片渠道和无限额普通 Key；覆盖400透传、模型替换、鉴权及日志用量。
// 有限额会话的图片费用无法构造可信上界，必须在外发前明确拒绝；不能把该拒绝当作非法尺寸透传已通过。
// 返回无；本地服务 defer 关闭，账单与部署随 harness 私有 schema 清理，不使用外部凭据。
func TestImagePlaygroundParameters(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var doc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.URL.Path != "/v1/images/generations" || doc["model"] != "real-image" || r.Header.Get("Authorization") != "Bearer local-key" {
			t.Errorf("图片路径、模型替换或上游鉴权错误: %s %+v", r.URL.Path, doc)
		}
		if doc["size"] != "1024x1024" {
			w.WriteHeader(400)
			writeJSON(w, map[string]any{"error": map[string]any{"code": "invalid_request", "message": "image size is not available for this channel"}})
			return
		}
		if doc["n"] != float64(1) || doc["seed"] != float64(42) || doc["prompt"] != "hello" {
			t.Errorf("图片原生参数丢失: %+v", doc)
		}
		writeJSON(w, map[string]any{"data": []any{map[string]any{"b64_json": "AAAA"}}, "usage": map[string]any{"input_tokens": 8, "output_tokens": 2}})
	}))
	defer up.Close()
	for _, tc := range []struct{ transport, path string }{
		{"openai_image_generation", "/v1/images/generations"},
		{"bypass_openai_image_generation", "/bypass/openai/v1/images/generations"},
	} {
		t.Run(tc.transport, func(t *testing.T) {
			endpointType := "image_generation"
			if tc.transport == "bypass_openai_image_generation" {
				endpointType = "bypass:openai-images"
			}
			h := newHarness(t, config.ModelEntry{ModelName: "image-playground", LiteLLMParams: map[string]any{"model": "real-image", "custom_llm_provider": "openai", "api_base": up.URL, "api_key": "local-key", "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}, ModelInfo: map[string]any{"transport": tc.transport, "endpoint_types": []string{endpointType}}})
			admin := h.adminSession()
			owner := h.openScope(t, admin, "image-unlimited")
			body := map[string]any{"model": "image-playground", "prompt": "hello", "n": 1, "seed": 42}
			if denied := h.do("POST", tc.path, "", body); denied.status != 401 {
				t.Fatalf("缺失网关鉴权未拒绝: %s", denied.describe())
			}
			for _, size := range []any{nil, "", "2048x2048"} {
				body["size"] = size
				before := calls.Load()
				failed := h.do("POST", tc.path, owner.key, body)
				if failed.status != 400 || !strings.Contains(failed.text(), "image size is not available") || calls.Load() != before+1 {
					t.Fatalf("渠道非法尺寸错误未保留: %s", failed.describe())
				}
			}
			body["size"] = "1024x1024"
			success := h.ok("POST", tc.path, owner.key, body)
			if len(success.json()["data"].([]any)) != 1 {
				t.Fatal("图片响应契约丢失")
			}
			// 配置 Redis 时成功账单先进入队列；读取 PostgreSQL 日志前显式推进本测试产生的记录。
			h.flushSpend()
			log := h.ok("GET", "/spend/logs/ui/"+success.header("x-litellm-call-id"), admin, nil).json()
			if log["model"] != "image-playground" || !nearlyEqual(numberOrZero(log["spend"]), 0.000012) {
				t.Fatalf("图片日志或计量错误: %+v", log)
			}
			// 软预算对有限额会话同样放行图片生成，仍按模型实际用量计费。
			limited := h.personalBillingSession()
			before := calls.Load()
			allowed := h.do("POST", tc.path, limited, body)
			if allowed.status != 200 || allowed.header("X-XHub-Budget-Guarantee") != "soft" || calls.Load() != before+1 || !nearlyEqual(parseFloatOrZero(allowed.header("x-litellm-response-cost")), 0.000012) {
				t.Fatalf("有限额图片软预算放行或计费错误: %s", allowed.describe())
			}
		})
	}
}
