package health

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEnterpriseUIConfig 前置未配置、停用及可用 SSO 回调；验证公开启动配置不误开启跳转。
// 无外部服务或数据库，响应记录器在测试结束释放，不产生持久化数据。
func TestEnterpriseUIConfig(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		h := handler{sso: func(*http.Request) bool { return enabled }}
		rec := httptest.NewRecorder()
		h.uiConfig(rec, httptest.NewRequest("GET", "/get/ui_config", nil))
		var body map[string]any
		if json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["sso_configured"] != enabled || body["auto_redirect_to_sso"] != false {
			t.Fatal("SSO 启动状态错误", body)
		}
	}
	rec := httptest.NewRecorder()
	(&handler{}).uiConfig(rec, httptest.NewRequest("GET", "/get/ui_config", nil))
	var body map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["sso_configured"] != false {
		t.Fatal("缺失 SSO 配置未安全关闭", body)
	}
}

// TestProbeBoundaries 前置成功或失败的数据库探测，验证存活不依赖数据库、就绪失败返回 503。
// 参数 t 为测试上下文；内存处理器不建立连接，测试环境变量由 t.Setenv 恢复。
func TestProbeBoundaries(t *testing.T) {
	for _, failed := range []bool{false, true} {
		h := handler{ping: func() error {
			if failed {
				return errors.New("offline")
			}
			return nil
		}}
		live := httptest.NewRecorder()
		h.healthLive(live, httptest.NewRequest("GET", "/health/liveness", nil))
		if live.Code != 200 {
			t.Fatalf("存活探测错误: %d", live.Code)
		}
		ready := httptest.NewRecorder()
		h.healthReady(ready, httptest.NewRequest("GET", "/health/readiness", nil))
		want := 200
		if failed {
			want = 503
		}
		if ready.Code != want {
			t.Fatalf("就绪探测错误: got=%d want=%d", ready.Code, want)
		}
	}
	t.Setenv("XHUB_PUBLIC_ORIGIN", " https://gateway.example/// ")
	if PublicOrigin() != "https://gateway.example" {
		t.Fatalf("公开地址未规范化: %s", PublicOrigin())
	}
	t.Setenv("XHUB_PUBLIC_ORIGIN", "")
	if PublicOrigin() != LocalGatewayOrigin {
		t.Fatal("缺省地址错误")
	}
}

// TestReadinessDetailsTruth 验证详情每次实际探测数据库、失败脱敏503、恢复后原成功字段不变；纯内存，无外部清理。
func TestReadinessDetailsTruth(t *testing.T) {
	calls := 0
	offline := false
	h := handler{version: "test-version", ping: func() error {
		calls++
		if offline {
			return errors.New("private-dsn-password")
		}
		return nil
	}}
	for _, failed := range []bool{false, true, false} {
		offline = failed
		w := httptest.NewRecorder()
		h.healthDetails(w, httptest.NewRequest("GET", "/health/readiness/details", nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if failed {
			if w.Code != 503 {
				t.Fatal("数据库失败仍报告就绪", w.Body.String())
			}
			e, _ := body["error"].(map[string]any)
			if e["type"] != "not_ready" || e["message"] != "database is not ready" {
				t.Fatal("错误应脱敏", body)
			}
		} else if w.Code != 200 || body["status"] != "ready" || body["litellm_version"] != "test-version" || body["healthy_count"] != float64(1) || body["unhealthy_count"] != float64(0) {
			t.Fatal("成功契约改变", body)
		}
	}
	if calls != 3 {
		t.Fatal("详情未逐次探测数据库", calls)
	}
}
