package family

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCatalogProtocolRejectsRPC 验证管理资源拒绝顶层RPC信封，不执行方法、不读写目录或伪造result。
// 参数t为测试上下文；纯内存窄存储覆盖请求、通知及坏版本，失败前后记录不变，无外部数据需清理。
func TestCatalogProtocolRejectsRPC(t *testing.T) {
	for _, body := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": nil, "id": "bad", "method": "unknown"},
	} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			st := &catalogMemoryStore{rows: map[string]map[string]any{"original": {"id": "original", "note": "keep"}}}
			w := httptest.NewRecorder()
			writeCatalogStore(st, w, httptest.NewRequest(method, "/search_tools/original", nil), body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"type":"unsupported_protocol"`) || strings.Contains(w.Body.String(), `"result"`) {
				t.Errorf("REST目录不得伪装RPC成功: method=%s code=%d body=%s", method, w.Code, w.Body.String())
			}
			if st.putCalls != 0 || len(st.deleteIDs) != 0 || len(st.rows) != 1 {
				t.Fatal("RPC拒绝路径触碰了存储")
			}
		}
	}
}

// TestCatalogProtocolTokenResource 验证token只是普通资源ID，正常创建读改删及存储失败不能被路径启发式替换成令牌。
// 参数t为测试上下文；内存记录验证实际持久化和清理，嵌套jsonrpc是合法资源数据，非RPC请求。
func TestCatalogProtocolTokenResource(t *testing.T) {
	st := &catalogMemoryStore{rows: map[string]map[string]any{}}
	for _, method := range []string{"PUT", "GET", "DELETE"} {
		w := httptest.NewRecorder()
		writeCatalogStore(st, w, httptest.NewRequest(method, "/search_tools/token", nil), map[string]any{"note": "saved", "metadata": map[string]any{"jsonrpc": "2.0"}})
		if w.Code != 200 || strings.Contains(w.Body.String(), `"access_token"`) {
			t.Fatalf("普通token资源被冒充发令牌: %s %d %s", method, w.Code, w.Body.String())
		}
		if method != "DELETE" && st.rows["token"]["note"] != "saved" {
			t.Fatal("普通token资源没有保存")
		}
	}
	if len(st.rows) != 0 {
		t.Fatal("资源删除未生效")
	}
	w := httptest.NewRecorder()
	writeCatalogStore(nil, w, httptest.NewRequest("PUT", "/search_tools/token", nil), nil)
	if w.Code != 503 {
		t.Fatalf("无存储仍冒充token成功: %d %s", w.Code, w.Body.String())
	}
}
