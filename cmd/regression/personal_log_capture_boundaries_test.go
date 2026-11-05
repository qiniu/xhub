package regression

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUserContractPersonalCaptureAsyncFailure 通过真实创建/查询接口验证 HTTP 200 和 502 两类失败任务。
// 私有数据库保留初始响应，详情 error 必须来自最终失败，且只保留一条零费用记录；harness 与本地上游自动清理。
func TestUserContractPersonalCaptureAsyncFailure(t *testing.T) {
	for _, status := range []int{200, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					writeJSON(w, map[string]any{"id": "capture-task", "status": "queued"})
					return
				}
				w.WriteHeader(status)
				writeJSON(w, map[string]any{"status": "failed", "error": map[string]any{"code": "render_failed", "message": "Synthetic render failure."}})
			}))
			t.Cleanup(up.Close)
			model := "volcengine/capture-task-model"
			dep := seedanceDeployment(model, "ark_contents_generation")
			dep.LiteLLMParams["api_base"] = up.URL
			h := newHarness(t, dep)
			admin := h.adminSession()
			h.ok(http.MethodPut, "/account/log-capture", admin, map[string]any{"enabled": true})
			created := h.ok(http.MethodPost, "/api/v3/contents/generations/tasks", admin, map[string]any{"model": model, "content": []any{map[string]any{"type": "text", "text": "Synthetic task."}}})
			id := created.header("x-litellm-call-id")
			poll := h.do(http.MethodGet, "/api/v3/contents/generations/tasks/capture-task", admin, nil)
			if poll.status != status {
				t.Fatalf("任务轮询状态错误: %s", poll.describe())
			}
			detail := logDetail(t, h, admin, id)
			if detail["status"] != "failed" || detail["body_capture_status"] != "saved" || !strings.Contains(fmt.Sprint(detail["response"]), "queued") || !strings.Contains(fmt.Sprint(detail["error"]), "render_failed") {
				t.Fatalf("任务最终错误被初始响应覆盖: %v", detail)
			}
			if rows := h.spendLogs(t, admin); len(rows) != 1 || numberOrZero(rows[0]["spend"]) != 0 {
				t.Fatalf("失败任务增加日志或费用: %v", rows)
			}
		})
	}
}

// TestUserContractPersonalCaptureSettingsTimeout 在私有数据库锁住配置表，验证真实设置接口按时返回 503，恢复后返回真实状态。
// 持锁事务在超时或正常路径均释放，HTTP 协程结束后由 harness 清理 schema；不使用线上账号。
func TestUserContractPersonalCaptureSettingsTimeout(t *testing.T) {
	h := openHarness(t, false)
	admin := h.adminSession()
	h.ok(http.MethodGet, "/account/log-capture", admin, nil)
	tx := h.store.Engine.NewSession()
	defer tx.Close()
	if err := tx.Begin(); err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("LOCK TABLE proxy_config IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan reply, 1)
	go func() { done <- h.do(http.MethodGet, "/account/log-capture", admin, nil) }()
	select {
	case response := <-done:
		if response.status != 503 {
			t.Fatalf("查询故障伪装为正常设置: %s", response.describe())
		}
	case <-time.After(2500 * time.Millisecond):
		_ = tx.Rollback()
		<-done
		t.Fatal("记录设置查询没有按时降级")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := h.ok(http.MethodGet, "/account/log-capture", admin, nil).json(); got["global_enabled"] != false {
		t.Fatalf("恢复后状态错误: %v", got)
	}
}
