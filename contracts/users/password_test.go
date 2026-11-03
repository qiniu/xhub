package users

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPasswordContract 验证两个密码入口、缺少字段以及确认对象禁止假成功；字节长度由真实后台回归锁定，无外部资源。
func TestPasswordContract(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		valid      bool
	}{{"/user/set_password", `{"user_id":"u","password":"Password-123"}`, true}, {"/user/set_password", `{"password":"Password-123"}`, false}, {"/user/u/password", `{"password":"Password-123"}`, true}, {"/user/u/password", `{}`, false}} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Request(r); (err == nil) != tc.valid {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{{`{"user_id":"u","password_updated":true}`, true}, {`{"user_id":"u","password_updated":false}`, false}, {`{"user_id":"u","password_updated":true,"password":"secret"}`, false}} {
		if err := v.Response(httptest.NewRequest("POST", "/user/set_password", nil), 200, http.Header{"Content-Type": []string{"application/json"}}, []byte(tc.body)); (err == nil) != tc.valid {
			t.Fatal(err)
		}
	}
}
