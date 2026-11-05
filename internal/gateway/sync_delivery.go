package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	"github.com/sunqirui1987/xhub/internal/gateway/requestlog"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	"github.com/sunqirui1987/xhub/internal/util"
)

// finishSyncDelivery 根据客户端写出结果确认同步账单；ctx 是原请求上下文，record 是已准备的快照，writeErr 为实际写错误或短写。
// 返回值无；失败时只标记网关观察到的写出错误并记零费用，不推断用户操作或上游责任。
// 确认和转入待结算队列是数据库事务；数据库失败保留待确认记录，后续恢复不能将未知结果作为成功收费。
func (s *Server) finishSyncDelivery(ctx context.Context, record iam.UsageRecord, writeErr error) {
	if writeErr != nil {
		var diag dpdiagnostics.UpstreamDiagnostics
		_ = json.Unmarshal([]byte(record.UpstreamResponse), &diag)
		failure := dpdiagnostics.ObserveFailure(ctx, "client_write", writeErr, time.Since(record.TS))
		failure.Kind = "client_write_error"
		failure.CancellationSource = "unknown"
		diag.Failure = failure
		record.Status, record.HTTPStatus, record.Cost = "error", 0, 0
		record.PriceSnapshot = ""
		record.UpstreamResponse = requestlog.DiagnosticJSON(&diag, diag.BillingUsage)
		record.Error = requestlog.FailureJSON(&diag, "")
	}
	// 已确认的终态先保存在独立磁盘队列；数据库故障时可由原实例恢复，不能提前恢复成未知账单。
	record.DeliveryPending = s.usageSpoolEnabled
	diskSaved := false
	if s.usageSpoolEnabled && s.usageSpool != nil {
		var err error
		if record.DeliverySpoolOnly {
			err = s.usageSpool.CompleteDelivery(record)
		} else {
			err = s.usageSpool.Put(record)
		}
		if err != nil {
			util.Error("delivery spool save failed request=%s: %v", record.RequestID, err)
		} else {
			diskSaved = true
		}
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.IAM.ConfirmDelivery(persistCtx, record); err != nil {
		util.Error("delivery confirmation failed request=%s: %v", record.RequestID, err)
		if diskSaved && record.DeliverySpoolOnly && errors.Is(err, iam.ErrDeliveryMarkerGone) {
			_ = s.FlushLocalUsage(persistCtx)
		}
		if !diskSaved && s.Live != nil {
			// Redis 同时冻结终态和各预算主体的热费用，数据库恢复后再严格核对并确认队列。
			raw, marshalErr := json.Marshal(record)
			if marshalErr == nil {
				row := live.SpendLog{DeliverySnapshot: string(raw), RequestID: record.RequestID, KeyID: record.KeyID, APIKey: record.KeyHash, UserID: record.UserID, TeamID: record.TeamID, OrgID: record.OrganizationID, ProjectID: record.ProjectID, Spend: record.Cost, SpendValid: true, Start: record.TS.UTC().Format(time.RFC3339Nano), End: record.EndedAt.UTC().Format(time.RFC3339Nano)}
				marshalErr = s.Live.EnqueueConfirmedDelivery(row)
			}
			if marshalErr != nil {
				util.Error("confirmed delivery redis persistence failed request=%s: %v", record.RequestID, marshalErr)
			}
		}
		return
	}
	if err := s.IAM.FlushPendingUsage(persistCtx, 500); err != nil {
		util.Error("delivery settlement pending request=%s: %v", record.RequestID, err)
		return
	}
	if diskSaved {
		if err := s.usageSpool.Acknowledge(record); err != nil {
			util.Error("delivery spool acknowledgement failed request=%s: %v", record.RequestID, err)
		}
	} else if record.DeliverySpoolOnly && s.usageSpool != nil {
		if err := s.usageSpool.DiscardUnconfirmedDelivery(record); err != nil {
			util.Error("delivery spool cleanup failed request=%s: %v", record.RequestID, err)
		}
	}
}
