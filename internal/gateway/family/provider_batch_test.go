package family

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProviderBatchMatch 验证四个模板及尾斜杠，失败/近似路径不被接管；纯HTTP记录器，无外部数据与清理。
func TestProviderBatchMatch(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{"GET", "/vendor/v1/batches"}, {"POST", "/vendor/v1/batches"}, {"GET", "/vendor/v1/batches/job"}, {"POST", "/vendor/v1/batches/job/cancel"},
	} {
		for _, suffix := range []string{"", "/"} {
			w := httptest.NewRecorder()
			if !rejectProviderBatch(w, httptest.NewRequest(tc.method, tc.path+suffix, nil)) || w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "unsupported_operation") {
				t.Fatalf("旧批处理未明确拒绝: %+v %s", tc, w.Body)
			}
		}
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/vendor/v1/batches/job/cancel"}, {"POST", "/vendor/v1/batches/job"}, {"DELETE", "/vendor/v1/batches"},
		{"GET", "/vendor/v1/batches//cancel"}, {"POST", "/vendor/v1/batches/job/retry"}, {"GET", "/vendor/v1/batches/job/content/extra"},
		{"POST", "/v1/batches"}, {"POST", "/batches"}, {"POST", "/vendor/v2/batches"}, {"POST", "/vendor/v1/batches-extra"},
		{"POST", "/bypass/openai/v1/batches"}, {"GET", "//v1/batches"}, {"GET", "/vendor/v1/files"},
	} {
		w := httptest.NewRecorder()
		if rejectProviderBatch(w, httptest.NewRequest(tc.method, tc.path, nil)) || w.Body.Len() != 0 {
			t.Fatalf("错误接管其他路径: %+v", tc)
		}
	}
}

// TestProviderBatchStoreBoundary 验证缺失或故障存储仍明确501且不能写入；内存故障注入只用于单元边界，无数据库清理。
func TestProviderBatchStoreBoundary(t *testing.T) {
	for _, st := range []*catalogMemoryStore{nil, {rows: map[string]map[string]any{"cancel": {"name": "keep"}}, getErr: errors.New("private-sql"), putErr: errors.New("private-sql")}} {
		var store catalogRecordStore
		if st != nil {
			store = st
		}
		for _, tc := range []struct{ method, path string }{{"GET", "/vendor/v1/batches/job"}, {"POST", "/vendor/v1/batches/job/cancel"}} {
			w := httptest.NewRecorder()
			writeCatalogStore(store, w, httptest.NewRequest(tc.method, tc.path, nil), map[string]any{"private_fixture": "must-not-echo"})
			if w.Code != 501 || strings.Contains(w.Body.String(), "private-sql") || strings.Contains(w.Body.String(), "must-not-echo") {
				t.Fatal("旧任务错误传播或正文回显", w)
			}
		}
		if st != nil && (st.putCalls != 0 || len(st.deleteIDs) != 0 || st.rows["cancel"]["name"] != "keep") {
			t.Fatal("旧任务操作存储", st)
		}
	}
}

// TestProviderBatchJSONValidation 验证管理对象JSON和JSON-RPC检查仍优先；授权内存宿主无存储，不需清理。
func TestProviderBatchJSONValidation(t *testing.T) {
	for _, body := range []string{`{`, `null`, `[]`, `{"jsonrpc":"2.0","method":"submit"}`} {
		w := httptest.NewRecorder()
		ServeMgmt(catalogPersistenceHost{}, w, httptest.NewRequest("POST", "/vendor/v1/batches", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal("JSON验证改变", body, w)
		}
	}
}
