package family

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAccessGroupConfigAdapterErrors 验证已鉴权适配层每类存储错误的 HTTP 状态与脱敏文案；内存夹具无外部清理。
// 服务返回原始错误，但兼容响应不得泄露数据库细节，也不能对失败写操作返回成功。
func TestAccessGroupConfigAdapterErrors(t *testing.T) {
	private := errors.New("private-database-detail")
	for _, tc := range []struct {
		method, path string
		st           *catalogMemoryStore
	}{
		{"GET", "/v1/access_group", &catalogMemoryStore{listErr: private}},
		{"GET", "/v1/access_group/one", &catalogMemoryStore{getErr: private}},
		{"POST", "/v1/access_group", &catalogMemoryStore{putErr: private}},
		{"PUT", "/v1/access_group/one", &catalogMemoryStore{getErr: private}},
		{"PUT", "/v1/access_group/one", &catalogMemoryStore{putErr: private}},
		{"DELETE", "/v1/access_group/one", &catalogMemoryStore{deleteErr: map[string]error{"one": private}}},
	} {
		w := callCatalogStore(tc.st, tc.method, tc.path, nil)
		if w.Code != 500 || strings.Contains(w.Body.String(), private.Error()) || !strings.Contains(w.Body.String(), "catalog store failure") {
			t.Fatalf("%s %s 错误映射不符: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if w := callCatalogStore(nil, "GET", "/v1/access_group", nil); w.Code != 503 {
		t.Fatal("无存储必须503")
	}
}

// TestAccessGroupConfigAdapterLegacyShapes 验证旧详情空 ID、缺失404、列表别名和重复删除计数；纯内存，无真实账号。
func TestAccessGroupConfigAdapterLegacyShapes(t *testing.T) {
	st := &catalogMemoryStore{}
	for _, tc := range []struct {
		action, id, contains string
		status               int
	}{
		{"info", "", `"data":[]`, 200}, {"info", "missing", "not_found", 404}, {"list", "", `[]`, 200}, {"delete", "missing", `"deleted":0`, 200},
	} {
		w := httptest.NewRecorder()
		writeAccessGroupConfigStore(st, w, httptest.NewRequest("GET", "/v1/access_group", nil), "access_group", tc.action, tc.id, nil)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("旧形状改变 action=%s: %d %s", tc.action, w.Code, w.Body.String())
		}
	}
	callCatalogStore(st, "POST", "/v1/access_group", map[string]any{"access_group_id": "one"})
	w := callCatalogStore(st, "DELETE", "/v1/access_group/one", map[string]any{"access_group_id": "one"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Fatal("重复ID删除计数改变", w.Body.String())
	}
	obj := map[string]any{"id": "compat", "password": "removed"}
	Freeze("access_group", obj)
	encoded, err := json.Marshal(obj)
	if err != nil || obj["access_group_id"] != "compat" || obj["access_group_name"] != "compat" || strings.Contains(string(encoded), "removed") {
		t.Fatal("旧Freeze适配失败", err)
	}
}
