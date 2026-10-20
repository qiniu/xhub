package iam

import (
	"context"
	"net/url"
	"strings"
	"xorm.io/xorm"
)

// saveEnterpriseOIDC 在调用方事务中写入独立 SSO 配置；in 已经网关校验，返回错误，提升版本使旧回调失效。
func saveEnterpriseOIDC(s *xorm.Session, in EnterpriseOIDC) error {
	if in.Protocol == "" {
		in.Protocol = "oidc"
	}
	if in.Protocol != "oidc" && in.Protocol != "saml" {
		return ErrInvalid
	}
	_, err := s.Exec(`INSERT INTO enterprise_oidc(connection_id,name,issuer,client_id,secret,redirect_uri,return_uri,identity_claim,enabled,protocol,allow_http,idp_metadata,idp_metadata_url,certificate,allow_expired_idp_certificate) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(connection_id) DO UPDATE SET name=EXCLUDED.name,issuer=EXCLUDED.issuer,client_id=EXCLUDED.client_id,secret=EXCLUDED.secret,redirect_uri=EXCLUDED.redirect_uri,return_uri=EXCLUDED.return_uri,identity_claim=EXCLUDED.identity_claim,enabled=EXCLUDED.enabled,protocol=EXCLUDED.protocol,allow_http=EXCLUDED.allow_http,idp_metadata=EXCLUDED.idp_metadata,idp_metadata_url=EXCLUDED.idp_metadata_url,certificate=EXCLUDED.certificate,allow_expired_idp_certificate=EXCLUDED.allow_expired_idp_certificate,revision=enterprise_oidc.revision+1`, in.ConnectionID, in.Name, in.Issuer, in.ClientID, in.Secret, in.RedirectURI, in.ReturnURI, in.IdentityClaim, in.Enabled, in.Protocol, in.AllowHTTP, in.IDPMetadata, in.IDPMetadataURL, in.Certificate, in.AllowExpiredIDPCertificate)
	return err
}

// CreateEnterpriseOIDC 独立创建组织 SSO；orgID/in 为目标及配置，无需 SCIM，重复配置拒绝，失败回滚，返回脱密配置或错误。
func (db *DB) CreateEnterpriseOIDC(ctx context.Context, by Actor, orgID string, in EnterpriseOIDC) (*EnterpriseOIDC, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, ErrInvalid
	}
	in.Name = strings.TrimSpace(in.Name)
	err := db.tx(ctx, func(s *xorm.Session) error {
		var org Organization
		if err := get(s.SQL("SELECT * FROM organizations WHERE id=? FOR UPDATE", orgID), &org); err != nil {
			return err
		}
		if org.Status != StatusActive {
			return ErrInactive
		}
		var d DirectoryConnection
		found, err := s.SQL("SELECT * FROM directory_connections WHERE organization_id=? FOR UPDATE", orgID).Get(&d)
		if err != nil {
			return err
		}
		if !found {
			// 内部身份锚点不是 SCIM 配置，不签发同步 token，也不出现在 SCIM 列表。
			d = DirectoryConnection{ID: newID(), OrganizationID: orgID, Name: in.Name, TokenHash: HashKey(newID()), Revision: 1, SCIMConfigured: false}
			if _, err := s.Insert(&d); err != nil {
				return err
			}
		}
		exists, err := s.Where("connection_id=?", d.ID).Exist(new(EnterpriseOIDC))
		if err != nil {
			return err
		}
		if exists {
			return ErrConflict
		}
		in.ConnectionID = d.ID
		// 表单默认实体标识是待保存占位值；使用已锁定的连接 ID 生成唯一 SP EntityID，避免不同组织覆盖 IdP 注册。
		// 管理员显式填写其他绝对 URI 时保留原值，由数据库唯一索引阻止重复 SAML 实体。
		if in.Protocol == "saml" {
			if origin, err := url.Parse(in.ReturnURI); err == nil {
				origin.Path = "/auth/enterprise/saml/metadata"
				if in.ClientID == origin.String() {
					origin.Path = "/auth/enterprise/" + d.ID + "/saml/metadata"
					in.ClientID = origin.String()
				}
			}
		}
		in.Revision = 1
		if err := saveEnterpriseOIDC(s, in); err != nil {
			return err
		}
		return writeAudit(s, by, Audit{Action: "enterprise.oidc.create", ObjectType: "enterprise_oidc", ObjectID: d.ID})
	})
	return &in, err
}

// EnterpriseOIDCConfigurations 返回已保存 SSO 列表；管理员列表调用，无写入，Secret 字段不可 JSON 序列化。
func (db *DB) EnterpriseOIDCConfigurations(ctx context.Context) ([]EnterpriseOIDC, error) {
	out := []EnterpriseOIDC{}
	err := db.Engine.Context(ctx).Asc("connection_id").Find(&out)
	return out, err
}

// DeleteEnterpriseOIDC 删除 SSO、登录事务及绑定；管理员确认弹窗调用，不修改 SCIM、本地用户或会话，失败回滚。
func (db *DB) DeleteEnterpriseOIDC(ctx context.Context, by Actor, id string) error {
	return db.tx(ctx, func(s *xorm.Session) error {
		if err := get(s.SQL("SELECT * FROM directory_connections WHERE id=? FOR UPDATE", id), new(DirectoryConnection)); err != nil {
			return err
		}
		if err := get(s.SQL("SELECT * FROM enterprise_oidc WHERE connection_id=? FOR UPDATE", id), new(EnterpriseOIDC)); err != nil {
			return err
		}
		for _, table := range []string{"enterprise_login_transactions", "enterprise_login_identities", "enterprise_oidc"} {
			if _, err := s.Exec("DELETE FROM "+table+" WHERE connection_id=?", id); err != nil {
				return err
			}
		}
		return writeAudit(s, by, Audit{Action: "enterprise.oidc.delete", ObjectType: "enterprise_oidc", ObjectID: id})
	})
}
