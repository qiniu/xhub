package regression

import (
	"net/http"
	"strings"
	"testing"
)

// TestUserCSVCreateContract 验证CSV使用的真实创建接口：密码边界返回400，合法行持久化且重复邮箱不重复创建。
// 前置私有PostgreSQL及管理员会话；测试结束由harness清理整个schema，不依赖外部模型。
func TestUserCSVCreateContract(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	for _, password := range []string{"", "short", strings.Repeat("x", 73)} {
		r := h.do(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "csv@example.invalid", "user_role": "user", "password": password})
		if r.status != 400 {
			t.Fatalf("非法初始密码应返回400，实际%d", r.status)
		}
	}
	body := map[string]any{"user_email": "csv@example.invalid", "user_role": "user", "password": "csv-password", "user_alias": "CSV User", "max_budget": 0}
	created := h.ok(http.MethodPost, "/user/new", admin, body).json()
	id := created["user_id"].(string)
	info := h.ok(http.MethodGet, "/user/info?user_id="+id, admin, nil).json()["user_info"].(map[string]any)
	if info["max_budget"] != float64(0) {
		t.Fatalf("零额度未持久化: %v", info["max_budget"])
	}
	if r := h.do(http.MethodPost, "/user/new", admin, body); r.status != 409 {
		t.Fatal("重复邮箱不应再次创建成功")
	}
}
