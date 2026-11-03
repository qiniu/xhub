package keys

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestKeyLifecycleContract 锁定轮换/重置正常与边界输入以及部分成功响应；纯契约单测，不伪造真实网关可用性，无资源清理。
func TestKeyLifecycleContract(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		valid      bool
	}{
		{"/key/regenerate", `{"key":"k"}`, true}, {"/key/regenerate", `{}`, false},
		{"/key/k/regenerate", `{}`, true}, {"/key/k/regenerate", `{"grace_period_seconds":5}`, false},
		{"/key/k/reset_spend", `{"reset_to":0}`, true}, {"/key/k/reset_spend", `{"reset_to":-1}`, false},
		{"/key/bulk_update", `{"keys":["a","missing"],"max_budget":null}`, true},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Request(r); (err == nil) != tc.valid {
			t.Fatalf("%s %s: %v", tc.path, tc.body, err)
		}
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{{`{"updated":0,"keys":[]}`, true}, {`{"updated":true,"keys":[]}`, false}, {`{"success":true}`, false}} {
		if err := v.Response(httptest.NewRequest("POST", "/key/bulk_update", nil), 200, http.Header{"Content-Type": []string{"application/json"}}, []byte(tc.body)); (err == nil) != tc.valid {
			t.Fatal(err)
		}
	}
}
