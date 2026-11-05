package gateway

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestCaptureUserID 验证只有会话和个人密钥可继承个人开关，共享或匿名身份不能猜测用户。
// 全部输入均为内存合成主体，无外部依赖或清理。
func TestCaptureUserID(t *testing.T) {
	for _, tc := range []struct {
		p    *auth.Principal
		want string
	}{
		{nil, ""}, {&auth.Principal{Kind: authz.KindSession, UserID: "u"}, "u"},
		{&auth.Principal{Kind: authz.KindKey, OwnerType: iam.OwnerPersonal, UserID: "u"}, "u"},
		{&auth.Principal{Kind: authz.KindKey, OwnerType: "team", UserID: "u"}, ""},
		{&auth.Principal{Kind: authz.KindKey, OwnerType: iam.OwnerPersonal}, ""},
	} {
		if got := captureUserID(tc.p); got != tc.want {
			t.Fatalf("个人记录身份错误: got=%q want=%q", got, tc.want)
		}
	}
}

// TestCaptureExchangeBoundaryStates 用合成文本、二进制、超大正文和过期时间验证明确降级状态。
// 前置隔离 PostgreSQL 和全局开启；任何分支都不修改请求或账单，测试结束清理 schema。
func TestCaptureExchangeBoundaryStates(t *testing.T) {
	db := testIdentityStore(t)
	s := &Server{Cfg: &config.Config{}, IAM: db}
	s.Cfg.GeneralSettings.StorePromptsInSpendLogs = true
	for _, tc := range []struct {
		name     string
		req      []byte
		response string
		start    time.Time
		want     string
	}{
		{"binary", []byte{0xff}, "response", time.Now(), "unsupported"},
		{"empty", nil, "", time.Now(), "empty"},
		{"large", bytes.Repeat([]byte("x"), 4<<20+1), "", time.Now(), "too_large"},
		{"stream", []byte(`{}`), strings.Repeat("x", 2<<20), time.Now(), "partial"},
		{"expired", []byte(`{}`), "answer", time.Now().Add(-31 * 24 * time.Hour), "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.BeginLogCapture(tc.name, nil)
			state, expiry := s.captureExchange(nil, tc.name, promptExchange{request: httptest.NewRequest("POST", "/v1/chat/completions", nil), requestBody: tc.req, rawResponse: tc.response}, tc.start, tc.name == "stream")
			if state != tc.want || expiry != "" {
				t.Fatalf("状态错误: got=%s expiry=%s want=%s", state, expiry, tc.want)
			}
		})
	}
	s.BeginLogCapture("large-nonstream", nil)
	state, expiry := s.captureExchange(nil, "large-nonstream", promptExchange{request: httptest.NewRequest("POST", "/v1/chat/completions", nil), requestBody: []byte(`{}`), rawResponse: strings.Repeat("x", 2<<20)}, time.Now(), false)
	if state != "saved" || expiry == "" {
		t.Fatalf("未截断的非流响应不应误报截断: %s", state)
	}
}

