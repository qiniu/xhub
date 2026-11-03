package family

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBudgetConfigAdapterErrors 验证已鉴权适配层每类存储错误的 HTTP 状态与脱敏文案；内存夹具无外部清理。
// 服务返回原始错误，但兼容响应不得泄露数据库细节，也不能对失败写操作返回成功。
func TestBudgetConfigAdapterErrors(t *testing.T) {
	private := errors.New("private-database-detail")
	for _, tc := range []struct {
		method, path string
		st           *catalogMemoryStore
	}{
		{"GET", "/budgets", &catalogMemoryStore{listErr: private}},
		{"GET", "/budgets/one", &catalogMemoryStore{getErr: private}},
		{"POST", "/budgets", &catalogMemoryStore{putErr: private}},
		{"PATCH", "/budgets/one", &catalogMemoryStore{getErr: private}},
		{"PATCH", "/budgets/one", &catalogMemoryStore{putErr: private}},
		{"DELETE", "/budgets/one", &catalogMemoryStore{deleteErr: map[string]error{"one": private}}},
	} {
		w := callCatalogStore(tc.st, tc.method, tc.path, nil)
		if w.Code != 500 || strings.Contains(w.Body.String(), private.Error()) || !strings.Contains(w.Body.String(), "catalog store failure") {
			t.Fatalf("%s %s 错误映射不符: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if w := callCatalogStore(nil, "GET", "/budgets", nil); w.Code != 503 {
		t.Fatal("无存储必须503")
	}
}

// TestBudgetConfigAdapterLegacyShapes 验证旧详情空 ID、缺失404、列表别名和重复删除计数；纯内存，无真实账号。
func TestBudgetConfigAdapterLegacyShapes(t *testing.T) {
	st := &catalogMemoryStore{}
	for _, tc := range []struct {
		action, id, contains string
		status               int
	}{
		{"info", "", `"data":[]`, 200}, {"info", "missing", "not_found", 404}, {"list", "", `"data":[]`, 200}, {"delete", "missing", `"deleted":0`, 200},
	} {
		w := httptest.NewRecorder()
		writeBudgetConfigStore(st, w, httptest.NewRequest("GET", "/budgets", nil), "budgets", tc.action, tc.id, nil)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("旧形状改变 action=%s: %d %s", tc.action, w.Code, w.Body.String())
		}
	}
	callCatalogStore(st, "POST", "/budgets", map[string]any{"budget_id": "one"})
	w := callCatalogStore(st, "DELETE", "/budgets/one", map[string]any{"budget_id": "one"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Fatal("重复ID删除计数改变", w.Body.String())
	}
	obj := map[string]any{"id": "compat", "password": "removed"}
	Freeze("budgets", obj)
	encoded, err := json.Marshal(obj)
	if err != nil || obj["budget_id"] != "compat" || obj["max_budget"] != nil || strings.Contains(string(encoded), "removed") {
		t.Fatal("旧Freeze适配失败", err)
	}
}
