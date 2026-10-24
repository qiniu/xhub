package users

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestUserContractRequests 验证支持输入的缺省/null/0、单人字符串与批量严格数字边界；只做内存契约检查，不模拟业务或产生待清理数据。
func TestUserContractRequests(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		valid      bool
	}{
		{"/user/new", `{"user_email":"a@example.invalid","password":"Example-password"}`, true},
		{"/user/new", `{"user_email":"a@example.invalid"}`, false},
		{"/user/update", `{"user_id":"u","max_budget":"500"}`, true},
		{"/user/update", `{"user_id":"u","max_budget":""}`, true},
		{"/user/update", `{"user_id":"u","max_budget":null}`, true},
		{"/user/update", `{"user_id":"u","max_budget":0}`, true},
		{"/user/update", `{"user_id":"u","max_budget":-1}`, false},
		{"/user/update", `{"budget_reset_day":null}`, false},
		{"/user/update", `{"budget_reset_day":32}`, false},
		{"/user/update", `{"budget_reset_day":1.5}`, false},
		{"/user/update", `{} {}`, false}, {"/user/update", `null`, false}, {"/user/update", ``, false},
		{"/user/bulk_update", `{"users":[{"user_id":"u","max_budget":null}]}`, true},
		{"/user/bulk_update", `{"users":[{"user_id":"u","budget_reset_day":0}]}`, true},
		{"/user/bulk_update", `{"all_users":true,"user_updates":{"max_budget":0}}`, true},
		{"/user/bulk_update", `{"users":[{"user_id":"u","max_budget":"0"}]}`, false},
		{"/user/bulk_update", `{"users":[{"user_id":"u"}]}`, false},
		{"/user/bulk_update", `{"users":[]}`, false},
		{"/user/bulk_update", `{"all_users":true,"users":[{"user_id":"u","max_budget":0}],"user_updates":{"max_budget":0}}`, false},
		{"/user/bulk_update", `{"all_users":true,"user_updates":{"max_budget":null,"user_role":"admin"}}`, false},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if err := v.Request(req); (err == nil) != tc.valid {
				t.Fatalf("输入契约 valid=%v want=%v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

// TestUserContractResponses 验证错误码与HTTP一致、成功必须完整、重定向和HTML不能假通过；纯内存无外部依赖或清理。
func TestUserContractResponses(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		status     int
		valid      bool
	}{
		{"/user/delete", `{"deleted":true}`, 200, true},
		{"/user/delete", `{"deleted":false}`, 200, false},
		{"/user/update", `{"user_id":"u","data":{}}`, 200, false},
		{"/user/update", `{"user_info":{}}`, 200, false},
		{"/user/update", `{"error":{"code":"403","type":"access_denied","message":"Denied","param":null}}`, 403, true},
		{"/user/update", `{"error":{"code":"429","type":"access_denied","message":"Denied","param":null}}`, 403, false},
		{"/user/update", `{"error":{"code":"500"}}`, 500, false},
		{"/user/update", `{}`, 302, false},
		{"/user/update", `{}`, 307, false},
		{"/user/update", `<html>ok</html>`, 200, false},
	} {
		req := httptest.NewRequest("POST", tc.path, nil)
		if err := v.Response(req, tc.status, http.Header{"Content-Type": []string{"application/json"}}, []byte(tc.body)); (err == nil) != tc.valid {
			t.Fatalf("%s status=%d valid=%v want=%v: %v", tc.body, tc.status, err == nil, tc.valid, err)
		}
	}
}

// TestGeneratedUserPatchPresence 验证生成Go请求类型往返保留缺字段、显式null、零和空串；防止补丁编解码清空额度，纯内存无需清理。
func TestGeneratedUserPatchPresence(t *testing.T) {
	for _, raw := range []string{`{"user_id":"u"}`, `{"user_id":"u","max_budget":null}`, `{"user_id":"u","max_budget":0}`, `{"user_id":"u","max_budget":""}`, `{"users":[{"user_id":"u","budget_reset_day":0}]}`} {
		for _, dst := range []any{new(UpdateUser), new(CreateUser), new(BulkBudgetInput)} {
			if err := json.Unmarshal([]byte(raw), dst); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(dst)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err := json.Unmarshal([]byte(raw), &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%T 字段存在性漂移 before=%s after=%s", dst, raw, encoded)
			}
		}
	}
}
