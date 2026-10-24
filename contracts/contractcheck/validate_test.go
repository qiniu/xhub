package contractcheck_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/contracts/contractcheck"
	"github.com/sunqirui1987/xhub/contracts/suppliers"
)

// TestSharedValidatorFailures 验证公共加载器拒绝非法JSON、引用和操作，并拦截空成功/HTML/重定向/尾随JSON；只读嵌入文档，无网络或清理。
func TestSharedValidatorFailures(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{`), []byte(`{"openapi":"3.0.3","info":{"title":"bad","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"$ref":"#/components/responses/missing"}}}}}}`), []byte(`{"openapi":"3.0.3","paths":{}}`)} {
		if _, err := contractcheck.NewValidator(raw); err == nil {
			t.Fatal("非法契约被接受")
		}
	}
	v, err := contractcheck.NewValidator(suppliers.Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body, media string
		status            int
		valid             bool
	}{
		{"/credentials", `{"object":"list","data":[],"credentials":[]}`, "application/json", 200, true},
		{"/credentials", `{}`, "application/json", 200, false},
		{"/credentials", `{} {}`, "application/json", 200, false},
		{"/credentials", `{"object":"list","data":[],"credentials":[]}`, "text/html", 200, false},
		{"/credentials", `{}`, "application/json", 307, false},
		{"/absent", `{}`, "application/json", 200, false},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if err := v.Response(req, tc.status, http.Header{"Content-Type": []string{tc.media}}, []byte(tc.body)); (err == nil) != tc.valid {
			t.Fatalf("响应检查 status=%d valid=%v want=%v err=%v", tc.status, err == nil, tc.valid, err)
		}
	}
}

// brokenBody 注入真实io.Reader错误，验证请求读取失败不能转成空成功；不产生文件或外部请求。
type brokenBody struct{}

// Read 返回固定失败，p仅满足Reader接口；用于验证器异常路径，不修改外部状态。
func (brokenBody) Read(p []byte) (int, error) { return 0, errors.New("fixture read error") }

// Close 满足ReadCloser接口，无资源需要释放。
func (brokenBody) Close() error { return nil }

// TestSharedRequestBody 验证支持对象、空正文及拒绝畸形/未知路由/读取失败，同时确保合法正文可被后续发送；仅内存无需清理。
func TestSharedRequestBody(t *testing.T) {
	v, err := contractcheck.NewValidator(suppliers.Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{{`{}`, true}, {``, true}, {" \n", true}, {`null`, false}, {`[]`, false}, {`{} {}`, false}, {`{`, false}} {
		req := httptest.NewRequest("POST", "/credentials", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if err := v.Request(req); (err == nil) != tc.valid {
			t.Fatalf("输入%s valid=%v want=%v err=%v", tc.body, err == nil, tc.valid, err)
		}
		if tc.body == `{}` {
			raw, err := io.ReadAll(req.Body)
			if err != nil || string(raw) != tc.body {
				t.Fatalf("验证后正文丢失: %s %v", raw, err)
			}
		}
	}
	req := httptest.NewRequest("POST", "/credentials", nil)
	req.Body = brokenBody{}
	if err := v.Request(req); err == nil {
		t.Fatal("读取错误被忽略")
	}
	if err := v.Request(httptest.NewRequest("GET", "/absent", nil)); err == nil {
		t.Fatal("未知路由被接受")
	}
}
