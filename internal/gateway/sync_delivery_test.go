package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestSyncDialogueRoute 验证同步对话入口的保护范围及非对话/错误方法的边界。
// 不调用外部服务；该函数不改请求状态，也不需要清理数据。
func TestSyncDialogueRoute(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		expected     bool
	}{
		{"POST", "/v1/chat/completions", true}, {"POST", "/bypass/openai/v1/chat/completions", true},
		{"POST", "/v1/responses", true}, {"POST", "/bypass/openai/v1/responses", true},
		{"POST", "/v1/messages", true}, {"POST", "/bypass/anthropic/v1/messages", true},
		{"GET", "/v1/responses", false}, {"POST", "/v1/embeddings", false}, {"POST", "/other", false},
	} {
		if got := syncDialogueRoute(tc.method, tc.path); got != tc.expected {
			t.Fatalf("%s %s: got=%t", tc.method, tc.path, got)
		}
	}
}

// TestSyncDialogueGuard 验证流式、WebSocket 和空请求不走同步正文缓冲，普通同步请求仍受保护。
// 使用内存 HTTP 请求，不连接供应商、不修改外部状态，无需清理。
func TestSyncDialogueGuard(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if !syncDialogueGuard(r, false) || syncDialogueGuard(r, true) || syncDialogueGuard(nil, false) {
		t.Fatal("普通与流式入口判定错误")
	}
	r.Header.Set("Upgrade", "WebSocket")
	if syncDialogueGuard(r, false) {
		t.Fatal("WebSocket 不应被缓冲")
	}
}

