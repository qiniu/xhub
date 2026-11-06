package iam

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"
)

// InferenceFenceResult 区分本次获得首次请求资格、重复请求和同键不同载荷；不包含请求内容。
type InferenceFenceResult uint8

const (
	// InferenceFenceUnknown 为零值，不能授予上游调用资格。
	InferenceFenceUnknown InferenceFenceResult = iota
	InferenceFenceAcquired
	InferenceFenceExisting
	InferenceFenceConflict
)

// ClaimInferenceFence 原子登记脱敏请求标识，供不同网关实例阻止幂等键重复外发。
// ctx 控制 DB 调用；keyHash/requestHash 为 32 字节 SHA256 的小写十六进制摘要；now/expiry 为当前和过期时间。
// 返回取得资格、相同请求已登记、不同载荷冲突或存储错误；不保存凭据、请求/响应正文或金额。
// 已过期标识可重新使用；过期清理由后台维护，不阻塞首次请求，DB 错误由调用方失败关闭。
func (db *DB) ClaimInferenceFence(ctx context.Context, keyHash, requestHash string, now, expiry time.Time) (InferenceFenceResult, error) {
	for _, value := range []string{keyHash, requestHash} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != value {
			return InferenceFenceConflict, fmt.Errorf("invalid idempotency digest")
		}
	}
	if !expiry.After(now) {
		return InferenceFenceConflict, fmt.Errorf("invalid idempotency expiry")
	}
	for attempts := 0; attempts < 3; attempts++ {
		rows, err := db.Engine.Context(ctx).QueryString(`INSERT INTO inference_idempotency_fences(key_hash,request_hash,expires_at) VALUES (?, ?, ?) ON CONFLICT (key_hash) DO UPDATE SET request_hash=EXCLUDED.request_hash, expires_at=EXCLUDED.expires_at WHERE inference_idempotency_fences.expires_at <= ? RETURNING key_hash`, keyHash, requestHash, expiry, now)
		if err != nil {
			return InferenceFenceConflict, err
		}
		if len(rows) > 0 {
			return InferenceFenceAcquired, nil
		}
		existing, err := db.Engine.Context(ctx).QueryString(`SELECT request_hash FROM inference_idempotency_fences WHERE key_hash = ?`, keyHash)
		if err != nil {
			return InferenceFenceConflict, err
		}
		if len(existing) == 0 {
			continue
		} // 并发过期清理后重新竞争；不可把缺行当作获得资格。
		if existing[0]["request_hash"] != requestHash {
			return InferenceFenceConflict, nil
		}
		return InferenceFenceExisting, nil
	}
	return InferenceFenceConflict, fmt.Errorf("idempotency fence contention")
}

// CleanupInferenceFences 分批删除已过期的幂等摘要；ctx 控制数据库调用，now 是过期判断时间，limit 必须为正。
// 返回删除数量和错误；后台维护调用，跳过其他实例持有的行锁，不读取或保存正文。
func (db *DB) CleanupInferenceFences(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("invalid fence cleanup limit")
	}
	result, err := db.Engine.Context(ctx).Exec(`DELETE FROM inference_idempotency_fences WHERE key_hash IN (SELECT key_hash FROM inference_idempotency_fences WHERE expires_at <= ? ORDER BY expires_at LIMIT ? FOR UPDATE SKIP LOCKED)`, now, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
