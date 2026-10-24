package suppliers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestContractValidation 用真实 OpenAPI 验证器拒绝错误形状及未声明操作；无需数据库，无持久化和清理。
// 参数 t 为测试上下文；正确对象、扩展字段与错误信封应通过，未知状态、缺字段、错类型和非 JSON 应失败。
func TestContractValidation(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
		valid                    bool
	}{
		{"record", "GET", "/credentials/by_name/sample", `{"id":"sample","credential_name":"sample","credential_info":{},"extension":null}`, 200, true},
		{"missing field", "GET", "/credentials/by_name/sample", `{"credential_name":"sample"}`, 200, false},
		{"wrong type", "DELETE", "/credentials/sample", `{"id":"sample","credential_name":"sample","deleted":"1"}`, 200, false},
		{"negative count", "DELETE", "/credentials/sample", `{"id":"sample","credential_name":"sample","deleted":-1}`, 200, false},
		{"empty list", "GET", "/credentials", `{"object":"list","data":[],"credentials":[]}`, 200, true},
		{"null list", "GET", "/credentials", `{"object":"list","data":null,"credentials":[]}`, 200, false},
		{"empty object", "GET", "/credentials", `{}`, 200, false},
		{"missing", "GET", "/credentials/by_name/sample", `{"error":{"message":"missing","code":"404","type":"not_found","param":null}}`, 404, true},
		{"mismatched code", "GET", "/credentials/by_name/sample", `{"error":{"message":"missing","code":"500","type":"not_found","param":null}}`, 404, false},
		{"unavailable", "GET", "/credentials", `{"error":{"message":"unavailable","code":"503","type":"unavailable","param":null}}`, 503, true},
		{"trailing response", "GET", "/credentials", `{"object":"list","data":[],"credentials":[]} {}`, 200, false},
		{"undeclared status", "GET", "/credentials", `{}`, 202, false},
		{"redirect 301", "GET", "/credentials", `{}`, 301, false},
		{"not modified", "GET", "/credentials", `{}`, 304, false},
		{"redirect 307", "GET", "/credentials", `{}`, 307, false},
		{"redirect 308", "GET", "/credentials", `{}`, 308, false},
		{"html", "GET", "/credentials", `<html>ok</html>`, 200, false},
		{"unknown method", "PUT", "/credentials/sample", `{}`, 200, false},
		{"unknown path", "GET", "/models", `{}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			err := v.Response(req, tc.status, http.Header{"Content-Type": []string{"application/json"}}, []byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("响应契约通过=%v，预期=%v: %v", err == nil, tc.valid, err)
			}
		})
	}
	req := httptest.NewRequest("GET", "/credentials", nil)
	if err := v.Response(req, 200, http.Header{"Content-Type": []string{"text/html"}}, []byte(`{"object":"list","data":[],"credentials":[]}`)); err == nil {
		t.Fatal("错误媒体类型被放过")
	}
	if err := v.Response(req, 307, http.Header{"Content-Type": []string{"text/html"}}, []byte(`{}`)); err == nil {
		t.Fatal("重定向不能跳过状态和媒体类型校验")
	}
}

// TestContractRequests 验证写入及两个 GET 操作允许扩展、null 字段和空对象，但拒绝非对象和损坏 JSON；鉴权由真实回归覆盖，此处无网络或数据清理。
func TestContractRequests(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{}`, true}, {`{"credential_values":null,"extension":false}`, true}, {``, true}, {" \n ", true}, {`null`, false}, {`[]`, false}, {`42`, false}, {`{} {}`, false}, {`{"x":`, false},
	} {
		for _, op := range []struct{ method, path string }{{"PATCH", "/credentials/sample"}, {"GET", "/credentials"}, {"GET", "/credentials/by_name/sample"}} {
			t.Run(op.method+op.path+tc.body, func(t *testing.T) {
				req := httptest.NewRequest(op.method, op.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				err := v.Request(req)
				if (err == nil) != tc.valid {
					t.Fatalf("请求契约通过=%v，预期=%v: %v", err == nil, tc.valid, err)
				}
			})
		}
	}
	if err := v.Request(httptest.NewRequest("POST", "/outside", nil)); err == nil {
		t.Fatal("未知路径不能跳过验证")
	}
}

