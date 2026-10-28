package family

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMedicalAliasMatch 验证根路径、操作名及尾斜杠拒绝，方法/相似前缀/多级路径不被接管；纯HTTP记录器，无需清理。
func TestMedicalAliasMatch(t *testing.T) {
	for _, path := range []string{"/comprehendmedical", "/comprehendmedical/detect-entities-v2", "/comprehendmedical/info", "/comprehendmedical/delete", "/comprehendmedical/update", "/comprehendmedical/unknown"} {
		for _, suffix := range []string{"", "/"} {
			w := httptest.NewRecorder()
			if !rejectMedicalAlias(w, httptest.NewRequest("POST", path+suffix, nil)) || w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "unsupported_operation") {
				t.Fatalf("未拒绝旧分析操作 %s: %s", path, w.Body)
			}
		}
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/comprehendmedical"}, {"DELETE", "/comprehendmedical/delete"}, {"PUT", "/comprehendmedical/update"},
		{"POST", "/comprehendmedical-other"}, {"POST", "/comprehendmedical/a/b"}, {"POST", "/comprehendmedical//"},
		{"POST", "/v1/comprehendmedical"}, {"POST", "/bypass/bedrock/comprehendmedical"}, {"POST", "/v1/chat/completions"},
	} {
		w := httptest.NewRecorder()
		if rejectMedicalAlias(w, httptest.NewRequest(tc.method, tc.path, nil)) || w.Body.Len() != 0 {
			t.Fatalf("误接管其他路径 %+v", tc)
		}
	}
}

// TestMedicalAliasStoreBoundary 验证缺失或故障存储不能触发配置读写删，错误不泄漏正文；内存故障夹具，无外部清理。
func TestMedicalAliasStoreBoundary(t *testing.T) {
	for _, st := range []*catalogMemoryStore{nil, {rows: map[string]map[string]any{"sentinel": {"name": "keep"}}, getErr: errors.New("private-sql"), putErr: errors.New("private-sql")}} {
		var store catalogRecordStore
		if st != nil {
			store = st
		}
		for _, path := range []string{"/comprehendmedical", "/comprehendmedical/info", "/comprehendmedical/delete"} {
			w := httptest.NewRecorder()
			writeCatalogStore(store, w, httptest.NewRequest("POST", path, nil), map[string]any{"id": "sentinel", "Text": "private-fixture"})
			if w.Code != 501 || strings.Contains(w.Body.String(), "private-sql") || strings.Contains(w.Body.String(), "private-fixture") {
				t.Fatal("分析入口错误或正文回显", w)
			}
		}
		if st != nil && (st.putCalls != 0 || len(st.deleteIDs) != 0 || st.rows["sentinel"]["name"] != "keep") {
			t.Fatal("分析入口操作了配置", st)
		}
	}
}

// TestMedicalAliasJSONValidation 验证原对象JSON及JSON-RPC检查仍在拒绝前执行；授权无存储宿主，无清理副作用。
func TestMedicalAliasJSONValidation(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `null`, `{"jsonrpc":"2.0","method":"analyze"}`} {
		w := httptest.NewRecorder()
		ServeMgmt(catalogPersistenceHost{}, w, httptest.NewRequest("POST", "/comprehendmedical", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal("原JSON检查改变", body, w)
		}
	}
}
