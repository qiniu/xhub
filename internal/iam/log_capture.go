package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"xorm.io/xorm"
)

// LogCapture 是一条独立的正文快照。请求文档包含方法、脱敏头和唯一的一份请求正文；响应文档单独保存。
// 调用方负责在写入前鉴权、脱敏并计算保留期限；此结构不用于计费或续聊。
type LogCapture struct {
	RequestID string    `xorm:"'request_id'"`
	UserID    string    `xorm:"'user_id'"`
	Source    string    `xorm:"'source'"`
	CreatedAt time.Time `xorm:"'created_at'"`
	ExpiresAt time.Time `xorm:"'expires_at'"`
	Request   string    `xorm:"'request_document'"`
	Response  string    `xorm:"'response_document'"`
}

// CaptureLimits 使用未压缩字节数控制单条、个人与全局上限；保守计量避免依赖正文压缩率。
type CaptureLimits struct{ PerRequest, PerUser, Global int64 }

// requestMessages 从唯一的请求文档恢复旧接口的 messages 视图；无 messages 时保留整个 body。
// 参数 document 是已保存的 JSON，返回 JSON 字符串；解析失败或缺少 body 返回空，不改变数据库。
func requestMessages(document string) string {
	var proxy map[string]json.RawMessage
	if json.Unmarshal([]byte(document), &proxy) != nil {
		return ""
	}
	body := proxy["body"]
	if len(body) == 0 || string(body) == "null" {
		return ""
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) == nil {
		if messages, ok := object["messages"]; ok {
			return string(messages)
		}
	}
	return string(body)
}

// PersonalLogCaptureEnabled 查询用户个人记录选择；userID 必须是已经鉴权的用户。
// 未设置返回关闭；数据库错误原样返回，调用方不得把未知状态当成开启。只读，无副作用。
func (db *DB) PersonalLogCaptureEnabled(ctx context.Context, userID string) (bool, error) {
	if strings.TrimSpace(userID) == "" {
		return false, ErrInvalid
	}
	var row struct{ Enabled bool }
	ok, err := db.Engine.Context(ctx).Table("personal_log_preferences").Where("user_id = ?", userID).Get(&row)
	if err != nil {
		return false, mapErr(err)
	}
	return ok && row.Enabled, nil
}

// SetPersonalLogCapture 保存已鉴权用户的个人记录选择；userID 不得来自待信任的请求正文。
// 返回数据库错误；不改变管理员全局设置、既有记录、权限或预算。
func (db *DB) SetPersonalLogCapture(ctx context.Context, userID string, enabled bool) error {
	if strings.TrimSpace(userID) == "" {
		return ErrInvalid
	}
	_, err := db.Engine.Context(ctx).Exec(`INSERT INTO personal_log_preferences (user_id, enabled) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()`, userID, enabled)
	return mapErr(err)
}

// PersonalLogCaptureBytes 返回该用户当前已占用的正文逻辑字节数；userID 由鉴权上下文提供。
// 统计包含等待后台清理的到期数据，直至清理成功；数据库错误不能显示为零。
func (db *DB) PersonalLogCaptureBytes(ctx context.Context, userID string) (int64, error) {
	if strings.TrimSpace(userID) == "" {
		return 0, ErrInvalid
	}
	var row struct {
		SizeBytes int64 `xorm:"'size_bytes'"`
	}
	_, err := db.Engine.Context(ctx).SQL(`SELECT size_bytes FROM request_log_capture_quotas WHERE scope = ?`, "user:"+userID).Get(&row)
	return row.SizeBytes, mapErr(err)
}

// captureTransaction 在独立事务内串行化正文容量增减，避免并发写入超额。
// ctx 控制数据库操作；fn 在持锁事务内调用，出错回滚。正文事务不调用预算维护，不能改变计费规则。
func (db *DB) captureTransaction(ctx context.Context, fn func(*xorm.Session) error) error {
	s := db.Engine.NewSession()
	defer s.Close()
	s.Context(ctx)
	if err := s.Begin(); err != nil {
		return err
	}
	defer s.Rollback()
	if _, err := s.Exec(`INSERT INTO request_log_capture_quotas(scope, size_bytes) VALUES ('global', 0) ON CONFLICT DO NOTHING`); err != nil {
		return err
	}
	// 独立计数行锁同时约束总容量和单用户容量；所有清理路径也按同一顺序获取锁。
	var lock struct {
		SizeBytes int64 `xorm:"'size_bytes'"`
	}
	if _, err := s.SQL(`SELECT size_bytes FROM request_log_capture_quotas WHERE scope = 'global' FOR UPDATE`).Get(&lock); err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return s.Commit()
}

// captureQuota 读取当前事务中的占用字节。scope 是内部固定前缀，不含调用方提供的 SQL。
// 缺行返回零；供已持有全局锁的保存路径使用，不修改数据。
func captureQuota(s *xorm.Session, scope string) (int64, error) {
	var row struct {
		SizeBytes int64 `xorm:"'size_bytes'"`
	}
	_, err := s.SQL(`SELECT size_bytes FROM request_log_capture_quotas WHERE scope = ?`, scope).Get(&row)
	return row.SizeBytes, err
}

