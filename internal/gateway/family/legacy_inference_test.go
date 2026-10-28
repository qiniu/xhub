package family

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLegacyInferenceMatch 验证六个旧POST及尾斜杠被拒绝，普通读取和原生/近似入口不被接管；无外部资源，无需清理。
func TestLegacyInferenceMatch(t *testing.T) {
	for _, path := range []string{"/rag/ingest", "/rag/query", "/ocr", "/models/demo:generateContent", "/models/demo:streamGenerateContent", "/models/demo:countTokens", "/models/demo:version:generateContent"} {
		for _, suffix := range []string{"", "/"} {
			w := httptest.NewRecorder()
			if !rejectLegacyInference(w, httptest.NewRequest("POST", path+suffix, nil)) || w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "unsupported_operation") {
				t.Fatalf("旧操作未拒绝 %s: %v", path+suffix, w)
			}
		}
	}
	for _, tc := range []struct{ method, path string }{{"GET", "/models/demo"}, {"GET", "/ocr"}, {"DELETE", "/rag/query"}, {"PUT", "/rag/ingest"}, {"POST", "/v1/rag/query"}, {"POST", "/v1/ocr"}, {"POST", "/v1beta/models/demo:generateContent"}, {"POST", "/bypass/gemini/v1beta/models/demo:generateContent"}, {"POST", "/vertex/v1/models/demo:generateContent"}, {"POST", "/models/:generateContent"}, {"POST", "/models/a/b:generateContent"}, {"POST", "/models/demo:unknown"}, {"POST", "/models/demo:generateContent/extra"}, {"POST", "/rag/query-more"}, {"POST", "/ocr-more"}} {
		w := httptest.NewRecorder()
		if rejectLegacyInference(w, httptest.NewRequest(tc.method, tc.path, nil)) || w.Body.Len() != 0 {
			t.Fatalf("误拦其他入口 %+v", tc)
		}
	}
}

// TestLegacyInferenceNoStore 验证拒绝发生在存储前，故障/缺失存储也不能回退写KV；正文只供确定性边界测试，无真实数据库。
func TestLegacyInferenceNoStore(t *testing.T) {
	for _, st := range []*catalogMemoryStore{nil, {rows: map[string]map[string]any{"ingest": {"name": "keep"}}, putErr: errors.New("private-sql")}} {
		w := httptest.NewRecorder()
		var store catalogRecordStore
		if st != nil {
			store = st
		}
		writeCatalogStore(store, w, httptest.NewRequest("POST", "/rag/ingest", nil), map[string]any{"private_fixture": "do-not-echo"})
		if w.Code != 501 || strings.Contains(w.Body.String(), "do-not-echo") || strings.Contains(w.Body.String(), "private-sql") {
			t.Fatal("拒绝响应异常", w)
		}
		if st != nil && (st.putCalls != 0 || len(st.deleteIDs) != 0 || st.rows["ingest"]["name"] != "keep") {
			t.Fatal("拒绝仍操作配置", st)
		}
	}
}

// TestLegacyInferenceValidation 验证原管理JSON校验优先，非法JSON和JSON-RPC仍400；无存储访问与清理。
func TestLegacyInferenceValidation(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `null`, `{"jsonrpc":"2.0","method":"x"}`} {
		w := httptest.NewRecorder()
		ServeMgmt(catalogPersistenceHost{}, w, httptest.NewRequest("POST", "/rag/query", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal("原请求校验改变", body, w)
		}
	}
}
