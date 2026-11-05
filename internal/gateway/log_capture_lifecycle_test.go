package gateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
)

// TestAsyncFailureCapture 回归验证：真实 PostgreSQL 保存初始任务及失败轮询，验证正文与最终状态。
// 使用合成正文和现有记账边界，不访问外部上游，私有 schema 自动清理。
func TestAsyncFailureCapture(t *testing.T) {
	db := testIdentityStore(t)
	s := &Server{Cfg: &config.Config{}, IAM: db}
	s.Cfg.GeneralSettings.StorePromptsInSpendLogs = true
	start := time.Now().UTC()
	s.BeginLogCapture(context.Background(), "capture-initial", nil)
	s.rememberExchange("capture-initial", httptest.NewRequest("POST", "/bypass/openai/v1/videos", nil), []byte(`{"model":"synthetic-video","prompt":"Synthetic request."}`), []byte(`{"id":"video-test","status":"queued"}`))
	s.AnnotateCall("capture-initial", dpcontract.CallNote{TaskRequestID: "capture-initial", TaskStatus: "executing", TaskInitial: true})
	s.recordSpend(httptest.NewRecorder(), nil, "capture-initial", "synthetic-video", "official:create", nil, start, false, 200, "")
	s.BeginLogCapture(context.Background(), "capture-poll", nil)
	s.rememberExchange("capture-poll", httptest.NewRequest("GET", "/bypass/openai/v1/videos/video-test", nil), nil, []byte(`{"id":"video-test","status":"failed","error":{"code":"render_failed","message":"Synthetic render failure."}}`))
	s.AnnotateCall("capture-poll", dpcontract.CallNote{TaskRequestID: "capture-initial", TaskStatus: "failed"})
	s.recordSpend(httptest.NewRecorder(), nil, "capture-poll", "synthetic-video", "official:query", nil, start, false, 200, "")
	s.ReleaseCall("capture-poll")
	row, err := db.GetRequestLog(context.Background(), "capture-initial")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("capture=%s response=%s error=%s", row.BodyCaptureStatus, row.ResponseBody, row.Error)
	if !strings.Contains(row.Error, "render_failed") || !strings.Contains(row.ResponseBody, "queued") {
		t.Fatal("初始响应或最终错误摘要丢失")
	}
}

// TestCaptureConfigLookupBounded 回归验证：独立数据库锁住配置表，验证记录策略查询是否受一秒故障降级约束。
// 持锁期间查询应超时降级；测试结束释放锁并等待协程退出，schema 自动清理。
func TestCaptureConfigLookupBounded(t *testing.T) {
	cfg, st, db := testGatewayStores(t)
	cfg.GeneralSettings.StorePromptsInSpendLogs = false
	s := &Server{Cfg: cfg, Store: st, IAM: db}
	tx := st.Engine.NewSession()
	defer tx.Close()
	if err := tx.Begin(); err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("LOCK TABLE proxy_config IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	start := time.Now()
	go func() { s.BeginLogCapture(context.Background(), "capture-lock", nil); close(done) }()
	select {
	case <-done:
		if s.capturePolicies["capture-lock"].status != "settings_unavailable" {
			t.Fatal("配置查询失败未显式降级")
		}
		t.Logf("策略查询在 %v 内返回", time.Since(start))
	case <-time.After(2200 * time.Millisecond):
		_ = tx.Rollback()
		<-done
		s.ReleaseCall("capture-lock")
		t.Fatalf("正文策略查询阻塞 %v，直到释放配置表锁才结束，一秒超时未覆盖全局设置查询", time.Since(start))
	}
	s.ReleaseCall("capture-lock")
}

// TestCaptureSnapshotSurvivesRelease 验证执行器释放后仅同一请求 ID 可恢复开始时的关闭选择。
// 全部状态在内存中构造；模拟管理员中途开启，不读写数据库，ReleaseCall 必须清空共享 map。
func TestCaptureSnapshotSurvivesRelease(t *testing.T) {
	s := &Server{Cfg: &config.Config{}}
	snapshot := &captureSnapshot{}
	ctx := context.WithValue(context.Background(), captureSnapshotKey{}, snapshot)
	s.BeginLogCapture(ctx, "snapshot-call", nil)
	s.ReleaseCall("snapshot-call")
	if len(s.capturePolicies) != 0 {
		t.Fatal("执行器未清理策略 map")
	}
	s.Cfg.GeneralSettings.StorePromptsInSpendLogs = true
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	ex := promptExchange{request: req, requestBody: []byte(`{"model":"missing"}`), rawResponse: `{"error":"model missing"}`}
	for _, tc := range []struct{ id, want string }{{"snapshot-call", "disabled"}, {"other-call", "storage_failed"}} {
		if state, _ := s.captureExchange(nil, tc.id, ex, time.Now(), false, true); state != tc.want {
			t.Fatalf("策略隔离错误 id=%s got=%s want=%s", tc.id, state, tc.want)
		}
	}
	// 未经 HTTP 宿主调用的空上下文也必须正常回收，不依赖外层 defer。
	s.BeginLogCapture(context.Background(), "direct-call", nil)
	s.ReleaseCall("direct-call")
	if len(s.capturePolicies) != 0 {
		t.Fatal("直接调用路径泄漏策略")
	}
}
