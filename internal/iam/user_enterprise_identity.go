package iam

import (
	"context"
	"strings"

	"xorm.io/xorm"
)

// EnterpriseIdentityInput 保存用户表单提交的企业登录绑定；两项同时为空表示解除绑定。
// ConnectionID 选择已配置的 SSO，ExternalID 是该连接内企业账号的稳定标识。
type EnterpriseIdentityInput struct {
	ConnectionID string
	ExternalID   string
}

// EnterpriseUserIdentity 是用户绑定的脱密视图；仅供平台管理员查看。
type EnterpriseUserIdentity struct {
	ConnectionID   string `xorm:"'sso_connection_id'" json:"sso_connection_id"`
	ConnectionName string `xorm:"'sso_connection_name'" json:"sso_connection_name"`
	ExternalID     string `xorm:"'sso_external_id'" json:"sso_external_id"`
	Managed        bool   `xorm:"'sso_binding_managed'" json:"sso_binding_managed"`
}

// setEnterpriseIdentity 在用户创建/更新事务中创建、改绑或解除企业身份；锁定 SSO 配置及目录锚点以串行化登录回调。
// 参数 s 为调用方事务，不由本函数关闭；userID 是目标用户，in 是完整绑定字段，两个空值表示解绑。
// 返回是否发生绑定变更及错误；未知 SSO、非普通用户、SCIM/组管理记录、重复 ID 或查询失败时返回错误，事务由调用方回滚。
func setEnterpriseIdentity(s *xorm.Session, userID string, in *EnterpriseIdentityInput) (bool, error) {
	if in == nil || strings.TrimSpace(userID) == "" {
		return false, ErrInvalid
	}
	normalized := *in
	in = &normalized
	in.ConnectionID = strings.TrimSpace(in.ConnectionID)
	in.ExternalID = strings.TrimSpace(in.ExternalID)
	if (in.ConnectionID == "") != (in.ExternalID == "") {
		return false, ErrInvalid
	}
	var current DirectoryUser
	found, err := s.Where("user_id = ?", userID).Get(&current)
	if err != nil {
		return false, err
	}
	connectionIDs := make([]string, 0, 2)
	if found {
		connectionIDs = append(connectionIDs, current.ConnectionID)
	}
	if in.ConnectionID != "" && (len(connectionIDs) == 0 || connectionIDs[0] != in.ConnectionID) {
		connectionIDs = append(connectionIDs, in.ConnectionID)
	}
	if len(connectionIDs) == 2 && connectionIDs[0] > connectionIDs[1] {
		connectionIDs[0], connectionIDs[1] = connectionIDs[1], connectionIDs[0]
	}
	// 连接配置写入先锁目录连接再锁 SSO；改绑沿用这个顺序，避免和配置更新互锁。
	for _, connectionID := range connectionIDs {
		var connection DirectoryConnection
		if err := get(s.SQL("SELECT * FROM directory_connections WHERE id = ? FOR UPDATE", connectionID), &connection); err != nil {
			return false, err
		}
		if connection.SCIMConfigured {
			return false, ErrInvalid
		}
		var config EnterpriseOIDC
		configErr := get(s.SQL("SELECT * FROM enterprise_oidc WHERE connection_id = ? FOR UPDATE", connectionID), &config)
		if connectionID == in.ConnectionID && configErr != nil {
			return false, configErr
		}
		if configErr != nil && configErr != ErrNotFound {
			return false, configErr
		}
	}
	// SCIM 也持有目录连接锁；拿锁后复查，拒绝等待期间失效的绑定快照。
	var latest DirectoryUser
	latestFound, err := s.Where("user_id = ?", userID).Get(&latest)
	if err != nil {
		return false, err
	}
	if latestFound != found || (found && (latest.ID != current.ID || latest.ConnectionID != current.ConnectionID || latest.ExternalID != current.ExternalID)) {
		return false, ErrConflict
	}
	if found {
		var groups struct{ Exists bool }
		if _, err := s.SQL("SELECT EXISTS (SELECT 1 FROM directory_group_members WHERE directory_user_id = ?) AS exists", current.ID).Get(&groups); err != nil {
			return false, err
		}
		if groups.Exists {
			return false, ErrInvalid
		}
	}
	if in.ConnectionID == "" {
		if !found {
			return false, nil
		}
		if _, err := s.Exec("DELETE FROM enterprise_login_identities WHERE connection_id = ? AND user_id = ?", current.ConnectionID, userID); err != nil {
			return false, err
		}
		if _, err := s.ID(current.ID).Delete(new(DirectoryUser)); err != nil {
			return false, mapErr(err)
		}
		return true, nil
	}
	var user User
	if err := get(s.ID(userID), &user); err != nil {
		return false, err
	}
	if user.Role != RoleUser {
		return false, ErrInvalid
	}
	if found && current.ConnectionID == in.ConnectionID && current.ExternalID == in.ExternalID {
		return false, nil
	}
	if found {
		if _, err := s.Exec("DELETE FROM enterprise_login_identities WHERE connection_id = ? AND user_id = ?", current.ConnectionID, userID); err != nil {
			return false, err
		}
		if _, err := s.Exec("UPDATE directory_users SET connection_id = ?, external_id = ?, active = true WHERE id = ?", in.ConnectionID, in.ExternalID, current.ID); err != nil {
			return false, mapErr(err)
		}
	} else {
		row := DirectoryUser{ID: newID(), ConnectionID: in.ConnectionID, ExternalID: in.ExternalID, UserID: userID, Active: true, Mode: "manual"}
		if _, err := s.Insert(&row); err != nil {
			return false, mapErr(err)
		}
	}
	return true, nil
}

// EnterpriseIdentityForUser 返回管理员用户详情要展示的企业绑定和只读原因，不包含身份平台凭据。
// 参数 ctx 为请求上下文，userID 为本地账户 ID；返回无绑定的 nil 或脱密身份视图，查询失败返回错误，无持久化副作用。
func (db *DB) EnterpriseIdentityForUser(ctx context.Context, userID string) (*EnterpriseUserIdentity, error) {
	var rows []EnterpriseUserIdentity
	query := "SELECT du.connection_id AS sso_connection_id, dc.name AS sso_connection_name, du.external_id AS sso_external_id, " +
		"(dc.scim_configured OR EXISTS (SELECT 1 FROM directory_group_members gm WHERE gm.directory_user_id = du.id)) AS sso_binding_managed " +
		"FROM directory_users du JOIN directory_connections dc ON dc.id = du.connection_id WHERE du.user_id = ?"
	err := db.Engine.Context(ctx).SQL(query, userID).Find(&rows)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}
