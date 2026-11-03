package regression

import (
	"net/http"
	"strings"
	"testing"
)

// TestLogDetailStorageFailure 验证真实网关在正文缺行、数据库读取故障和恢复后的详情契约与权限。
// 前置独立 PostgreSQL schema 和本地上游；创建请求后模拟只影响正文表的故障，结束由清理函数恢复表及测试库。
func TestLogDetailStorageFailure(t *testing.T) {
	h := newHarness(t, chatDeployment("detail-storage"))
	admin := h.adminSession()
	owner := h.provision(t, admin, "detail-storage-owner")
	other := h.provision(t, admin, "detail-storage-other")
	response := h.ok(http.MethodPost, "/v1/chat/completions", owner.key, chatRequest("detail-storage", "Check detail storage availability."))
	h.flushSpend()
	id := response.header("x-litellm-call-id")
	if id == "" {
		t.Fatal("模型请求未返回关联 ID")
	}
	path := "/spend/logs/ui/" + id
	if got := h.do(http.MethodGet, path, owner.session, nil); got.status != http.StatusOK {
		t.Fatalf("正常详情应可读取: %s", got.describe())
	}
	if got := h.do(http.MethodGet, path, other.session, nil); got.status != http.StatusNotFound {
		t.Fatalf("其他用户不得读取详情: %s", got.describe())
	}
	// 历史正文可能被清理，但用量元数据仍应作为 200 返回；测试仅删除隔离请求的数据。
	if _, err := h.db.Engine.Exec("DELETE FROM request_logs WHERE request_id = ?", id); err != nil {
		t.Fatal(err)
	}
	missing := h.do(http.MethodGet, path, owner.session, nil)
	if missing.status != http.StatusOK || missing.json()["request_id"] != id {
		t.Fatalf("正文缺行时应保留用量摘要: %s", missing.describe())
	}
	// 重命名测试 schema 中的正文表，不影响用量表，验证失败不能伪装成无正文成功。
	catalogSQL(t, h, `ALTER TABLE request_logs RENAME TO request_logs_detail_unavailable`)
	restored := false
	t.Cleanup(func() {
		if !restored {
			catalogSQL(t, h, `ALTER TABLE request_logs_detail_unavailable RENAME TO request_logs`)
		}
	})
	failed := h.do(http.MethodGet, path, owner.session, nil)
	if failed.status != http.StatusInternalServerError {
		t.Fatalf("正文存储失败必须返回 500 而不是空详情: %s", failed.describe())
	}
	if strings.Contains(failed.text(), "request_logs_detail_unavailable") || strings.Contains(failed.text(), "SQL") {
		t.Fatal("对客户端泄露了内部数据库信息")
	}
	if got := h.do(http.MethodGet, path, other.session, nil); got.status != http.StatusNotFound {
		t.Fatalf("存储故障不得改变越权 404: %s", got.describe())
	}
	catalogSQL(t, h, `ALTER TABLE request_logs_detail_unavailable RENAME TO request_logs`)
	restored = true
	if got := h.do(http.MethodGet, path, owner.session, nil); got.status != http.StatusOK {
		t.Fatalf("存储恢复后应重新读取摘要: %s", got.describe())
	}
}
