package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"xorm.io/xorm"
)

// ErrDeliveryMarkerGone 表示待确认标记已被转换或恢复，调用方必须核实已有账单，不得凭此创建新账单。
var ErrDeliveryMarkerGone = errors.New("delivery marker no longer exists")

// DiscardRecordedDelivery 仅在相同请求已有持久账单或待结算快照时删除未确认标记。
// ctx 控制数据库操作，id 为调用 ID；返回错误供调用方记录，不能清理由内存推断的未记录结果。
func (db *DB) DiscardRecordedDelivery(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("empty delivery identity")
	}
	_, err := db.Engine.Context(ctx).Exec(`DELETE FROM usage_delivery_pending WHERE request_id = ? AND (EXISTS (SELECT 1 FROM usage_events WHERE request_id = ?) OR EXISTS (SELECT 1 FROM usage_pending WHERE request_id = ?))`, id, id, id)
	return err
}

// PrepareDelivery 在响应写出前独立持久化零费用恢复标记；record 是调用的归属和计费事实，expires 为恢复截止时间。
// 返回存储或输入错误；调用方必须在成功后才写出响应，不能把恢复标记放入自动结算队列。
// 标记不保存请求、响应、请求头或上游正文；未确认的价格快照也不保存，超时只能恢复为结果未知的失败。
func (db *DB) PrepareDelivery(ctx context.Context, record UsageRecord, expires time.Time) error {
	if !expires.After(time.Now()) {
		return fmt.Errorf("invalid delivery deadline")
	}
	record, err := UnconfirmedDelivery(record)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	result, err := db.Engine.Context(ctx).Exec(`INSERT INTO usage_delivery_pending(request_id,payload,expires_at) VALUES (?,?::jsonb,?) ON CONFLICT(request_id) DO NOTHING`, record.RequestID, string(payload), expires)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return fmt.Errorf("%w: delivery marker already exists", ErrConflict)
	}
	return nil
}

// UnconfirmedDelivery 构造不含正文和费用的恢复快照；record 为上游产生的结果，返回独立副本或验证错误。
// 数据库与磁盘准备阶段共用；不得把该快照当作客户端明确停止或已经收到响应的证据。
func UnconfirmedDelivery(record UsageRecord) (UsageRecord, error) {
	if err := validatePendingUsage(record); err != nil {
		return UsageRecord{}, err
	}
	record.TS = stamp(record.TS)
	record.Status, record.HTTPStatus, record.Cost = "error", 0, 0
	record.RequestBody, record.ResponseBody, record.ProxyRequest, record.UpstreamResponse = "", "", "", ""
	record.PriceSnapshot, record.Guardrail = "", ""
	// 用户备注可能包含任意文本，不能作为恢复标记长期保存。
	record.Label = ""
	record.Error = `{"type":"delivery_unconfirmed","message":"client delivery was not confirmed"}`
	return record, nil
}

// stageDelivery 将单条最终快照放入同一事务中的待结算队列；参数是活动事务与已验证账单。
// 返回持久化或快照冲突错误；相同请求已有不同内容时必须回滚，避免替换原有账单。
func stageDelivery(s *xorm.Session, record UsageRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = s.Exec(`INSERT INTO usage_pending(request_id,payload) VALUES (?,?::jsonb) ON CONFLICT(request_id) DO NOTHING`, record.RequestID, string(payload)); err != nil {
		return err
	}
	rows, err := s.QueryString(`SELECT payload = CAST(? AS jsonb) AS same FROM usage_pending WHERE request_id = ?`, string(payload), record.RequestID)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0]["same"] != "true" {
		return fmt.Errorf("%w: pending delivery snapshot differs", ErrConflict)
	}
	return nil
}

// ConfirmDelivery 将已确认写出或明确写失败的最终账单原子转入待结算队列。
// 参数 ctx 为数据库上下文，record 为最终快照；返回错误时不删除恢复标记，也不产生部分账单。
// 记录必须属于原请求的相同密钥和账单归属；结果已被恢复或同 ID 内容冲突时拒绝重复结算。
func (db *DB) ConfirmDelivery(ctx context.Context, record UsageRecord) error {
	if err := validatePendingUsage(record); err != nil {
		return err
	}
	if record.Cost > 0 && (record.Status != "success" || record.HTTPStatus >= 400) {
		return fmt.Errorf("invalid delivery charge for failed response")
	}
	s := db.Engine.NewSession().Context(ctx)
	defer s.Close()
	if err := s.Begin(); err != nil {
		return err
	}
	defer s.Rollback()
	rows, err := s.QueryString(`SELECT payload::text AS payload FROM usage_delivery_pending WHERE request_id = ? FOR UPDATE`, record.RequestID)
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return ErrDeliveryMarkerGone
	}
	var initial UsageRecord
	if err := json.Unmarshal([]byte(rows[0]["payload"]), &initial); err != nil {
		return err
	}
	if initial.RequestID != record.RequestID || initial.KeyID != record.KeyID || initial.UserID != record.UserID || initial.TeamID != record.TeamID || initial.ProjectID != record.ProjectID || initial.OrganizationID != record.OrganizationID || initial.OwnerType != record.OwnerType || initial.Model != record.Model || (initial.CallType != "" && initial.CallType != record.CallType) || initial.BudgetReservationID != record.BudgetReservationID {
		return fmt.Errorf("%w: delivery owner differs", ErrConflict)
	}
	// 首次准备时固定发生时间，故障恢复不会把账单移到其他时间范围。
	record.TS = initial.TS
	if err := stageDelivery(s, record); err != nil {
		return err
	}
	if _, err := s.Exec(`DELETE FROM usage_delivery_pending WHERE request_id = ?`, record.RequestID); err != nil {
		return err
	}
	return s.Commit()
}