// TestGeneratedErrorsRoundTrip 验证各 HTTP 错误生成类型完整保留四个必填字段及 null；源响应和回编码均须符合真实契约。
// 只在内存编解码；另验证缺字段及状态码不一致必须失败，无网络、数据库或待清理数据。
func TestGeneratedErrorsRoundTrip(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status int
		target any
	}{
		{400, &Error400{}}, {401, &Error401{}}, {403, &Error403{}},
		{404, &Error404{}}, {500, &Error500{}}, {503, &Error503{}},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			req := httptest.NewRequest("GET", "/credentials/by_name/sample", nil)
			header := http.Header{"Content-Type": []string{"application/json"}}
			want := map[string]any{"error": map[string]any{"code": strconv.Itoa(tc.status), "message": "fixture failure", "type": "fixture_error", "param": nil}}
			raw, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, tc.target); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("错误类型丢字段: want=%s got=%s", raw, encoded)
			}
			if err := v.Response(req, tc.status, header, encoded); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"code", "message", "type", "param"} {
				var invalid map[string]map[string]any
				if err := json.Unmarshal(raw, &invalid); err != nil {
					t.Fatal(err)
				}
				delete(invalid["error"], field)
				body, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				if v.Response(req, tc.status, header, body) == nil {
					t.Fatalf("缺少 %s 被接受", field)
				}
			}
			want["error"].(map[string]any)["code"] = "499"
			invalid, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if v.Response(req, tc.status, header, invalid) == nil {
				t.Fatal("错误码与状态不一致被接受")
			}
		})
	}
}

// TestContractInvalidDocument 验证载入错误、悬空引用和无效规范必须阻断测试；输入仅在内存中，无文件副作用。
func TestContractInvalidDocument(t *testing.T) {
	for _, raw := range []string{`{`, `{}`, `{"openapi":"3.0.3","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"$ref":"#/components/responses/missing"}}}}}}`} {
		if _, err := NewValidator([]byte(raw)); err == nil {
			t.Fatalf("无效契约被接受: %s", raw)
		}
	}
}

// TestGeneratedCredentialRoundTrip 验证生成类型保留缺省/null/空值及扩展字段；只在内存编解码，非法 JSON 对象必须失败，字段约束由独立 OpenAPI 测试验证，无清理。
func TestGeneratedCredentialRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		target    any
	}{
		{"input omitted", `{}`, &CredentialInput{}},
		{"input null", `{"credential_values":null,"credential_info":{},"created_at":0,"credential_name":"sample","extra":false}`, &CredentialInput{}},
		{"record", `{"id":"sample","credential_name":"sample","credential_info":null,"credential_values":{},"extra":null}`, &Credential{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(tc.raw), tc.target); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(tc.target)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal([]byte(tc.raw), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("生成类型改变缺省或null语义: want=%s got=%s", tc.raw, raw)
			}
		})
	}
	// Go 使用原始 JSON map 保留未知字段及 null；字段类型由 OpenAPI 验证，而不是依赖 Go 解码器。
	for _, raw := range []string{`{`, `[]`} {
		if err := json.Unmarshal([]byte(raw), &Credential{}); err == nil {
			t.Fatalf("非法对象应拒绝: %s", raw)
		}
	}
}

// brokenBody 为读取失败边界提供确定性 I/O 错误，Read 参数是目标缓冲区，返回零字节及错误；仅测试使用。
type brokenBody struct{}

// Read 返回固定读取错误供异常分支验证；不写缓冲区，无资源副作用。
func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("fixture body read failure") }

// TestContractBodyReadFailure 验证请求流读取失败不能被当作空对象成功；无网络，NopCloser 无资源待清理。
func TestContractBodyReadFailure(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/credentials", nil)
	req.Body = io.NopCloser(brokenBody{})
	if err := v.Request(req); err == nil || !strings.Contains(err.Error(), "fixture body read failure") {
		t.Fatalf("读取失败被吞掉: %v", err)
	}
}
