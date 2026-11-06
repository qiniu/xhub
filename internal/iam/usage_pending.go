package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// pendingUsage 是数据库待结算快照，供直写路径与后台恢复共享；不包含额外凭据，正文沿用现有脱敏与保存策略。
type pendingUsage struct {
	RequestID string `xorm:"request_id"`
	Payload   string `xorm:"payload"`
}

// validatePendingUsage 校验可持久化的账单身份与数值；参数为完整快照，返回错误且不写数据库。
// 直写入队和恢复解码都调用，拒绝损坏或不受支持的数据而不删除待处理证据。
func validatePendingUsage(record UsageRecord) error {
	if record.RequestID == "" || record.Cost < 0 || math.IsNaN(record.Cost) || math.IsInf(record.Cost, 0) || record.PromptTokens < 0 || record.CompletionTokens < 0 || (record.CachedTokens != nil && *record.CachedTokens < 0) {
		return fmt.Errorf("invalid pending usage identity, cost or token count")
	}
	return nil
}

// RecordUsageDurably 在独立提交的待结算表中保存完整快照，然后尝试原子结算并确认删除。
// 参数 ctx 控制数据库操作，record 为已计算费用/归属/正文；返回 durable 表示快照已持久化，err 表示保存、结算或确认失败。
// 网关无 Redis 或 Redis 入队失败时调用；结算失败仍保留快照供 Flush/新实例恢复，不改变已成功的上游响应。
// 数据库本身不可写时 durable=false，调用方必须明确报告未持久化，不能把内存日志当作可靠队列。
func (db *DB) RecordUsageDurably(ctx context.Context, record UsageRecord) (durable bool, err error) {
	if err = validatePendingUsage(record); err != nil {
		return false, err
	}
	// 首次入队即冻结默认发生时间，不能在故障恢复时把旧调用移入新的预算周期。
	record.TS = stamp(record.TS)
	payload, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	// 不复用 db.tx：该事务包含周期预算维护和账单表写入，正是可能需要独立恢复的失败边界。
	if _, err = db.Engine.Context(ctx).Exec(`INSERT INTO usage_pending(request_id, payload) VALUES (?, ?::jsonb) ON CONFLICT (request_id) DO NOTHING`, record.RequestID, string(payload)); err != nil {
		return false, err
	}
	var saved pendingUsage
	found, err := db.Engine.Context(ctx).Table("usage_pending").Where("request_id = ?", record.RequestID).Get(&saved)
	if err != nil {
		return false, err
	}
	if !found {
		// 并发恢复者可能已确认；必须精确核对同一快照，不能把别人的同ID记录当作本份持久化。
		err := db.RecordUsageExact(ctx, []UsageRecord{record})
		return err == nil, err
	}
	rows, compareErr := db.Engine.Context(ctx).QueryString("SELECT CAST(? AS jsonb) = CAST(? AS jsonb) AS same", saved.Payload, string(payload))
	if compareErr != nil {
		return false, compareErr
	}
	if len(rows) != 1 || rows[0]["same"] != "true" {
		return false, fmt.Errorf("%w: pending usage snapshot differs", ErrConflict)
	}
	return true, db.settlePendingUsage(ctx, saved)
}

// settlePendingUsage 校验并结算一个持久快照；参数为数据库原值，返回解码、结算或删除错误。
// 直写和后台恢复调用；RecordUsageExact 的事务保证完整快照匹配及账单/预算/聚合幂等，只有提交成功才删除相同快照。
func (db *DB) settlePendingUsage(ctx context.Context, saved pendingUsage) error {
	var record UsageRecord
	if err := json.Unmarshal([]byte(saved.Payload), &record); err != nil {
		return fmt.Errorf("pending usage %s decode: %w", saved.RequestID, err)
	}
	if err := validatePendingUsage(record); err != nil || record.RequestID != saved.RequestID {
		return fmt.Errorf("pending usage %s has invalid snapshot", saved.RequestID)
	}
	if err := db.RecordUsageExact(ctx, []UsageRecord{record}); err != nil {
		return err
	}
	// 提交后进程退出会留下同一快照；下一实例重放由 RecordUsage 去重后继续确认。
	_, err := db.Engine.Context(ctx).Exec(`DELETE FROM usage_pending WHERE request_id = ? AND payload = ?::jsonb`, saved.RequestID, saved.Payload)
	return err
}

// FlushPendingUsageRequest 仅结算指定请求的持久快照；ctx 控制数据库操作，requestID 为非空调用标识。
// 同步交付确认后调用；未找到快照时核对已提交账单，不扫描或删除其他请求的待结算证据。
func (db *DB) FlushPendingUsageRequest(ctx context.Context, requestID string) error {
	if requestID == "" {
		return fmt.Errorf("pending usage request ID is required")
	}
	var saved pendingUsage
	found, err := db.Engine.Context(ctx).Table("usage_pending").Where("request_id = ?", requestID).Get(&saved)
	if err != nil {
		return err
	}
	if !found {
		committed, err := db.Engine.Context(ctx).Table("usage_events").Where("request_id = ?", requestID).Exist()
		if err != nil {
			return err
		}
		if !committed {
			return fmt.Errorf("pending usage and committed event are missing")
		}
		return nil
	}
	return db.settlePendingUsage(ctx, saved)
}

// FlushPendingUsage 重放最多 limit 条待结算账单；参数 ctx 控制操作，limit 必须为正，返回各失败的合并错误。
// 后台 Flush 即使没有 Redis 也调用；单条损坏或约束失败不阻止同批其他记录，失败原文保留供修复。
// 不调用上游、不重算价格；多实例重复读取依靠 RecordUsage 的 request_id 幂等性，确认失败可再次重放。
func (db *DB) FlushPendingUsage(ctx context.Context, limit int) error {
	if limit < 1 {
		return fmt.Errorf("pending usage batch limit must be positive")
	}
	var rows []pendingUsage
	if err := db.Engine.Context(ctx).Table("usage_pending").Asc("created_at", "request_id").Limit(limit).Find(&rows); err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if err := db.settlePendingUsage(ctx, row); err != nil {
			failures = append(failures, fmt.Errorf("pending usage %s: %w", row.RequestID, err))
		}
	}
	return errors.Join(failures...)
}
