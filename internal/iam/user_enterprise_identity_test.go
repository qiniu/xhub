package iam

import (
	"context"
	"errors"
	"testing"
)

// TestEnterpriseIdentityLifecycle 验证用户绑定在隔离 PostgreSQL 中创建、SSO 首次匹配、改绑、解绑及冲突回滚。
// 所有用户、连接、登录映射和审计记录位于 testDB 专属 schema，测试结束自动删除，不依赖外部 IdP。
func TestEnterpriseIdentityLifecycle(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	by := Actor{Kind: "master"}
	org, err := db.CreateOrg(ctx, by, "Identity lifecycle", nil)
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.CreateEnterpriseOIDC(ctx, by, org.ID, EnterpriseOIDC{Name: "Identity SSO", Issuer: "https://identity.example.test", ClientID: "client", RedirectURI: "https://console.example.test/auth/enterprise/callback", ReturnURI: "https://console.example.test/ui/login", IdentityClaim: "uid", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(ctx, by, UserInput{Email: "linked@example.test", Password: "linked-password", EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: "ldap-user-1"}})
	if err != nil {
		t.Fatalf("创建并绑定用户失败: %v", err)
	}
	if user.SessionVersion != 1 {
		t.Fatalf("新账号初始会话版本异常: %d", user.SessionVersion)
	}
	identity, err := db.EnterpriseIdentityForUser(ctx, user.ID)
	if err != nil || identity == nil || identity.ConnectionID != config.ConnectionID || identity.ExternalID != "ldap-user-1" || identity.Managed {
		t.Fatalf("读取绑定失败: %+v %v", identity, err)
	}
	if _, err := db.EnterpriseLoginUser(ctx, config.ConnectionID, config.Issuer, "subject-old", "ldap-user-1", config.Revision); err != nil {
		t.Fatalf("首次 SSO 稳定身份匹配失败: %v", err)
	}
	current, err := db.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := db.AdminUpdateUser(ctx, by, user.ID, UserUpdate{EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: "ldap-user-1"}})
	if err != nil || unchanged.SessionVersion != current.SessionVersion {
		t.Fatalf("相同绑定不应使会话失效: %+v %v", unchanged, err)
	}
	updated, err := db.AdminUpdateUser(ctx, by, user.ID, UserUpdate{Name: stringPtrForTest("Updated"), EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: "ldap-user-2"}})
	if err != nil || updated.Name != "Updated" || updated.SessionVersion != current.SessionVersion+1 {
		t.Fatalf("改绑需和用户字段原子保存并使旧会话失效: %+v %v", updated, err)
	}
	if _, err := db.EnterpriseLoginUser(ctx, config.ConnectionID, config.Issuer, "subject-old", "ldap-user-1", config.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("旧账号 ID 不应继续匹配: %v", err)
	}
	if _, err := db.EnterpriseLoginUser(ctx, config.ConnectionID, config.Issuer, "subject-new", "ldap-user-2", config.Revision); err != nil {
		t.Fatalf("新账号 ID 匹配失败: %v", err)
	}
	other, err := db.CreateUser(ctx, by, UserInput{Email: "other@example.test", Password: "other-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AdminUpdateUser(ctx, by, other.ID, UserUpdate{EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: "ldap-user-2"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复企业 ID 应拒绝: %v", err)
	}
	stored, err := db.GetUser(ctx, other.ID)
	if err != nil || stored.SessionVersion != other.SessionVersion {
		t.Fatalf("重复绑定失败应回滚用户与会话: %+v %v", stored, err)
	}
	// 角色提升必须显式解绑，错误不能清除已有身份。
	if _, err := db.AdminUpdateUser(ctx, by, user.ID, UserUpdate{Role: stringPtrForTest(RoleAdmin)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("绑定账号提升角色未拒绝: %v", err)
	}
	stillBound, err := db.EnterpriseIdentityForUser(ctx, user.ID)
	if err != nil || stillBound == nil || stillBound.ExternalID != "ldap-user-2" {
		t.Fatalf("失败提升破坏身份: %+v %v", stillBound, err)
	}
	cleared, err := db.AdminUpdateUser(ctx, by, user.ID, UserUpdate{EnterpriseIdentity: &EnterpriseIdentityInput{}})
	if err != nil || cleared.SessionVersion != updated.SessionVersion+1 {
		t.Fatalf("解绑需使会话失效: %+v %v", cleared, err)
	}
	identity, err = db.EnterpriseIdentityForUser(ctx, user.ID)
	if err != nil || identity != nil {
		t.Fatalf("解绑后仍有身份记录: %+v %v", identity, err)
	}
	if _, err := db.EnterpriseLoginUser(ctx, config.ConnectionID, config.Issuer, "subject-new", "ldap-user-2", config.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("解绑后仍能通过 SSO 登录: %v", err)
	}
	// 并发人工编辑必须串行产生会话版本；删除已绑定账号需同步清除稳定身份和登录映射。
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, externalID := range []string{"concurrent-one", "concurrent-two"} {
		go func(value string) {
			<-start
			_, err := db.AdminUpdateUser(ctx, by, other.ID, UserUpdate{EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: value}})
			results <- err
		}(externalID)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("并发改绑失败: %v", err)
		}
	}
	concurrent, err := db.GetUser(ctx, other.ID)
	if err != nil || concurrent.SessionVersion != other.SessionVersion+2 {
		t.Fatalf("并发改绑丢失会话版本: %+v %v", concurrent, err)
	}
	binding, err := db.EnterpriseIdentityForUser(ctx, other.ID)
	if err != nil || binding == nil {
		t.Fatalf("并发改绑丢失身份: %+v %v", binding, err)
	}
	if _, err := db.EnterpriseLoginUser(ctx, config.ConnectionID, config.Issuer, "deleted-subject", binding.ExternalID, config.Revision); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(ctx, by, other.ID); err != nil {
		t.Fatalf("删除绑定用户失败: %v", err)
	}
	if _, err := db.EnterpriseLoginUser(ctx, config.ConnectionID, config.Issuer, "deleted-subject", binding.ExternalID, config.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("已删除用户仍匹配企业身份: %v", err)
	}

}

