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
	InferenceFenceAcquired InferenceFenceResult = iota
	InferenceFenceExisting
	InferenceFenceConflict
)

// ClaimInferenceFence 原子登记脱敏请求标识，供不同网关实例阻止幂等键重复外发。
// ctx 控制 DB 调用；keyHash/requestHash 为 32 字节 SHA256 的小写十六进制摘要；now/expiry 为当前和过期时间。
// 返回取得资格、相同请求已登记、不同载荷冲突或存储错误；不保存凭据、请求/响应正文或金额。
// 已过期标识可重新使用；每次调用最多清除 64 条过期记录，DB 错误由调用方失败关闭。
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
	// 所有操作均限定到摘要，清理仅删除已过期记录，不扫描或输出用户正文。
	// 外层再次检查过期时间，避免并发刷新过期键后被本次清理误删。
	_, err := db.Engine.Context(ctx).Exec(`DELETE FROM inference_idempotency_fences WHERE expires_at <= ? AND key_hash IN (SELECT key_hash FROM inference_idempotency_fences WHERE expires_at <= ? ORDER BY expires_at LIMIT 64)`, now, now)
	if err != nil {
		return InferenceFenceConflict, err
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
