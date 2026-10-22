package regression

import (
	"errors"
	"net/http"
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestUserEnterpriseIdentityContract 经真实 HTTP 用户接口和隔离 PostgreSQL 验证企业身份绑定、权限、唯一性、改绑和 SSO 解析。
// 测试用户及企业配置由 harness 私有 schema 创建，覆盖失败回滚与旧会话失效，harness 在用例结束时清理所有数据。
func TestUserEnterpriseIdentityContract(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	org, err := h.db.CreateOrg(t.Context(), iam.Actor{Kind: "master"}, "Identity API regression", nil)
	if err != nil {
		t.Fatal(err)
	}
	config, err := h.db.CreateEnterpriseOIDC(t.Context(), iam.Actor{Kind: "master"}, org.ID, iam.EnterpriseOIDC{Name: "Regression SSO", Issuer: "https://identity.example.test", ClientID: "regression", RedirectURI: "https://console.example.test/auth/enterprise/callback", ReturnURI: "https://console.example.test/ui/login", IdentityClaim: "uid", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	created := h.ok(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "identity-bound@example.invalid", "password": "identity-password", "user_role": "user", "sso_connection_id": config.ConnectionID, "sso_external_id": "ldap-employee-1"}).json()
	userID := firstString(created, "user_id")
	if userID == "" {
		t.Fatalf("user/new missing id: %+v", created)
	}
	info := h.ok(http.MethodGet, "/user/info?user_id="+userID, admin, nil).json()["user_info"].(map[string]any)
	if info["sso_connection_id"] != config.ConnectionID || info["sso_external_id"] != "ldap-employee-1" || info["sso_binding_managed"] != false {
		t.Fatalf("管理员读取绑定不一致: %+v", info)
	}
	if r := h.do(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "partial@example.invalid", "password": "identity-password", "sso_connection_id": config.ConnectionID}); r.status != http.StatusBadRequest {
		t.Fatalf("不成对字段应返回 400: %s", r.describe())
	}
	if r := h.do(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "unknown@example.invalid", "password": "identity-password", "sso_connection_id": "missing", "sso_external_id": "unknown"}); r.status != http.StatusNotFound {
		t.Fatalf("未知连接应返回 404: %s", r.describe())
	}
	if r := h.do(http.MethodPost, "/user/new", admin, map[string]any{"user_email": "duplicate@example.invalid", "password": "identity-password", "sso_connection_id": config.ConnectionID, "sso_external_id": "ldap-employee-1"}); r.status != http.StatusConflict {
		t.Fatalf("重复企业身份应返回 409: %s", r.describe())
	}
	if _, err := h.db.UserByEmail(t.Context(), "duplicate@example.invalid"); !errors.Is(err, iam.ErrNotFound) {
		t.Fatalf("重复身份失败仍创建用户: %v", err)
	}
	user, err := h.db.GetUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.EnterpriseLoginUser(t.Context(), config.ConnectionID, config.Issuer, "subject-before", "ldap-employee-1", config.Revision); err != nil {
		t.Fatalf("企业登录未按外部 ID 匹配: %v", err)
	}
	memberSession := h.login(user.Email, "identity-password")
	updated := h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": userID, "user_alias": "Updated atomically", "sso_connection_id": config.ConnectionID, "sso_external_id": "ldap-employee-2"}).json()["user_info"].(map[string]any)
	if updated["user_alias"] != "Updated atomically" || updated["sso_external_id"] != "ldap-employee-2" {
		t.Fatalf("用户字段和身份绑定未一起更新: %+v", updated)
	}
	if _, err := h.db.GetUser(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	if r := h.do(http.MethodGet, "/user/info?user_id="+userID, memberSession, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("改绑未使旧用户会话失效: %s", r.describe())
	}
	if _, err := h.db.EnterpriseLoginUser(t.Context(), config.ConnectionID, config.Issuer, "subject-old", "ldap-employee-1", config.Revision); !errors.Is(err, iam.ErrNotFound) {
		t.Fatalf("旧外部 ID 仍能匹配: %v", err)
	}
	if _, err := h.db.EnterpriseLoginUser(t.Context(), config.ConnectionID, config.Issuer, "subject-after", "ldap-employee-2", config.Revision); err != nil {
		t.Fatalf("新外部 ID 无法登录: %v", err)
	}
	clear := h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": userID, "sso_connection_id": "", "sso_external_id": ""}).json()["user_info"].(map[string]any)
	if clear["sso_connection_id"] != "" || clear["sso_external_id"] != "" {
		t.Fatalf("解绑响应仍包含身份: %+v", clear)
	}
	if _, err := h.db.EnterpriseLoginUser(t.Context(), config.ConnectionID, config.Issuer, "subject-last", "ldap-employee-2", config.Revision); !errors.Is(err, iam.ErrNotFound) {
		t.Fatalf("解除绑定后仍能通过 SSO 登录: %v", err)
	}
	if r := h.do(http.MethodPost, "/user/update", h.login(user.Email, "identity-password"), map[string]any{"user_id": userID, "sso_connection_id": config.ConnectionID, "sso_external_id": "self-bind"}); r.status != http.StatusForbidden {
		t.Fatalf("普通用户不应自行绑定企业身份: %s", r.describe())
	}
}