// SettleConfirmedDelivery 恢复已持久化的客户端写出终态；ctx 控制数据库操作，record 为完整冻结快照。
// 先原子转换待确认标记；仅独立磁盘在断库时保存的快照可在没有标记时创建账单，其他情况必须已存在持久账单。
// 全量快照由严格指纹核对，冲突保留队列并返回错误，不能伪造确认或重复计费。
func (db *DB) SettleConfirmedDelivery(ctx context.Context, record UsageRecord) error {
	if err := db.ConfirmDelivery(ctx, record); err != nil {
		if !errors.Is(err, ErrDeliveryMarkerGone) {
			return err
		}
		pending, err := db.Engine.Context(ctx).Table("usage_pending").Where("request_id = ?", record.RequestID).Exist()
		if err != nil {
			return err
		}
		committed, err := db.Engine.Context(ctx).Table("usage_events").Where("request_id = ?", record.RequestID).Exist()
		if err != nil {
			return err
		}
		if !pending && !committed && !record.DeliverySpoolOnly {
			return fmt.Errorf("delivery marker and ledger are missing")
		}
	}
	if err := db.RecordUsageExact(ctx, []UsageRecord{record}); err != nil {
		return err
	}
	return db.FlushPendingUsage(ctx, 500)
}

// RecoverExpiredDeliveries 将过期且尚未确认的记录原子转为零费用失败，返回已转移数量和错误。
// ctx 控制数据库操作，now 为观察时间，limit 必须为正；多个实例通过行锁互斥，不调用上游。
// 已转移记录仍由现有 FlushPendingUsage 精确结算；恢复后迟到的确认会被拒绝，不能再次收费。
func (db *DB) RecoverExpiredDeliveries(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, fmt.Errorf("invalid recovery batch size")
	}
	s := db.Engine.NewSession().Context(ctx)
	defer s.Close()
	if err := s.Begin(); err != nil {
		return 0, err
	}
	defer s.Rollback()
	// 磁盘独占的标记必须由持有该卷的实例恢复；其他实例看不到其已确认终态，不能猜测失败。
	rows, err := s.QueryString(`SELECT request_id,payload::text AS payload FROM usage_delivery_pending WHERE expires_at <= ? AND COALESCE((payload->>'DeliverySpoolOnly')::boolean, false) = false ORDER BY expires_at,request_id LIMIT ? FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		// 已有最终账单的失败请求可能在清理标记前退出，不能用另一份未知快照覆盖或重复结算。
		existing, err := s.QueryString(`SELECT EXISTS (SELECT 1 FROM usage_events WHERE request_id = ?) OR EXISTS (SELECT 1 FROM usage_pending WHERE request_id = ?) AS present`, row["request_id"], row["request_id"])
		if err != nil {
			return 0, err
		}
		if len(existing) == 1 && existing[0]["present"] == "true" {
			if _, err := s.Exec(`DELETE FROM usage_delivery_pending WHERE request_id = ?`, row["request_id"]); err != nil {
				return 0, err
			}
			continue
		}
		var record UsageRecord
		if err := json.Unmarshal([]byte(row["payload"]), &record); err != nil {
			return 0, err
		}
		if err := validatePendingUsage(record); err != nil {
			return 0, err
		}
		if record.RequestID != row["request_id"] || record.Status != "error" || record.Cost != 0 {
			return 0, fmt.Errorf("invalid delivery recovery snapshot")
		}
		if err := stageDelivery(s, record); err != nil {
			return 0, err
		}
		if _, err := s.Exec(`DELETE FROM usage_delivery_pending WHERE request_id = ?`, record.RequestID); err != nil {
			return 0, err
		}
	}
	if err := s.Commit(); err != nil {
		return 0, err
	}
	return len(rows), nil
}
