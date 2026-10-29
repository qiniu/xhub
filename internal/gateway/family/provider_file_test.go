package family

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProviderFileMatch 验证五个模板及尾斜杠，失败/近似路径不被接管；纯HTTP记录器，无外部数据与清理。
func TestProviderFileMatch(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{"GET", "/vendor/v1/files"}, {"POST", "/vendor/v1/files"}, {"GET", "/vendor/v1/files/job"}, {"GET", "/vendor/v1/files/job/content"}, {"DELETE", "/vendor/v1/files/job"},
	} {
		for _, suffix := range []string{"", "/"} {
			w := httptest.NewRecorder()
			if !rejectProviderFile(w, httptest.NewRequest(tc.method, tc.path+suffix, nil)) || w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "unsupported_operation") {
				t.Fatalf("旧文件未明确拒绝: %+v %s", tc, w.Body)
			}
		}
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/vendor/v1/files/job/cancel"}, {"POST", "/vendor/v1/files/job"}, {"DELETE", "/vendor/v1/files"},
		{"GET", "/vendor/v1/files//cancel"}, {"POST", "/vendor/v1/files/job/retry"}, {"GET", "/vendor/v1/files/job/content/extra"},
		{"POST", "/v1/files"}, {"POST", "/files"}, {"POST", "/vendor/v2/files"}, {"POST", "/vendor/v1/files-extra"},
		{"POST", "/bypass/openai/v1/files"}, {"GET", "//v1/files"}, {"GET", "/vendor/v1/batches"},
	} {
		w := httptest.NewRecorder()
		if rejectProviderFile(w, httptest.NewRequest(tc.method, tc.path, nil)) || w.Body.Len() != 0 {
			t.Fatalf("错误接管其他路径: %+v", tc)
		}
	}
}

// TestProviderFileStoreBoundary 验证缺失或故障存储仍明确501且不能写入；内存故障注入只用于单元边界，无数据库清理。
func TestProviderFileStoreBoundary(t *testing.T) {
	for _, st := range []*catalogMemoryStore{nil, {rows: map[string]map[string]any{"content": {"name": "keep"}}, getErr: errors.New("private-sql"), putErr: errors.New("private-sql")}} {
		var store catalogRecordStore
		if st != nil {
			store = st
		}
		for _, tc := range []struct{ method, path string }{{"GET", "/vendor/v1/files/job"}, {"GET", "/vendor/v1/files/job/content"}, {"DELETE", "/vendor/v1/files/job"}} {
			w := httptest.NewRecorder()
			writeCatalogStore(store, w, httptest.NewRequest(tc.method, tc.path, nil), map[string]any{"private_fixture": "must-not-echo"})
			if w.Code != 501 || strings.Contains(w.Body.String(), "private-sql") || strings.Contains(w.Body.String(), "must-not-echo") {
				t.Fatal("旧文件错误传播或正文回显", w)
			}
		}
		if st != nil && (st.putCalls != 0 || len(st.deleteIDs) != 0 || st.rows["content"]["name"] != "keep") {
			t.Fatal("旧文件操作存储", st)
		}
	}
}

// TestProviderFileJSONValidation 验证管理对象JSON和JSON-RPC检查仍优先；授权内存宿主无存储，不需清理。
func TestProviderFileJSONValidation(t *testing.T) {
	for _, body := range []string{`{`, `null`, `[]`, `{"jsonrpc":"2.0","method":"submit"}`} {
		w := httptest.NewRecorder()
		ServeMgmt(catalogPersistenceHost{}, w, httptest.NewRequest("POST", "/vendor/v1/files", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal("JSON验证改变", body, w)
		}
	}
}
