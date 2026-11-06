package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/gateway"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/store"
)

// crashDeliveryWriter 在真实响应正文写出前终止隔离子进程；持有请求 ID 路径，不接触用户凭据。
type crashDeliveryWriter struct {
	header http.Header
	idPath string
}

// Header 返回当前响应头，供真实网关写入调用 ID；不访问网络或磁盘。
func (w *crashDeliveryWriter) Header() http.Header { return w.header }

// WriteHeader 保持响应待写状态；子进程测试不发送真实网络响应。
func (w *crashDeliveryWriter) WriteHeader(int) {}

// Write 先保存测试调用 ID 再直接退出进程，模拟写出终态无法确认；不保存正文。
func (w *crashDeliveryWriter) Write(_ []byte) (int, error) {
	if os.WriteFile(w.idPath, []byte(w.header.Get("x-litellm-call-id")), 0600) != nil {
		os.Exit(88)
	}
	os.Exit(87)
	return 0, nil
}

// TestSyncDeliveryCrashRecovery 使用真实网关、私有 PostgreSQL、子进程和本地上游，验证写响应前进程退出后恢复零费用。
// 覆盖标准及透传接口；父进程保留隔离服务、检查唯一上游调用，测试结束回收 schema 和临时文件。
func TestSyncDeliveryCrashRecovery(t *testing.T) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := chatDeployment("sync-crash")
	dep.ModelInfo["transport"] = "bypass_openai_chat"
	dep.ModelInfo["endpoint_types"] = []string{"chat", "bypass:openai-chat"}
	h := newHarness(t, dep)
	owner := h.openScope(t, h.adminSession(), "sync-crash")
	tmp := t.TempDir()
	configBytes, err := json.Marshal(h.gw.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configFile, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/chat/completions", "/bypass/openai/v1/chat/completions"} {
		idFile := filepath.Join(tmp, "call-id")
		_ = os.Remove(idFile)
		child := exec.Command(os.Args[0], "-test.run=^TestSyncDeliveryCrashHelper$")
		child.Env = append(os.Environ(), "XHUB_TEST_SYNC_CHILD=1", "XHUB_TEST_SYNC_CONFIG="+configFile, "XHUB_TEST_SYNC_KEY="+owner.key, "XHUB_TEST_SYNC_PATH="+path, "XHUB_TEST_SYNC_ID="+idFile)
		err := child.Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 87 {
			t.Fatalf("模拟写出前退出失败: %v", err)
		}
		idRaw, err := os.ReadFile(idFile)
		if err != nil || len(idRaw) == 0 {
			t.Fatalf("未记录子进程调用 ID: %v", err)
		}
		id := string(idRaw)
		if n, err := h.db.Engine.Table("usage_events").Where("request_id = ?", id).Count(); err != nil || n != 0 {
			t.Fatalf("未确认前错误计费: n=%d err=%v", n, err)
		}
		if n, err := h.db.RecoverExpiredDeliveries(t.Context(), time.Now().Add(25*time.Hour), 100); err != nil || n != 1 {
			t.Fatalf("崩溃标记未恢复: n=%d err=%v", n, err)
		}
		if err := h.db.FlushPendingUsage(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
		event, err := h.db.GetUsageEvent(t.Context(), iam.UsageQuery{}, id)
		if err != nil || event.Status != "error" || event.Cost != 0 {
			t.Fatalf("崩溃恢复结果错误: %+v %v", event, err)
		}
	}
	key, err := h.db.GetKey(t.Context(), owner.keyID)
	if err != nil || key.Spend != 0 || len(h.upstreamCalls()) != 2 {
		t.Fatalf("崩溃后重复调用或扣费: calls=%d err=%v", len(h.upstreamCalls()), err)
	}
}

