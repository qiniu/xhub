package keys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestKeyContractRequests 以内存契约验证器检查创建/补丁缺省、null、0和能力边界；不模拟实际网关，无外部数据需清理。
func TestKeyContractRequests(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		valid      bool
	}{
		{"/key/generate", `{}`, true},
		{"/key/generate", `{"max_budget":null}`, true},
		{"/key/generate", `{"max_budget":0}`, true},
		{"/key/generate", `{"max_budget":-1}`, false},
		{"/key/generate", `{"auto_rotate":true}`, false},
		{"/key/generate", `{"key_type":"management"}`, false},
		{"/key/generate", `{"allowed_routes":["management_routes"]}`, false},
		{"/key/update", `{"key":"k"}`, true},
		{"/key/update", `{"key":"k","max_budget":"","route_template_id":null}`, true},
		{"/key/update", `{"key":"k","rpm_limit":1.5}`, false},
		{"/key/update", `{"key":"k","rpm_limit":2147483648}`, false},
		{"/key/update", `{"max_budget":0}`, false},
		{"/key/update", `null`, false}, {"/key/update", `{} {}`, false},
		{"/v2/key/info", `{"key":"k"}`, true}, {"/v2/key/info", `{"keys":["k"]}`, false},
		{"/key/delete", `{"keys":["k"]}`, true}, {"/key/delete", `{"key":"k"}`, true},
		{"/key/delete", `{"keys":[]}`, false}, {"/key/block", `{}`, false},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if err := v.Request(req); (err == nil) != tc.valid {
				t.Fatalf("请求契约 valid=%v want=%v: %v", err == nil, tc.valid, err)
			}
		})
	}
	for _, path := range []string{"/key/list?size=101", "/key/list?page=0", "/key/list?sort_by=bad", "/key/info"} {
		if err := v.Request(httptest.NewRequest("GET", path, nil)); err == nil {
			t.Fatalf("非法查询通过: %s", path)
		}
	}
}

// TestKeyContractResponses 验证状态码一致、成功对象完整、禁用/恢复方向以及读取响应禁止凭据字段；纯内存测试，无清理。
func TestKeyContractResponses(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		status     int
		valid      bool
	}{
		{"/key/delete", `{"deleted":1}`, 200, true}, {"/key/delete", `{"deleted":true}`, 200, false},
		{"/key/block", `{"blocked":true}`, 200, true}, {"/key/block", `{"blocked":false}`, 200, false},
		{"/key/unblock", `{"blocked":false}`, 200, true}, {"/key/unblock", `{"blocked":true}`, 200, false},
		{"/key/update", `{"data":{}}`, 200, false}, {"/key/update", `{}`, 200, false},
		{"/key/update", `{"error":{"code":"403","type":"access_denied","message":"Denied","param":null}}`, 403, true},
		{"/key/update", `{"error":{"code":"429","type":"access_denied","message":"Denied","param":null}}`, 403, false},
		{"/key/update", `{}`, 307, false}, {"/key/update", `<html>ok</html>`, 200, false},
	} {
		if err := v.Response(httptest.NewRequest("POST", tc.path, nil), tc.status, http.Header{"Content-Type": []string{"application/json"}}, []byte(tc.body)); (err == nil) != tc.valid {
			t.Fatalf("%s %s status=%d valid=%v: %v", tc.path, tc.body, tc.status, tc.valid, err)
		}
	}
}

// TestKeyPatchPresence 验证生成请求类型保持字段存在性；往返缺省/null/零/空串，无服务器副作用，不需清理。
func TestKeyPatchPresence(t *testing.T) {
	for _, raw := range []string{`{"key":"k"}`, `{"key":"k","max_budget":null}`, `{"key":"k","max_budget":0}`, `{"key":"k","max_budget":"","route_template_id":null}`} {
		for _, dst := range []any{new(CreateKey), new(UpdateKey)} {
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
				t.Fatalf("%T 字段漂移: %s => %s", dst, raw, encoded)
			}
		}
	}
}

// TestKeyReadRejectsCredentialLeak 使用完整生成读取对象验证正常响应与明文/hash额外字段拒绝；纯内存，无账户或文件需清理。
func TestKeyReadRejectsCredentialLeak(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	value := KeyRecord{TokenId: "public-id", Token: "sk-prefix...", KeyPrefix: "sk-prefix", KeyType: KeyRecordKeyType("llm_api"), AllowedRoutes: []KeyRecordAllowedRoutes{"llm_api_routes"}, Models: []string{}, OwnerType: "personal", Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/key/update", nil)
	headers := http.Header{"Content-Type": []string{"application/json"}}
	if err := v.Response(req, 200, headers, raw); err != nil {
		t.Fatalf("完整Key响应不合契约: %v", err)
	}
	for _, field := range []string{"key", "key_hash"} {
		var leak map[string]any
		if err := json.Unmarshal(raw, &leak); err != nil {
			t.Fatal(err)
		}
		leak[field] = "must-not-leak"
		encoded, err := json.Marshal(leak)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Response(req, 200, headers, encoded); err == nil {
			t.Fatalf("读取接口允许泄漏字段: %s", field)
		}
	}
}
