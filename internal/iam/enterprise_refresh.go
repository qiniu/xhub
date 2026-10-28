package iam

import (
	"context"
	"time"
	"xorm.io/xorm"
)

// SAMLRefresh 保存手工 SAML 元数据的独立调度与脱敏结果；租约仅后台使用，不返回管理员。
// 时间使用 Unix 秒避免零时间歧义；配置独立于登录 revision，刷新不能使正在登录的事务失效。
type SAMLRefresh struct {
	ConnectionID  string `xorm:"pk 'connection_id'" json:"connection_id"`
	Enabled       bool   `json:"enabled"`
	SourceURL     string `xorm:"'source_url'" json:"source_url"`
	IntervalHours int    `xorm:"'interval_hours'" json:"interval_hours"`
	LastAttempt   int64  `xorm:"'last_attempt'" json:"last_attempt"`
	LastSuccess   int64  `xorm:"'last_success'" json:"last_success"`
	NextAttempt   int64  `xorm:"'next_attempt'" json:"next_attempt"`
	ErrorCode     string `xorm:"'error_code'" json:"error_code"`
	Lease         string `json:"-"`
	LeaseUntil    int64  `xorm:"'lease_until'" json:"-"`
}

// TableName 指定独立调度表；ORM 调用，无副作用。
func (SAMLRefresh) TableName() string { return "enterprise_saml_refresh" }

// SAMLRefreshByID 返回已存调度或默认关闭值；管理员读取调用，不创建记录，数据库错误原样返回。
func (db *DB) SAMLRefreshByID(ctx context.Context, id string) (*SAMLRefresh, error) {
	out := &SAMLRefresh{ConnectionID: id, IntervalHours: 6}
	_, err := db.Engine.Context(ctx).ID(id).Get(out)
	return out, err
}

// SaveSAMLRefresh 保存管理员已校验的调度；锁登录配置、检查 revision，清除旧租约避免旧请求覆盖新配置。
// 不改变身份材料和登录版本；错误回滚并由调用方映射，成功记录管理审计。
func (db *DB) SaveSAMLRefresh(ctx context.Context, by Actor, c *EnterpriseOIDC, in SAMLRefresh) error {
	return db.tx(ctx, func(s *xorm.Session) error {
		var current EnterpriseOIDC
		if err := get(s.SQL("SELECT * FROM enterprise_oidc WHERE connection_id=? FOR UPDATE", c.ConnectionID), &current); err != nil {
			return err
		}
		if current.Revision != c.Revision || current.Protocol != "saml" || in.IntervalHours < 1 || in.IntervalHours > 24 {
			return ErrConflict
		}
		_, err := s.Exec(`INSERT INTO enterprise_saml_refresh(connection_id,enabled,source_url,interval_hours,next_attempt) VALUES(?,?,?,?,?) ON CONFLICT(connection_id) DO UPDATE SET enabled=EXCLUDED.enabled,source_url=EXCLUDED.source_url,interval_hours=EXCLUDED.interval_hours,next_attempt=EXCLUDED.next_attempt,lease='',lease_until=0,error_code=''`, c.ConnectionID, in.Enabled, in.SourceURL, in.IntervalHours, time.Now().Unix())
		if err != nil {
			return err
		}
		return writeAudit(s, by, Audit{Action: "enterprise.saml.refresh.configure", ObjectType: "enterprise_oidc", ObjectID: c.ConnectionID})
	})
}

// DueSAMLRefresh 返回至多 32 个到期任务；后台循环调用，未启用连接或正在执行任务不进入队列。
func (db *DB) DueSAMLRefresh(ctx context.Context) ([]SAMLRefresh, error) {
	rows := []SAMLRefresh{}
	err := db.Engine.Context(ctx).SQL(`SELECT r.* FROM enterprise_saml_refresh r JOIN enterprise_oidc o USING(connection_id) WHERE r.enabled AND o.enabled AND o.protocol='saml' AND r.next_attempt<=? AND r.lease_until<=? ORDER BY r.next_attempt LIMIT 32`, time.Now().Unix(), time.Now().Unix()).Find(&rows)
	return rows, err
}

// ClaimSAMLRefresh 原子领取两分钟租约；后台须到期，手动刷新允许关闭定时开关。并发执行返回 ErrConflict。
// 网络请求在领取事务之外执行，进程崩溃后租约到期可恢复；不持有数据库锁等待外网。
func (db *DB) ClaimSAMLRefresh(ctx context.Context, id string, manual bool) (*SAMLRefresh, error) {
	rows := []SAMLRefresh{}
	now := time.Now().Unix()
	err := db.Engine.Context(ctx).SQL(`UPDATE enterprise_saml_refresh SET lease=?,lease_until=?,last_attempt=? WHERE connection_id=? AND lease_until<=? AND (? OR (enabled AND next_attempt<=?)) RETURNING *`, newID(), now+120, now, id, now, manual, now).Find(&rows)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrConflict
	}
	return &rows[0], nil
}

// FinishSAMLRefresh 事务提交已校验 XML 或失败状态；旧 revision、原 XML 或租约改变时拒绝迟到结果。
// 自动刷新不增 revision，保持当前登录事务；仅接受调用方确定的错误码，失败不改 XML，写入审计失败则整体回滚。
func (db *DB) FinishSAMLRefresh(ctx context.Context, by Actor, c *EnterpriseOIDC, job *SAMLRefresh, raw, code string, next int64) error {
	return db.tx(ctx, func(s *xorm.Session) error {
		var current EnterpriseOIDC
		if err := get(s.SQL("SELECT * FROM enterprise_oidc WHERE connection_id=? FOR UPDATE", c.ConnectionID), &current); err != nil {
			return err
		}
		var claim SAMLRefresh
		if err := get(s.SQL("SELECT * FROM enterprise_saml_refresh WHERE connection_id=? FOR UPDATE", c.ConnectionID), &claim); err != nil {
			return err
		}
		if claim.Lease != job.Lease || claim.LeaseUntil <= time.Now().Unix() || current.Revision != c.Revision || current.IDPMetadata != c.IDPMetadata {
			return ErrConflict
		}
		success := claim.LastSuccess
		if code == "" {
			if raw == "" {
				return ErrInvalid
			}
			if _, err := s.Exec("UPDATE enterprise_oidc SET idp_metadata=? WHERE connection_id=?", raw, c.ConnectionID); err != nil {
				return err
			}
			success = time.Now().Unix()
		}
		if _, err := s.Exec("UPDATE enterprise_saml_refresh SET last_success=?,next_attempt=?,error_code=?,lease='',lease_until=0 WHERE connection_id=?", success, next, code, c.ConnectionID); err != nil {
			return err
		}
		action := "enterprise.saml.refresh.success"
		if code != "" {
			action = "enterprise.saml.refresh.failed"
		}
		return writeAudit(s, by, Audit{Action: action, ObjectType: "enterprise_oidc", ObjectID: c.ConnectionID})
	})
}
