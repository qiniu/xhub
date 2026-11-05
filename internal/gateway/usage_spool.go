package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sunqirui1987/xhub/internal/gateway/usagespool"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

// usageSpoolConfig 从启动环境读取独立本机队列设置，返回配置、是否启用与错误；不允许数据库在线改写磁盘路径。
// 配置未设置保持已有部署行为；Compose显式挂持久卷并启用，容量只接收正整数，错误不能悄悄关闭持久化。
func usageSpoolConfig() (usagespool.Config, bool, error) {
	config := usagespool.Config{Dir: os.Getenv("XHUB_USAGE_SPOOL_DIR")}
	if config.Dir == "" {
		return config, false, nil
	}
	for _, field := range []struct {
		name   string
		target *int64
	}{{"XHUB_USAGE_SPOOL_MAX_BYTES", &config.MaxBytes}, {"XHUB_USAGE_SPOOL_RECORD_BYTES", &config.MaxRecordBytes}} {
		if raw := os.Getenv(field.name); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value <= 0 {
				return config, true, fmt.Errorf("invalid %s", field.name)
			}
			*field.target = value
		}
	}
	if raw := os.Getenv("XHUB_USAGE_SPOOL_MAX_ENTRIES"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return config, true, fmt.Errorf("invalid XHUB_USAGE_SPOOL_MAX_ENTRIES")
		}
		config.MaxEntries = value
	}
	return config, true, nil
}

// initializeUsageSpool 在网关启动时取得磁盘队列锁并恢复已有文件；返回值无，错误保存在宿主以阻止新付费请求。
// 不改变健康查询/管理面可达性，不删除坏证据；只有显式配置的目录会写入磁盘。
func (s *Server) initializeUsageSpool() {
	config, enabled, err := usageSpoolConfig()
	s.usageSpoolEnabled = enabled
	if enabled && err == nil {
		s.usageSpool, err = usagespool.Open(config)
	}
	s.usageSpoolErr = err
	if err == nil && s.usageSpool != nil {
		if status, statusErr := s.usageSpool.Status(); statusErr == nil && status.Quarantined > 0 {
			util.Error("usage spool contains quarantined evidence: records=%d; new paid calls are blocked", status.Quarantined)
		}
	}
	if err != nil {
		util.Error("usage spool initialization failed: %v", err)
	}
}

// CloseUsageSpool 在进程退出或隔离重启测试中释放磁盘队列锁，不清空文件；重复调用安全。
// 调用方须先停止该实例请求和刷写，避免关闭正在使用的持久存储。
func (s *Server) CloseUsageSpool() error {
	if s == nil || s.usageSpool == nil {
		return nil
	}
	return s.usageSpool.Close()
}

// admitUsageSpool 在上游付费发送前为本次call预留本机容量，返回是否继续；失败写503及明确存储错误类型。
// 覆盖硬额度、无限额和搜索软额度，不能等收到付费结果后才发现配置/容量已不可用。
func (s *Server) admitUsageSpool(w http.ResponseWriter, r *http.Request, callID string) bool {
	if !s.usageSpoolEnabled {
		return true
	}
	err := s.usageSpoolErr
	if err == nil && s.usageSpool == nil {
		err = usagespool.ErrUnavailable
	}
	if err == nil {
		err = s.usageSpool.Reserve(callID)
	}
	if err == nil {
		return true
	}
	util.Error("usage spool admission denied call_id=%q: %v", callID, err)
	w.Header().Set("X-XHub-Usage-Persistence", "unavailable")
	util.WriteTypedError(w, r.URL.Path, http.StatusServiceUnavailable, "billing_storage_unavailable", "billing storage is unavailable; retry after recovery")
	return false
}

