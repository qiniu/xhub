package iam

import (
	"errors"
	"testing"
	"time"
)

// TestSAMLRefreshPersistence 验证独立调度的持久化边界；真实私有 PostgreSQL，配置不改变认证版本。
// 覆盖缺省、非法/并发设置、租约到期、迟到提交、失败保留、成功及级联删除；夹具最后删除 schema。
func TestSAMLRefreshPersistence(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	by := Actor{Kind: "master"}
	org, err := db.CreateOrg(ctx, by, "refresh-persistence", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateEnterpriseOIDC(ctx, by, org.ID, EnterpriseOIDC{Name: "fixture", Protocol: "saml", ClientID: "urn:refresh:test", Issuer: "urn:idp", IDPMetadata: "original", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	read, err := db.SAMLRefreshByID(ctx, c.ConnectionID)
	if err != nil || read.Enabled || read.IntervalHours != 6 {
		t.Fatal("缺省非关闭", read, err)
	}
	settings := SAMLRefresh{Enabled: true, SourceURL: "https://idp.example/metadata", IntervalHours: 6}
	wrong := *c
	wrong.Revision++
	if err = db.SaveSAMLRefresh(ctx, by, &wrong, settings); !errors.Is(err, ErrConflict) {
		t.Fatal("旧版本保存成功", err)
	}
	bad := settings
	bad.IntervalHours = 25
	if err = db.SaveSAMLRefresh(ctx, by, c, bad); !errors.Is(err, ErrConflict) {
		t.Fatal("非法间隔成功", err)
	}
	if err = db.SaveSAMLRefresh(ctx, by, c, settings); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal("重复迁移失败", err)
	}
	jobs, err := db.DueSAMLRefresh(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal("到期任务缺失", jobs, err)
	}
	claim, err := db.ClaimSAMLRefresh(ctx, c.ConnectionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimSAMLRefresh(ctx, c.ConnectionID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("租约重复", err)
	}
	if err = db.FinishSAMLRefresh(ctx, by, c, claim, "", "", time.Now().Unix()); !errors.Is(err, ErrInvalid) {
		t.Fatal("空成功覆盖元数据", err)
	}
	if err = db.FinishSAMLRefresh(ctx, by, c, claim, "", "fetch_failed", time.Now().Add(5*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	state, err := db.SAMLRefreshByID(ctx, c.ConnectionID)
	if err != nil || state.LastSuccess != 0 || state.ErrorCode != "fetch_failed" || state.Lease != "" {
		t.Fatal("失败状态错误", state, err)
	}
	current, _ := db.EnterpriseOIDCByID(ctx, c.ConnectionID)
	if current.IDPMetadata != "original" || current.Revision != c.Revision {
		t.Fatal("失败修改材料或版本")
	}
	if _, err = db.ClaimSAMLRefresh(ctx, c.ConnectionID, false); !errors.Is(err, ErrConflict) {
		t.Fatal("未到期后台领取", err)
	}
	claim, err = db.ClaimSAMLRefresh(ctx, c.ConnectionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Engine.Exec("UPDATE enterprise_saml_refresh SET lease_until=0 WHERE connection_id=?", c.ConnectionID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := db.ClaimSAMLRefresh(ctx, c.ConnectionID, true)
	if err != nil || reclaimed.Lease == claim.Lease {
		t.Fatal("崩溃租约未恢复", err)
	}
	if err = db.FinishSAMLRefresh(ctx, by, c, claim, "late", "", time.Now().Unix()); !errors.Is(err, ErrConflict) {
		t.Fatal("过期租约覆盖", err)
	}
	// XML 并发更新即使 revision 未变也必须拒绝，防止两个不同快照互相覆盖。
	if _, err = db.Engine.Exec("UPDATE enterprise_oidc SET idp_metadata='changed' WHERE connection_id=?", c.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishSAMLRefresh(ctx, by, c, reclaimed, "late", "", time.Now().Unix()); !errors.Is(err, ErrConflict) {
		t.Fatal("旧 XML 快照覆盖", err)
	}
	current, _ = db.EnterpriseOIDCByID(ctx, c.ConnectionID)
	if err = db.FinishSAMLRefresh(ctx, by, current, reclaimed, "renewed", "", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	current, _ = db.EnterpriseOIDCByID(ctx, c.ConnectionID)
	state, _ = db.SAMLRefreshByID(ctx, c.ConnectionID)
	if current.IDPMetadata != "renewed" || current.Revision != c.Revision || state.LastSuccess == 0 || state.ErrorCode != "" {
		t.Fatal("成功状态或版本错误", state)
	}
	settings.Enabled = false
	if err = db.SaveSAMLRefresh(ctx, by, current, settings); err != nil {
		t.Fatal(err)
	}
	jobs, err = db.DueSAMLRefresh(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatal("关闭后仍调度", jobs, err)
	}
	if err = db.DeleteEnterpriseOIDC(ctx, by, c.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimSAMLRefresh(ctx, c.ConnectionID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("删除后仍有任务", err)
	}
	if err = db.SaveSAMLRefresh(ctx, by, c, settings); err == nil {
		t.Fatal("不存在连接保存成功")
	}
}

// TestSAMLRefreshStorageFailure 验证真实数据库连接关闭时读写及领取都报错；不使用 mock 或跳过，夹具清理私有 schema。
func TestSAMLRefreshStorageFailure(t *testing.T) {
	db := testDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := db.SAMLRefreshByID(ctx, "missing"); err == nil {
		t.Fatal("读取故障假成功")
	}
	if _, err := db.DueSAMLRefresh(ctx); err == nil {
		t.Fatal("扫描故障假成功")
	}
	if _, err := db.ClaimSAMLRefresh(ctx, "missing", true); err == nil {
		t.Fatal("领取故障假成功")
	}
}