// SaveLogCapture 保存一份正文并原子检查容量；c 为已脱敏快照，limits 为正字节上限，now 为当前时间。
// 返回 saved、too_large、capacity、expired 或 duplicate；数据库失败返回错误，绝不改变账单。
// 新正文只保存一次，不通过用量队列重放；过期或容量不足均不写正文。
func (db *DB) SaveLogCapture(ctx context.Context, c LogCapture, limits CaptureLimits, now time.Time) (string, error) {
	if c.RequestID == "" || (c.Source != "personal" && c.Source != "global") || (c.Source == "personal" && c.UserID == "") || c.CreatedAt.IsZero() || c.ExpiresAt.IsZero() || limits.PerRequest <= 0 || limits.PerUser <= 0 || limits.Global <= 0 {
		return "", ErrInvalid
	}
	if !c.ExpiresAt.After(now) {
		return "expired", nil
	}
	size := int64(len(c.Request) + len(c.Response))
	if size > limits.PerRequest {
		return "too_large", nil
	}
	state := "saved"
	err := db.captureTransaction(ctx, func(s *xorm.Session) error {
		var existing struct {
			RequestID string `xorm:"'request_id'"`
		}
		found, err := s.SQL(`SELECT request_id FROM request_log_captures WHERE request_id = ?`, c.RequestID).Get(&existing)
		if err != nil {
			return err
		}
		if found {
			state = "duplicate"
			return nil
		}
		global, err := captureQuota(s, "global")
		if err != nil {
			return err
		}
		userScope := "user:" + c.UserID
		user, err := captureQuota(s, userScope)
		if err != nil {
			return err
		}
		if size > limits.Global-global || (c.UserID != "" && size > limits.PerUser-user) {
			state = "capacity"
			return nil
		}
		if _, err = s.Exec(`INSERT INTO request_log_captures (request_id, user_id, source, created_at, expires_at, request_document, response_document, size_bytes) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, c.RequestID, c.UserID, c.Source, c.CreatedAt, c.ExpiresAt, c.Request, c.Response, size); err != nil {
			return err
		}
		if _, err = s.Exec(`UPDATE request_log_capture_quotas SET size_bytes = size_bytes + ? WHERE scope = 'global'`, size); err != nil {
			return err
		}
		if c.UserID != "" {
			_, err = s.Exec(`INSERT INTO request_log_capture_quotas(scope, size_bytes) VALUES (?, ?) ON CONFLICT (scope) DO UPDATE SET size_bytes = request_log_capture_quotas.size_bytes + EXCLUDED.size_bytes`, userScope, size)
		}
		return err
	})
	return state, mapErr(err)
}

// GetLogCapture 只读取未到期正文；调用方必须先验证所属日志的访问权限。
// requestID 为日志标识，now 为读取时刻；缺失或到期返回 ErrNotFound，不延长保留期。
func (db *DB) GetLogCapture(ctx context.Context, requestID string, now time.Time) (*LogCapture, error) {
	var c LogCapture
	found, err := db.Engine.Context(ctx).Table("request_log_captures").Where("request_id = ? AND expires_at > ?", requestID, now).Get(&c)
	if err != nil {
		return nil, mapErr(err)
	}
	if !found {
		return nil, ErrNotFound
	}
	return &c, nil
}

// reconcileCaptureQuota 在已持有全局锁的事务内重算指定占用；scope 只能是 global 或内部 user: 前缀。
// 供异常计数修复使用，统计删除后的剩余正文并原子写回；查询或更新失败由调用方回滚整个清理批次。
func reconcileCaptureQuota(s *xorm.Session, scope string) error {
	var row struct {
		SizeBytes int64 `xorm:"'size_bytes'"`
	}
	query := `SELECT COALESCE(SUM(size_bytes), 0) AS size_bytes FROM request_log_captures`
	var err error
	if scope == "global" {
		_, err = s.SQL(query).Get(&row)
	} else if strings.HasPrefix(scope, "user:") {
		_, err = s.SQL(query+` WHERE user_id = ?`, strings.TrimPrefix(scope, "user:")).Get(&row)
	} else {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	_, err = s.Exec(`INSERT INTO request_log_capture_quotas(scope, size_bytes) VALUES (?, ?) ON CONFLICT (scope) DO UPDATE SET size_bytes = EXCLUDED.size_bytes`, scope, row.SizeBytes)
	return err
}

// PurgeExpiredLogCaptures 最多清理 limit 条到期正文，now 为清理边界。
// 容量计数在同一独立事务中回收；异常时重算剩余占用，重算失败整体回滚；不删除账单、诊断摘要或续聊历史。
func (db *DB) PurgeExpiredLogCaptures(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 10000 {
		return 0, ErrInvalid
	}
	count := 0
	err := db.captureTransaction(ctx, func(s *xorm.Session) error {
		type expired struct {
			UserID    string `xorm:"'user_id'"`
			SizeBytes int64  `xorm:"'size_bytes'"`
		}
		var rows []expired
		// 先限定批量再删除，减少持锁期间往返；失败由整个事务回滚。
		if err := s.SQL(`WITH expired AS (SELECT request_id FROM request_log_captures WHERE expires_at <= ? ORDER BY expires_at, request_id LIMIT ?) DELETE FROM request_log_captures WHERE request_id IN (SELECT request_id FROM expired) RETURNING user_id, size_bytes`, now, limit).Find(&rows); err != nil {
			return err
		}
		deltas := map[string]int64{}
		for _, row := range rows {
			deltas["global"] += row.SizeBytes
			if row.UserID != "" {
				deltas["user:"+row.UserID] += row.SizeBytes
			}
		}
		for scope, size := range deltas {
			result, err := s.Exec(`UPDATE request_log_capture_quotas SET size_bytes = size_bytes - ? WHERE scope = ? AND size_bytes >= ?`, size, scope, size)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				// 单个损坏的计数不能让到期正文永久阻塞；全局锁确保重算不会与写入交叉。
				if err := reconcileCaptureQuota(s, scope); err != nil {
					return fmt.Errorf("reconcile capture quota %s: %w", scope, err)
				}
			}
		}
		count = len(rows)
		return nil
	})
	if err != nil {
		return 0, mapErr(err)
	}
	return count, nil
}