// stringPtrForTest 返回独立字符串指针，供增量字段测试组合提交；不访问数据库或共享状态。
func stringPtrForTest(value string) *string { return &value }

// TestEnterpriseIdentityRejectsInvalidTargets 覆盖字段不成对、管理员账号、未知/SCIM 连接及审计约束失败回滚。
// 每种资源均在 testDB 私有 schema 创建；schema 在测试结束自动清理，无外部服务依赖。
func TestEnterpriseIdentityRejectsInvalidTargets(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	by := Actor{Kind: "master"}
	org, err := db.CreateOrg(ctx, by, "Invalid identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.CreateEnterpriseOIDC(ctx, by, org.ID, EnterpriseOIDC{Name: "Identity SSO", Issuer: "https://identity.example.test", ClientID: "client", RedirectURI: "https://console.example.test/auth/enterprise/callback", ReturnURI: "https://console.example.test/ui/login", IdentityClaim: "uid", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		input UserInput
	}{
		{name: "unpaired", input: UserInput{Email: "unpaired@example.test", Password: "valid-password", EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID}}},
		{name: "platform administrator", input: UserInput{Email: "admin@example.test", Password: "valid-password", Role: RoleAdmin, EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: "admin"}}},
		{name: "unknown connection", input: UserInput{Email: "unknown@example.test", Password: "valid-password", EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: "missing", ExternalID: "unknown"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.CreateUser(ctx, by, tc.input); !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrNotFound) {
				t.Fatalf("目标应拒绝: %v", err)
			}
			if _, err := db.UserByEmail(ctx, tc.input.Email); !errors.Is(err, ErrNotFound) {
				t.Fatalf("失败创建留下了用户: %v", err)
			}
		})
	}
	team, err := db.CreateTeam(ctx, by, TeamInput{OrganizationID: org.ID, Name: "SCIM team"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Exec("UPDATE directory_connections SET scim_configured=true, enabled=true, default_team_id=? WHERE id=?", team.ID, config.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(ctx, by, UserInput{Email: "scim-rejected@example.test", Password: "valid-password", EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: "scim-id"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SCIM 连接不应接受人工绑定: %v", err)
	}
	if _, err := db.Engine.Exec("UPDATE directory_connections SET scim_configured=false, enabled=false, default_team_id=NULL WHERE id=?", config.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Engine.Context(ctx).Exec("ALTER TABLE audit_logs ADD CONSTRAINT reject_identity_create CHECK (action <> 'user.create')"); err != nil {
		t.Fatal(err)
	}
	_, err = db.CreateUser(ctx, by, UserInput{Email: "audit-failure@example.test", Password: "valid-password", EnterpriseIdentity: &EnterpriseIdentityInput{ConnectionID: config.ConnectionID, ExternalID: "audit-id"}})
	if err == nil {
		t.Fatal("审计失败应导致创建与身份绑定回滚")
	}
	if _, err := db.Engine.Exec("ALTER TABLE audit_logs DROP CONSTRAINT reject_identity_create"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UserByEmail(ctx, "audit-failure@example.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("审计失败后用户仍存在: %v", err)
	}
}