// TestCaptureQueueDoesNotResurrectExpiredBody 先在独立 Redis 排队，再清理隔离 PostgreSQL 的正文并重放账单。
// 验证队列不含合成正文、重放不重建正文且用量仍在；测试自动关闭 Redis 和清理数据库。
func TestCaptureQueueDoesNotResurrectExpiredBody(t *testing.T) {
	db := testIdentityStore(t)
	redis := settlementRedis(t)
	s := &Server{Cfg: &config.Config{}, IAM: db, Live: redis}
	s.Cfg.GeneralSettings.StorePromptsInSpendLogs = true
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	s.rememberExchange("queue-expired", req, []byte(`{"messages":[{"content":"private-queued-prompt"}]}`), []byte(`{"output":"private-queued-output"}`))
	s.recordSpend(httptest.NewRecorder(), nil, "queue-expired", "model", "chat", nil, time.Now(), false, 200, "")
	rows, raw := redis.PeekLogs(10)
	if len(rows) != 1 || len(raw) != 1 || rows[0].BodyCaptureStatus != "saved" || strings.Contains(raw[0], "private-queued") {
		t.Fatalf("账单队列携带正文或丢失关联: count=%d", len(rows))
	}
	if n, err := db.PurgeExpiredLogCaptures(context.Background(), time.Now().Add(31*24*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("模拟到期清理失败: %d %v", n, err)
	}
	s.FlushSpend()
	log, err := db.GetRequestLog(context.Background(), "queue-expired")
	if err != nil || strings.Contains(log.RequestBody+log.ResponseBody+log.ProxyRequest, "private-queued") {
		t.Fatalf("账单重放恢复了已清理正文: %v", err)
	}
	if _, err := db.GetUsageEvent(context.Background(), iam.UsageQuery{}, "queue-expired"); err != nil {
		t.Fatalf("重放丢失用量: %v", err)
	}
}

// TestCaptureCleanupLoopCleansAndStops 在私有数据库准备一条到期正文，验证后台维护会清理且能响应取消。
// 使用合成正文和短时上下文；协程退出后由夹具关闭连接并删除隔离 schema。
func TestCaptureCleanupLoopCleansAndStops(t *testing.T) {
	db := testIdentityStore(t)
	now := time.Now().UTC()
	state, err := db.SaveLogCapture(context.Background(), iam.LogCapture{RequestID: "cleanup-loop", Source: "global", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), Request: "synthetic"}, iam.CaptureLimits{PerRequest: 1024, PerUser: 1024, Global: 1024}, now.Add(-2*time.Hour))
	if err != nil || state != "saved" {
		t.Fatalf("准备过期正文失败: %s %v", state, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Server{IAM: db}).logCaptureCleanupLoop(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		count, queryErr := db.Engine.Table("request_log_captures").Where("request_id = ?", "cleanup-loop").Count()
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("后台清理未处理过期正文")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDisabledCaptureKeepsFailureSummary 在独立 PostgreSQL 写入真实失败用量，验证关闭正文时仍保留原因。
// 失败响应中的合成输入与输出不得进入请求、响应及错误字段；隔离 schema 自动清理。
func TestDisabledCaptureKeepsFailureSummary(t *testing.T) {
	db := testIdentityStore(t)
	s := &Server{Cfg: &config.Config{}, IAM: db}
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	raw := []byte(`{"error":{"code":"vendor_busy","message":"Vendor busy","request":{"messages":[{"content":"secret-input"}]}},"choices":[{"text":"secret-output"}]}`)
	s.BeginLogCapture("disabled-failure", nil)
	s.rememberExchange("disabled-failure", request, []byte(`{"messages":[{"content":"secret-input"}]}`), raw)
	s.recordSpend(httptest.NewRecorder(), nil, "disabled-failure", "model", "chat", nil, time.Now(), false, 502, "")
	log, err := db.GetRequestLog(context.Background(), "disabled-failure")
	if err != nil {
		t.Fatal(err)
	}
	if log.BodyCaptureStatus != "disabled" || log.RequestBody != "" || log.ResponseBody != "" || log.ProxyRequest != "" || !strings.Contains(log.Error, "vendor_busy") || strings.Contains(log.Error, "secret-input") || strings.Contains(log.Error, "secret-output") {
		t.Fatalf("失败日志错误摘要或正文边界不正确: %+v", log)
	}
}

// TestCaptureConfig 检查默认值、显式配置和无效配置；环境变量由测试自动恢复，不访问服务器。
func TestCaptureConfig(t *testing.T) {
	t.Setenv("XHUB_LOG_BODY_REQUEST_BYTES", "8192")
	t.Setenv("XHUB_LOG_BODY_GLOBAL_RETENTION_DAYS", "45")
	limits, days, err := captureConfig()
	if err != nil || limits.PerRequest != 8192 || days != 45 {
		t.Fatalf("配置未生效: %+v %d %v", limits, days, err)
	}
	t.Setenv("XHUB_LOG_BODY_GLOBAL_RETENTION_DAYS", "0")
	if _, _, err := captureConfig(); err == nil {
		t.Fatal("无效留存不能变成无限期")
	}
}
