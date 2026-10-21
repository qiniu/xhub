package regression

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/gateway"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestUsageSpoolDatabaseDisconnect 验证上游已接收后整个IAM连接池关闭、无Redis或Redis队列损坏时仍保存完整账单。
// 前置私有PG和本地供应商，覆盖同步/流式及有无硬额度；新连接、新网关启动自动恢复，重复Flush不重复费用或上游，最后清理schema/磁盘/连接。
func TestUsageSpoolDatabaseDisconnect(t *testing.T) {
	for _, protocol := range []string{"chat/completions", "responses", "messages"} {
		for _, useRedis := range []bool{false, true} {
			for _, hard := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/Redis=%v/hard=%v/stream=%v", protocol, useRedis, hard, stream), func(t *testing.T) {
						dir := filepath.Join(t.TempDir(), "spool")
						t.Setenv("XHUB_USAGE_SPOOL_DIR", dir)
						t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
						var breakRedis func()
						if useRedis {
							rdb := isolatedSpendRedis(t)
							breakRedis = func() {
								if err := rdb.Set(t.Context(), "xhub:spendlog", "broken-queue", 0).Err(); err != nil {
									t.Error(err)
								}
							}
						}
						const model = "regression-disk-recovery"
						dep := chatDeployment(model)
						dep.ModelInfo["endpoint_types"] = []string{"chat", "responses", "messages"}
						h := newHarness(t, dep)
						t.Cleanup(func() { _ = h.gw.CloseUsageSpool() })
						admin := h.adminSession()
						scope := h.openScope(t, admin, "disk-recovery")
						if hard {
							h.setKeyBudget(t, admin, scope.key, 10)
						}
						before := h.moneyOf(t, scope)
						h.onUpstream(model, func() {
							if breakRedis != nil {
								breakRedis()
							}
							_ = h.db.Engine.Close()
						})
						request := chatRequest(model, "Verify independent durable billing recovery.")
						if protocol == "responses" {
							request = map[string]any{"model": model, "input": "Verify independent durable billing recovery."}
						}
						if protocol == "messages" {
							request["max_tokens"] = 128
						}
						request["stream"] = stream
						response := h.ok(http.MethodPost, "/v1/"+protocol, scope.key, request)
						callID := response.header("x-litellm-call-id")
						if callID == "" || len(h.upstreamCalls()) != 1 {
							t.Fatal("真实推理未完成唯一调用")
						}
						h.server.Close() // 等待SSE处理器完成本机落盘，再重建实例。
						files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
						if len(files) != 1 {
							t.Fatalf("PG完全不可写时磁盘账单数=%d", len(files))
						}
						snapshot, err := os.ReadFile(files[0])
						if err != nil {
							t.Fatal(err)
						}
						_ = h.gw.CloseUsageSpool()
						reopened, err := iam.Open(t.Context(), h.gw.Cfg.GeneralSettings.DatabaseURL)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = reopened.Close() })
						n, err := reopened.Engine.Table("usage_pending").Count()
						if err != nil || n != 0 {
							t.Fatalf("本场景必须证明PG pending也不可写: n=%d err=%v", n, err)
						}
						holds, err := reopened.PendingKeyBudget(t.Context(), scope.keyID)
						if err != nil || len(holds) != 0 {
							t.Fatalf("断库期间预占证据错误: holds=%d err=%v", len(holds), err)
						}
						fresh := gateway.New(h.gw.Cfg, h.store, reopened)
						if fresh.Live != nil {
							t.Cleanup(func() { _ = fresh.Live.Close() })
						}
						t.Cleanup(func() { _ = fresh.CloseUsageSpool() })
						// 模拟数据库提交完成、删除磁盘文件前进程退出，重放相同快照必须由账单幂等键去重。
						if err = os.WriteFile(files[0], snapshot, 0600); err != nil {
							t.Fatal(err)
						}
						for range 3 {
							fresh.FlushSpend()
						}
						files, _ = filepath.Glob(filepath.Join(dir, "*.json"))
						if len(files) != 0 {
							t.Fatalf("恢复未确认磁盘快照: %d", len(files))
						}
						holds, err = reopened.PendingKeyBudget(t.Context(), scope.keyID)
						if err != nil || len(holds) != 0 {
							t.Fatalf("完整账单恢复未释放已结算占用: holds=%d err=%v", len(holds), err)
						}
						h.db.Engine = reopened.Engine // 原HTTP鉴权持有相同DB对象，恢复其连接以验证用户可见查询。
						if after := h.moneyOf(t, scope); !after.grewBy(before, expectedCost()) {
							t.Fatalf("五层费用未唯一结算: before=%+v after=%+v", before, after)
						}
						rows, err := reopened.Engine.Table("usage_events").Where("request_id = ?", callID).Count()
						if err != nil || rows != 1 {
							t.Fatalf("账单非唯一: n=%d err=%v", rows, err)
						}
						log, err := reopened.GetRequestLog(t.Context(), callID)
						if err != nil || !strings.Contains(log.RequestBody, "Verify independent") || log.ResponseBody == "" {
							t.Fatalf("完整日志未恢复: err=%v", err)
						}
						if len(h.upstreamCalls()) != 1 {
							t.Fatal("重放错误地重试上游")
						}
					})
				}
			}
		}
	}
}

// TestUsageSpoolUnavailableAdmission 验证不可写磁盘在真实HTTP准入时返回503且不调用上游；恢复权限后可继续对话。
// 使用临时目录及私有PG，硬额度与无限额账号均走相同存储准入；最后清理所有测试数据。
func TestUsageSpoolUnavailableAdmission(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	t.Setenv("XHUB_USAGE_SPOOL_DIR", dir)
	t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
	h := newHarness(t, chatDeployment("spool-admission"))
	t.Cleanup(func() { _ = h.gw.CloseUsageSpool() })
	admin := h.adminSession()
	scope := h.openScope(t, admin, "spool-admission")
	h.setKeyBudget(t, admin, scope.key, 10)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	for _, key := range []string{scope.key, scope.session} {
		r := h.do(http.MethodPost, "/v1/chat/completions", key, chatRequest("spool-admission", "Verify durable admission."))
		if r.status != 503 || !strings.Contains(string(r.body), "billing_storage_unavailable") {
			t.Fatalf("存储故障未明确拒绝: %s", r.describe())
		}
	}
	if len(h.upstreamCalls()) != 0 {
		t.Fatal("存储不可用仍调用付费上游")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	h.ok(http.MethodPost, "/v1/chat/completions", scope.key, chatRequest("spool-admission", "Verify restored admission."))
}
