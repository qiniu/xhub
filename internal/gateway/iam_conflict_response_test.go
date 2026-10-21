package gateway

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestIAMWrappedConflictResponse 验证原始及数据库包装冲突均返回409；仅内存请求，不依赖数据库或清理资源。
func TestIAMWrappedConflictResponse(t *testing.T) {
	for _, err := range []error{iam.ErrConflict, fmt.Errorf("%w: users_email", iam.ErrConflict)} {
		w := httptest.NewRecorder()
		(&Server{}).writeIAMError(w, httptest.NewRequest("POST", "/user/new", nil), err)
		if w.Code != 409 {
			t.Fatalf("重复数据应返回409，实际%d", w.Code)
		}
	}
}