// FlushLocalUsage 重放本机完整账单，供周期/显式Flush及启动恢复调用；返回错误便于可观察诊断。
// 只在数据库确认事务成功后删除；#63的核销fence一致时nil可确认、冲突保留，不调用上游且不重算价格。
func (s *Server) FlushLocalUsage(ctx context.Context) error {
	if !s.usageSpoolEnabled {
		return nil
	}
	if s.usageSpoolErr != nil {
		return s.usageSpoolErr
	}
	if s.usageSpool == nil || s.IAM == nil {
		return usagespool.ErrUnavailable
	}
	// 限制单轮数据库故障等待，避免后台重放长期占住退出流程。
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.usageSpool.Replay(ctx, 500, func(ctx context.Context, record iam.UsageRecord) error {
		if record.DeliveryPending {
			if record.Cost > 0 && (record.Status != "success" || record.HTTPStatus >= 400) {
				return fmt.Errorf("invalid delivery charge")
			}
			if record.DeliveryUnconfirmed {
				// 仅新进程能重放在途标记；保留零费用及结果未知，不重现未确认正文。
				record.DeliveryUnconfirmed = false
			}
			return s.IAM.SettleConfirmedDelivery(ctx, record)
		}
		return s.IAM.RecordUsageExact(ctx, []iam.UsageRecord{record})
	})
	return err
}

// HasPendingLocalUsage 检查独立磁盘是否有未确认或待结算快照，供未知响应恢复前使用。
// 参数为兼容维护接口的上下文；返回待处理状态或存储错误；没有启用独立队列时返回 false。
func (s *Server) HasPendingLocalUsage(_ context.Context) (bool, error) {
	if !s.usageSpoolEnabled {
		return false, nil
	}
	if s.usageSpoolErr != nil {
		return false, s.usageSpoolErr
	}
	if s.usageSpool == nil {
		return false, usagespool.ErrUnavailable
	}
	state, err := s.usageSpool.Status()
	if err != nil {
		return false, err
	}
	if state.Quarantined > 0 {
		return false, usagespool.ErrCorrupt
	}
	return state.Pending > 0 || state.Reserved > 0, nil
}

// AcknowledgeDeliverySpool 在 Redis 终态已严格入账后清除同一请求的本机未确认标记。
// ctx 为维护上下文，record 为已核对的最终账单；未启用磁盘时无副作用，失败保留 Redis 证据。
func (s *Server) AcknowledgeDeliverySpool(_ context.Context, record iam.UsageRecord) error {
	if !s.usageSpoolEnabled {
		return nil
	}
	if s.usageSpoolErr != nil {
		return s.usageSpoolErr
	}
	if s.usageSpool == nil {
		return usagespool.ErrUnavailable
	}
	return s.usageSpool.DiscardUnconfirmedDelivery(record)
}

// persistUsageSnapshot 将完整最终账单先落独立磁盘，再精确提交PG事务；磁盘失败才使用PG pending兜底；返回是否已有可靠介质。
// 持久化失败不改写已完成上游响应；磁盘写失败仍尝试PG，若两者失败由persistSpend尝试Redis并告警，不虚构可恢复事实。
func (s *Server) persistUsageSnapshot(record iam.UsageRecord) (bool, error) {
	diskDurable := false
	var diskErr error
	if s.usageSpoolEnabled {
		diskErr = s.usageSpoolErr
		if diskErr == nil && s.usageSpool != nil {
			diskErr = s.usageSpool.Put(record)
			diskDurable = diskErr == nil
		} else if diskErr == nil {
			diskErr = usagespool.ErrUnavailable
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var databaseDurable bool
	var databaseErr error
	if diskDurable {
		// 磁盘已保存完整证据，直接严格确认本份快照，不能用同ID另一份PG pending的成功替代。
		databaseErr = s.IAM.RecordUsageExact(ctx, []iam.UsageRecord{record})
		databaseDurable = databaseErr == nil
	} else {
		databaseDurable, databaseErr = s.IAM.RecordUsageDurably(ctx, record)
	}
	if databaseErr == nil && diskDurable {
		if err := s.usageSpool.Acknowledge(record); err != nil {
			diskErr = err
		}
	}
	return diskDurable || databaseDurable, errors.Join(diskErr, databaseErr)
}