// TestSyncDeliveryCrashHelper 仅由父测试启动，接入父测试的隔离数据库及本地供应商并在响应写出前退出。
// 所有凭据和地址均是临时测试数据，父测试负责资源回收，不将对话内容写入文件。
func TestSyncDeliveryCrashHelper(t *testing.T) {
	if os.Getenv("XHUB_TEST_SYNC_CHILD") != "1" {
		return
	}
	raw, err := os.ReadFile(os.Getenv("XHUB_TEST_SYNC_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	db, err := iam.Open(t.Context(), cfg.GeneralSettings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.GeneralSettings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	s := gateway.New(&cfg, st, db)
	req := httptest.NewRequest("POST", os.Getenv("XHUB_TEST_SYNC_PATH"), strings.NewReader(`{"model":"sync-crash","messages":[{"role":"user","content":"Verify crash recovery."}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("XHUB_TEST_SYNC_KEY"))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(&crashDeliveryWriter{header: make(http.Header), idPath: os.Getenv("XHUB_TEST_SYNC_ID")}, req)
	t.Fatal("预期在响应写出前终止进程")
}

// TestSyncDeliveryCrashDuringUpstream 验证付费请求已经抵达上游后网关进程崩溃，仍有零费用恢复证据。
// 前置私有 PostgreSQL 和本机阻塞上游，通过子进程真实中断数据面；父测试释放上游并清理 schema。
func TestSyncDeliveryCrashDuringUpstream(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/bypass/openai/v1/chat/completions"} {
		for _, disk := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/disk=%t", path, disk), func(t *testing.T) { testSyncCrashDuringUpstream(t, path, disk) })
		}
	}
}

// testSyncCrashDuringUpstream 执行单条标准或透传路径的真实子进程中断回归。
// 参数 t 为隔离测试上下文、path 为入站地址、disk 决定是否使用独立队列；父测试回收私有资源。
func testSyncCrashDuringUpstream(t *testing.T, path string, disk bool) {
	t.Setenv("XHUB_USAGE_SPOOL_DIR", "")
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	dep := chatDeployment("sync-crash")
	dep.ModelInfo["transport"] = "bypass_openai_chat"
	dep.ModelInfo["endpoint_types"] = []string{"chat", "bypass:openai-chat"}
	h := newHarness(t, dep)
	owner := h.openScope(t, h.adminSession(), "sync-crash")
	started, release := make(chan struct{}), make(chan struct{})
	h.onUpstream("sync-crash", func() { close(started); <-release })
	defer close(release)
	tmp := t.TempDir()
	configBytes, err := json.Marshal(h.gw.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configFile, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestSyncDeliveryCrashHelper$")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "XHUB_USAGE_SPOOL_DIR=") {
			child.Env = append(child.Env, env)
		}
	}
	spoolDir := ""
	if disk {
		spoolDir = filepath.Join(tmp, "child-spool")
	}
	child.Env = append(child.Env, "XHUB_USAGE_SPOOL_DIR="+spoolDir, "XHUB_TEST_SYNC_CHILD=1", "XHUB_TEST_SYNC_CONFIG="+configFile, "XHUB_TEST_SYNC_KEY="+owner.key, "XHUB_TEST_SYNC_PATH="+path, "XHUB_TEST_SYNC_ID="+filepath.Join(tmp, "id"))
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("请求未到达隔离上游")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	rows, err := h.db.Engine.QueryString("SELECT request_id FROM usage_delivery_pending")
	if err != nil || len(rows) != 1 {
		t.Fatalf("上游处理中崩溃缺少恢复标记: n=%d err=%v", len(rows), err)
	}
	if disk {
		t.Setenv("XHUB_USAGE_SPOOL_DIR", spoolDir)
		fresh := gateway.New(h.gw.Cfg, h.store, h.db)
		t.Cleanup(func() {
			_ = fresh.CloseUsageSpool()
			if fresh.Live != nil {
				_ = fresh.Live.Close()
			}
		})
		fresh.FlushSpend()
	} else if n, err := h.db.RecoverExpiredDeliveries(t.Context(), time.Now().Add(25*time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("崩溃恢复失败: n=%d err=%v", n, err)
	}
	if err := h.db.FlushPendingUsage(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	event, err := h.db.GetUsageEvent(t.Context(), iam.UsageQuery{}, rows[0]["request_id"])
	if err != nil || event.Status != "error" || event.Cost != 0 {
		t.Fatalf("上游处理中崩溃未记零费用失败: %+v %v", event, err)
	}
}
