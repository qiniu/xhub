package family

import (
	"github.com/sunqirui1987/xhub/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// iamBoundaryHost 只提供可配置鉴权，未实现存储等方法；拒绝路径如误访问存储会直接失败，无外部资源。
type iamBoundaryHost struct {
	Host
	status int
}

// RequireManage 模拟原鉴权状态；非200写拒绝响应并返回nil，200返回身份，仅供边界单测。
func (h iamBoundaryHost) RequireManage(w http.ResponseWriter, r *http.Request) *auth.Principal {
	if h.status != 200 {
		w.WriteHeader(h.status)
		return nil
	}
	return &auth.Principal{UserID: "admin"}
}

// TestIAMFallbackPath 验证根资源与版本前缀边界；避免误拦预算/用户等其他域，无数据需清理。
func TestIAMFallbackPath(t *testing.T) {
	for _, path := range []string{"/team/block", "/organization/info", "/v2/organization/id", "/api/v1/team/id/", "/team"} {
		if !isIAMFallbackPath(path) {
			t.Fatal("未识别IAM旧入口", path)
		}
	}
	for _, path := range []string{"", "/", "/v2", "/budget/list", "/teams", "/foo/team", "/user/team", "/teamwork"} {
		if isIAMFallbackPath(path) {
			t.Fatal("误拦其他域", path)
		}
	}
}

// TestIAMFallbackAuthorization 验证未授权状态不变、授权后不解析正文或访问存储便返回明确501；内存HTTP无外部清理。
func TestIAMFallbackAuthorization(t *testing.T) {
	for _, status := range []int{401, 403, 200} {
		w := httptest.NewRecorder()
		ServeMgmt(iamBoundaryHost{status: status}, w, httptest.NewRequest("POST", "/team/block", strings.NewReader("invalid json")))
		want := status
		if status == 200 {
			want = 501
		}
		if w.Code != want {
			t.Fatal("鉴权/拒绝顺序错误", status, w.Code)
		}
		if status == 200 && !strings.Contains(w.Body.String(), `"type":"unsupported_iam_operation"`) {
			t.Fatal("缺少明确错误", w.Body.String())
		}
	}
}
