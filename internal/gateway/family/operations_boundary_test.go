package family

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
)

// operationHost 仅提供鉴权；未实现存储和运行时方法，使误执行目录读写立即失败，测试无外部资源。
type operationHost struct {
	Host
	status int
}

// RequireManage 模拟原匿名/成员/管理员结果；失败直接写状态，成功返回身份，仅供边界测试。
func (h operationHost) RequireManage(w http.ResponseWriter, r *http.Request) *auth.Principal {
	if h.status != 200 {
		w.WriteHeader(h.status)
		return nil
	}
	return &auth.Principal{UserID: "admin"}
}

// TestOperationalBoundary 验证明确路径表、鉴权先于拒绝且无正文解析/存储访问；内存HTTP无外部清理。
func TestOperationalBoundary(t *testing.T) {
	for _, path := range []string{"/coordination_redis/settings", "/coordination_redis/settings/test", "/debug/asyncio-tasks", "/debug/memory/details", "/debug/memory/summary", "/debug/memory/gc/configure", "/memory-usage", "/memory-usage-in-mem-cache", "/memory-usage-in-mem-cache-items", "/health/backlog", "/health/drain", "/health/history", "/health/license", "/health/shared-status"} {
		for _, status := range []int{401, 403, 200} {
			w := httptest.NewRecorder()
			ServeMgmt(operationHost{status: status}, w, httptest.NewRequest("POST", path+"/", strings.NewReader("invalid json and secret")))
			want := status
			if want == 200 {
				want = 501
			}
			if w.Code != want {
				t.Fatal("鉴权或拒绝错误", path, status, w.Code)
			}
			if status == 200 && (!strings.Contains(w.Body.String(), `"type":"unsupported_operation"`) || strings.Contains(w.Body.String(), "secret")) {
				t.Fatal("错误类型或脱敏错误", w.Body.String())
			}
		}
	}
	for _, path := range []string{"", "/health", "/health/readiness", "/health/readiness/details", "/health/liveness", "/health/services", "/health/test_connection", "/debugger", "/health/drain/other", "/budget/list", "/cache/settings"} {
		if unsupportedOperationalPath(path) {
			t.Fatal("误拦其他接口", path)
		}
	}
}
