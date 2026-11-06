package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
)

// browserQueueConflictControl 为私有浏览器夹具创建或修复一条确定性冲突账单。
// w/r 为控制端请求，s 为连接独占 Redis 和私有 schema 的网关；返回是否命中控制路径。
// 仅测试二进制调用；恢复通过正常结算完成，不能连接共享 Redis 或删除真实账单。
func browserQueueConflictControl(w http.ResponseWriter, r *http.Request, s *Server) bool {
	if r.URL.Path != "/usage/queue-conflict" {
		return false
	}
	var body struct{ Action, ID string }
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || !strings.HasPrefix(body.ID, "e2e-queue-conflict-") || s.Live == nil {
		http.Error(w, "private redis fixture and valid test id required", http.StatusBadRequest)
		return true
	}
	ctx := r.Context()
	var err error
	switch body.Action {
	case "start":
		record := iam.UsageRecord{RequestID: body.ID, TS: time.Now().UTC(), Model: "fixture-original", OwnerType: iam.OwnerPersonal, CallType: "chat", Status: "success", HTTPStatus: 200}
		if err = s.IAM.PrepareDelivery(ctx, record, time.Now().Add(time.Hour)); err == nil {
			record.Model = "fixture-conflict"
			var raw []byte
			raw, err = json.Marshal(record)
			if err == nil {
				err = s.Live.EnqueueConfirmedDelivery(live.SpendLog{RequestID: body.ID, SpendValid: true, Start: record.TS.Format(time.RFC3339Nano), DeliverySnapshot: string(raw)})
			}
		}
	case "flush":
		s.FlushSpend()
	case "resolve":
		_, err = s.IAM.Engine.Context(ctx).Exec(`UPDATE usage_delivery_pending SET payload=jsonb_set(payload, '{Model}', to_jsonb('fixture-conflict'::text)) WHERE request_id=?`, body.ID)
		if err == nil {
			for range 4 {
				s.FlushSpend()
			}
			_, err = s.IAM.GetUsageEvent(ctx, iam.UsageQuery{}, body.ID)
		}
	default:
		err = fmt.Errorf("unknown action")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	return true
}

// TestBrowserQueueConflictControl 验证私有夹具拒绝非法输入，并能创建和正常核对冲突记录。
// 使用独立 PostgreSQL 与临时 Redis；校验错误输入、保持冲突、恢复结算及清理，全部资源由测试回收。
func TestBrowserQueueConflictControl(t *testing.T) {
	_, _, db := testGatewayStores(t)
	client, err := live.Open(browserPrivateRedis(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	s := &Server{IAM: db, Live: client}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"action":"start","id":"outside-fixture"}`, 400},
		{`{"action":"unknown","id":"e2e-queue-conflict-unit"}`, 500},
		{`{"action":"start","id":"e2e-queue-conflict-unit"}`, 200},
		{`{"action":"flush","id":"e2e-queue-conflict-unit"}`, 200},
		{`{"action":"resolve","id":"e2e-queue-conflict-unit"}`, 200},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/usage/queue-conflict", strings.NewReader(tc.body))
		if !browserQueueConflictControl(w, r, s) || w.Code != tc.want {
			t.Fatalf("隔离控制响应不符: body=%s status=%d want=%d", tc.body, w.Code, tc.want)
		}
	}
	if rows, _ := client.PeekLogs(1); len(rows) != 0 {
		t.Fatalf("恢复后不应残留测试账单: rows=%+v", rows)
	}
}
