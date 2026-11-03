package family

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMetadataAdapterErrors 验证CRUD委派的失败映射和无库503；纯内存存储不触碰真实账号。
func TestMetadataAdapterErrors(t *testing.T) {
	private := errors.New("private-database-detail")
	for _, kind := range []string{"tag", "tags", "customer", "end_user"} {
		field := "user_id"
		if kind == "tag" || kind == "tags" {
			field = "name"
		}
		for _, tc := range []struct {
			action string
			st     *catalogMemoryStore
		}{
			{"list", &catalogMemoryStore{listErr: private}}, {"info", &catalogMemoryStore{getErr: private}},
			{"new", &catalogMemoryStore{putErr: private}}, {"update", &catalogMemoryStore{getErr: private}},
			{"update", &catalogMemoryStore{putErr: private}}, {"delete", &catalogMemoryStore{deleteErr: map[string]error{"one": private}}},
		} {
			w := callCatalogStore(tc.st, "POST", "/"+kind+"/"+tc.action, map[string]any{field: "one"})
			if w.Code != 500 || strings.Contains(w.Body.String(), private.Error()) {
				t.Fatalf("%s/%s错误映射变化: %d %s", kind, tc.action, w.Code, w.Body.String())
			}
		}
		if w := callCatalogStore(nil, "GET", "/"+kind+"/list", nil); w.Code != 503 {
			t.Fatal("缺库未拒绝")
		}
	}
}

// TestMetadataAdapterShapes 验证空详情、缺失、列表、删除、Freeze兼容与执行型路径隔离；内存对象自动回收。
func TestMetadataAdapterShapes(t *testing.T) {
	for _, kind := range []string{"tag", "customer"} {
		for _, tc := range []struct {
			action, id, body string
			status           int
		}{
			{"info", "", `"data":[]`, 200}, {"info", "missing", "not_found", 404},
			{"delete", "missing", `"deleted":0`, 200}, {"list", "", "[]", 200},
		} {
			w := httptest.NewRecorder()
			writeMetadataConfigStore(&catalogMemoryStore{}, w, httptest.NewRequest("GET", "/"+kind+"/list", nil), kind, tc.action, tc.id, nil)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) {
				t.Fatal("旧形状改变", w.Code, w.Body.String())
			}
		}
	}
	for _, path := range []string{"/customer/block", "/end_user/unblock", "/customer/daily/activity", "/tag/summary"} {
		if metadataCRUDPath(path) {
			t.Fatal("执行型接口被CRUD接管", path)
		}
	}
	for _, path := range []string{"/tag/new", "/customer/update/", "/end_user/delete", "/tag/info", "/customer/list"} {
		if !metadataCRUDPath(path) {
			t.Fatal("遗漏CRUD", path)
		}
	}
	for _, kind := range []string{"tag", "tags", "customer", "end_user"} {
		obj := map[string]any{"id": "legacy", "password": "removed"}
		Freeze(kind, obj)
		if obj["password"] != nil || obj["spend"] != 0 {
			t.Fatal("Freeze兼容失败", obj)
		}
	}
}
