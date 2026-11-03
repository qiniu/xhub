package family

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRejectServiceAux 验证匹配表中的辅助操作明确失败；前置内存HTTP且不配置存储，响应不包含请求内容。
// 每个模板同时覆盖正常ID和尾斜杠；没有全局状态或持久化数据，无需清理。
func TestRejectServiceAux(t *testing.T) {
	if len(unsupportedServiceAuxRoutes) != 26 {
		t.Fatal("C1精确范围发生变化，必须复核独立真实回归清单")
	}
	for _, route := range unsupportedServiceAuxRoutes {
		method, path, _ := strings.Cut(route, " ")
		for _, name := range []string{"policy_id", "policy_name", "attachment_id", "model_id", "prompt_id"} {
			path = strings.ReplaceAll(path, "{"+name+"}", "fixture")
		}
		for _, suffix := range []string{"", "/"} {
			t.Run(strings.ReplaceAll(route, "/", "|")+" suffix="+strings.ReplaceAll(suffix, "/", "slash"), func(t *testing.T) {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(method, path+suffix, strings.NewReader(`{"private":"do-not-echo"}`))
				if !rejectServiceAux(w, r) || w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "unsupported_operation") || strings.Contains(w.Body.String(), "do-not-echo") {
					t.Fatal("辅助操作没有明确拒绝", w.Code, w.Body.String())
				}
			})
		}
	}
}

// TestRejectServiceAuxPreservesOtherRoutes 验证普通配置ID、方法差异、前缀碰撞和额外路径不会被辅助拒绝接管。
// 前置无存储内存HTTP，预期返回false且不写头/正文；未注册路径仍交由生产注册器处理404，无清理副作用。
func TestRejectServiceAuxPreservesOtherRoutes(t *testing.T) {
	for _, route := range []string{
		"POST /policies", "GET /policies/list", "GET /policy/list", "GET /policy/info/example",
		"GET /policies/status", "PUT /policies/status", "DELETE /policies/test-pipeline",
		"GET /credentials/by_name/migrate-encryption", "PATCH /credentials/migrate-encryption",
		"GET /prompts/versions", "PUT /prompts/versions", "POST /prompts/test",
		"GET /policies/fixture/status", "POST /policies/fixture/versions", "GET /policies/extra/fixture/resolved-guardrails",
		"GET /prompts//versions", "GET /prompts/one/two/versions", "POST /v1/policies/resolve",
		"POST /v1/chat/completions", "POST /v1/responses", "POST /guardrails/test_custom_code",
	} {
		t.Run(route, func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			w := httptest.NewRecorder()
			if rejectServiceAux(w, httptest.NewRequest(method, path, nil)) || w.Body.Len() != 0 || len(w.Header()) != 0 {
				t.Fatal("非目标入口被接管", route)
			}
		})
	}
}
