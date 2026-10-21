package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// createPasswordGate 仅提供管理员身份，其他依赖为空；验证无效密码在数据库访问前被拒绝。
type createPasswordGate struct{ Gate }

// RequireManage 为创建处理器提供测试管理员；参数为请求响应，无持久化副作用。
func (createPasswordGate) RequireManage(http.ResponseWriter, *http.Request) *auth.Principal {
	return &auth.Principal{Kind: authz.KindSession, Role: iam.RoleAdmin, UserID: "admin"}
}

// TestUserNewPasswordValidation 验证空、短、超长及多字节密码返回400且不回显密码；纯内存HTTP，无需清理。
func TestUserNewPasswordValidation(t *testing.T) {
	for _, password := range []string{"", "short", strings.Repeat("x", 73), strings.Repeat("密", 25)} {
		raw, _ := json.Marshal(map[string]any{"user_email": "test@example.invalid", "password": password})
		w := httptest.NewRecorder()
		UserNew(createPasswordGate{}, w, httptest.NewRequest("POST", "/user/new", strings.NewReader(string(raw))))
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"invalid_request"`) {
			t.Fatalf("非法密码未被正确拒绝: %d", w.Code)
		}
		if password != "" && strings.Contains(w.Body.String(), password) {
			t.Fatal("错误响应泄露密码")
		}
	}
}
