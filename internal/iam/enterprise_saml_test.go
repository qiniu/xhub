package iam

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestEnterpriseSAMLTransactions 验证 SAML POST 暂存和浏览器 GET 单次消费；前置私有 PostgreSQL schema。
// 覆盖正常身份、空身份、重复/过期/版本变化及错误浏览器；重复迁移保留协议数据，夹具自动删除 schema。
func TestEnterpriseSAMLTransactions(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	by := Actor{Kind: "master"}
	org, err := db.CreateOrg(ctx, by, "SAML transaction", nil)
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.CreateEnterpriseOIDC(ctx, by, org.ID, EnterpriseOIDC{Name: "SAML", Protocol: "saml", AllowHTTP: true, AllowExpiredIDPCertificate: true, IDPMetadata: "public XML", IDPMetadataURL: "https://idp.example/meta", Certificate: "public certificate", Issuer: "https://idp.example", ClientID: "urn:xhub:test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = db.Migrate(ctx); err != nil {
			t.Fatal("重复迁移失败", err)
		}
	}
	persisted, err := db.EnterpriseOIDCByID(ctx, config.ConnectionID)
	if err != nil || persisted.Protocol != "saml" || !persisted.AllowHTTP || !persisted.AllowExpiredIDPCertificate || persisted.IDPMetadata != config.IDPMetadata || persisted.Certificate != config.Certificate {
		t.Fatal("迁移丢失SAML配置", err)
	}
	for _, mode := range []string{"valid", "expired", "revision", "disabled", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			saved := *persisted
			saved.Enabled = true
			saved.Protocol = "saml"
			if err = db.SaveEnterpriseOIDC(ctx, by, saved); err != nil {
				t.Fatal(err)
			}
			current, err := db.EnterpriseOIDCByID(ctx, config.ConnectionID)
			if err != nil {
				t.Fatal(err)
			}
			state := "saml-state-" + mode
			tx := EnterpriseLoginTransaction{StateHash: HashKey(state), ConnectionID: config.ConnectionID, Revision: current.Revision, BrowserHash: HashKey("original-browser"), Nonce: "signed-request-id", ExpiresAt: time.Now().Add(time.Minute)}
			if err = db.StartEnterpriseLogin(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if err := db.StageEnterpriseSAML(ctx, state, current.Revision, ""); !errors.Is(err, ErrInvalid) {
				t.Fatal("空身份未拒绝", err)
			}
			if mode == "expired" {
				_, err = db.Engine.Exec("UPDATE enterprise_login_transactions SET expires_at=now()-interval '1 minute' WHERE state_hash=?", tx.StateHash)
			}
			if mode == "revision" {
				err = db.SaveEnterpriseOIDC(ctx, by, *current)
			}
			if mode == "disabled" {
				_, err = db.Engine.Exec("UPDATE enterprise_oidc SET enabled=false WHERE connection_id=?", config.ConnectionID)
			}
			if mode == "oidc" {
				_, err = db.Engine.Exec("UPDATE enterprise_oidc SET protocol='oidc' WHERE connection_id=?", config.ConnectionID)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = db.StageEnterpriseSAML(ctx, state, current.Revision, "employee-001")
			if mode != "valid" {
				if !errors.Is(err, ErrInactive) {
					t.Fatal("不可用认证事务被暂存", mode, err)
				}
				return
			}
			if err != nil {
				t.Fatal("合法身份暂存失败", err)
			}
			if err = db.StageEnterpriseSAML(ctx, state, current.Revision, "different"); !errors.Is(err, ErrInactive) {
				t.Fatal("回调重放覆盖身份", err)
			}
			staged, err := db.EnterpriseSAMLTransaction(ctx, state)
			if err != nil || staged.SAMLSubject != "employee-001" {
				t.Fatal("暂存身份丢失", err)
			}
			if _, err = db.ConsumeEnterpriseLogin(ctx, state, "other-browser"); !errors.Is(err, ErrInactive) {
				t.Fatal("错误浏览器可以消费", err)
			}
			consumed, err := db.ConsumeEnterpriseLogin(ctx, state, "original-browser")
			if err != nil || consumed.SAMLSubject != "employee-001" {
				t.Fatal("合法浏览器消费失败", err)
			}
			if _, err = db.ConsumeEnterpriseLogin(ctx, state, "original-browser"); !errors.Is(err, ErrInactive) {
				t.Fatal("最终GET可以重放", err)
			}
		})
	}
}

// TestEnterpriseSAMLUniqueEntity 验证默认 SP 实体按连接生成且自定义重复被拒绝；前置私有 PostgreSQL schema。
// 两组织自动标识不冲突，非法重复更新回滚，不影响原 OIDC 配置；testDB 最终清理 schema。
func TestEnterpriseSAMLUniqueEntity(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	by := Actor{Kind: "master"}
	var configs []*EnterpriseOIDC
	for _, name := range []string{"first", "second"} {
		org, err := db.CreateOrg(ctx, by, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := db.CreateEnterpriseOIDC(ctx, by, org.ID, EnterpriseOIDC{Name: name, Protocol: "saml", ClientID: "http://console.example/auth/enterprise/saml/metadata", ReturnURI: "http://console.example/ui/login"})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ClientID != "http://console.example/auth/enterprise/"+cfg.ConnectionID+"/saml/metadata" {
			t.Fatal("默认SP实体不是连接唯一值")
		}
		configs = append(configs, cfg)
	}
	second := *configs[1]
	second.ClientID = configs[0].ClientID
	if err := db.SaveEnterpriseOIDC(ctx, by, second); err == nil {
		t.Fatal("重复SP实体被接受")
	}
	actual, err := db.EnterpriseOIDCByID(ctx, second.ConnectionID)
	if err != nil || actual.ClientID != configs[1].ClientID {
		t.Fatal("重复实体更新未回滚", err)
	}
}