// TestFinishSyncDelivery 验证写出成功按实际用量结算，写失败记录独立诊断且费用为零。
// 前置私有 PostgreSQL 和已准备标记；不访问外部供应商，数据库由测试夹具清理。
func TestFinishSyncDelivery(t *testing.T) {
	db := testIdentityStore(t)
	s := &Server{IAM: db}
	for _, tc := range []struct {
		id         string
		err        error
		wantStatus string
		wantCost   float64
	}{
		{"finish-ok", nil, "success", 0.25}, {"finish-write-failed", errors.New("test client write"), "error", 0},
	} {
		record := iam.UsageRecord{RequestID: tc.id, TS: time.Now().UTC(), Model: "test", CallType: "chat", Status: "success", HTTPStatus: 200, Cost: 0.25}
		if err := db.PrepareDelivery(t.Context(), record, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		s.finishSyncDelivery(context.Background(), record, tc.err)
		event, err := db.GetUsageEvent(t.Context(), iam.UsageQuery{}, tc.id)
		if err != nil || event.Status != tc.wantStatus || event.Cost != tc.wantCost {
			t.Fatalf("同步结果错误: event=%+v err=%v", event, err)
		}
		log, err := db.GetRequestLog(t.Context(), tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if tc.err != nil && (!strings.Contains(log.UpstreamResponse, "client_write_error") || !strings.Contains(log.Error, "client_write_error")) {
			t.Fatal("写失败缺少独立诊断")
		}
	}
}

// TestFinishSyncDeliveryDiskRecovery 验证磁盘终态在数据库可用和暂时拒绝结算时均可安全确认。
// 前置独立 PostgreSQL 与临时磁盘队列；故障由测试约束注入并清理，数据库及目录由夹具回收。
func TestFinishSyncDeliveryDiskRecovery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("database_failure=%t", fail), func(t *testing.T) {
			t.Setenv("XHUB_USAGE_SPOOL_DIR", filepath.Join(t.TempDir(), "spool"))
			db := testIdentityStore(t)
			s := &Server{IAM: db}
			s.initializeUsageSpool()
			t.Cleanup(func() { _ = s.CloseUsageSpool() })
			rec := iam.UsageRecord{RequestID: "finish-disk", TS: time.Now().UTC(), Model: "test", CallType: "chat", Status: "success", HTTPStatus: 200, Cost: 0.25, DeliveryPending: true, DeliverySpoolOnly: true}
			if err := db.PrepareDelivery(t.Context(), rec, time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			marker, err := iam.UnconfirmedDelivery(rec)
			if err != nil {
				t.Fatal(err)
			}
			marker.DeliveryUnconfirmed = true
			if err := s.usageSpool.BeginDelivery(marker); err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err := db.Engine.Exec("ALTER TABLE usage_pending ADD CONSTRAINT reject_finish CHECK (false)"); err != nil {
					t.Fatal(err)
				}
			}
			s.finishSyncDelivery(context.Background(), rec, nil)
			if fail {
				pending, err := s.HasPendingLocalUsage(t.Context())
				if err != nil || !pending {
					t.Fatalf("失败快照未留存: %t %v", pending, err)
				}
				if _, err := db.Engine.Exec("ALTER TABLE usage_pending DROP CONSTRAINT reject_finish"); err != nil {
					t.Fatal(err)
				}
				if err := s.FlushLocalUsage(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := s.HasPendingLocalUsage(t.Context())
			if err != nil || pending {
				t.Fatalf("终态未清理: %t %v", pending, err)
			}
			event, err := db.GetUsageEvent(t.Context(), iam.UsageQuery{}, rec.RequestID)
			if err != nil || event.Status != "success" || event.Cost != 0.25 {
				t.Fatalf("磁盘恢复账单错误: %+v %v", event, err)
			}
		})
	}
}

// TestDeliveryBuffer 验证只识别已启用的请求级缓冲，兼容普通包装和空输入。
// 使用本机响应记录器，无外部服务或持久数据，测试结束自动回收内存。
func TestDeliveryBuffer(t *testing.T) {
	base := httptest.NewRecorder()
	if deliveryBuffer(nil) != nil || deliveryBuffer(base) != nil {
		t.Fatal("普通 writer 不应进入同步确认")
	}
	h := &holdWriter{ResponseWriter: base, guardDelivery: true}
	w := &statusRecorder{ResponseWriter: h}
	if deliveryBuffer(w) != h {
		t.Fatal("包装链未找到缓冲")
	}
	h.guardDelivery = false
	if deliveryBuffer(w) != nil {
		t.Fatal("未启用的缓冲不应接管账单")
	}
}

// TestSyncDeliveryWriteFailureHidesNetworkAddresses 验证客户端写入故障的日志不会泄露连接两端地址。
// 使用隔离 PostgreSQL 和带地址的模拟 TCP 错误；核对失败零费用和诊断信息，测试库自动清理。
func TestSyncDeliveryWriteFailureHidesNetworkAddresses(t *testing.T) {
	db := testIdentityStore(t)
	s := &Server{IAM: db}
	record := iam.UsageRecord{RequestID: "finish-network-privacy", TS: time.Now().UTC(), Model: "test", CallType: "chat", Status: "success", HTTPStatus: 200, Cost: 0.25}
	if err := db.PrepareDelivery(t.Context(), record, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	writeErr := &net.OpError{Op: "write", Net: "tcp", Source: &net.TCPAddr{IP: net.ParseIP("10.20.30.40"), Port: 1234}, Addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.123"), Port: 5678}, Err: errors.New("broken pipe")}
	s.finishSyncDelivery(context.Background(), record, writeErr)
	event, err := db.GetUsageEvent(t.Context(), iam.UsageQuery{}, record.RequestID)
	if err != nil || event.Status != "error" || event.Cost != 0 {
		t.Fatalf("写失败不能扣费: event=%+v err=%v", event, err)
	}
	log, err := db.GetRequestLog(t.Context(), record.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{log.UpstreamResponse, log.Error} {
		if strings.Contains(value, "10.20.30.40") || strings.Contains(value, "198.51.100.123") {
			t.Fatalf("错误诊断不应包含 IP 地址: %s", value)
		}
		if !strings.Contains(value, "client_write_error") {
			t.Fatalf("缺少写入失败分类: %s", value)
		}
	}
}
