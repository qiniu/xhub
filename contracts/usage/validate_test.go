package usage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestUsageContractShape 验证真实日志页分页与错误信封，拒绝空对象假成功/错误状态不一致；纯内存，不模拟业务或需要清理。
func TestUsageContractShape(t *testing.T) {
	v, err := NewValidator(Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body   string
		status int
		valid  bool
	}{
		{`{"data":[],"total":0,"page":1,"page_size":50,"total_pages":0}`, 200, true},
		{`{}`, 200, false}, {`{"data":[],"total":-1,"page":1,"page_size":50,"total_pages":0}`, 200, false},
		{`{"error":{"code":"403","message":"Denied","type":"access_denied","param":null}}`, 403, true},
		{`{"error":{"code":"501","message":"Denied","type":"access_denied","param":null}}`, 403, false},
		{`<html>error</html>`, 200, false},
	} {
		if err := v.Response(httptest.NewRequest("GET", "/spend/logs/ui", nil), tc.status, http.Header{"Content-Type": []string{"application/json"}}, []byte(tc.body)); (err == nil) != tc.valid {
			t.Fatalf("status=%d body=%s: %v", tc.status, tc.body, err)
		}
	}
	for _, path := range []string{"/spend/logs/ui?page=bad&page_size=900", "/spend/logs/session/ui", "/user/daily/activity?timezone=-480&start_date=bad"} {
		if err := v.Request(httptest.NewRequest("GET", path, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Request(httptest.NewRequest("POST", "/spend/logs/ui", strings.NewReader(`{}`))); err == nil {
		t.Fatal("未声明操作应拒绝")
	}
}

// TestUsageGeneratedRoundTrip 验证生成日志类型保留完整历史字段、未知扩展和任意正文；非法类型/JSON拒绝，纯内存无清理。
func TestUsageGeneratedRoundTrip(t *testing.T) {
	raw := []byte(`{"request_id":"r","api_key":"k","team_id":"t","model":"m","model_id":"m","call_type":"chat","reasoning_effort":"high","startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:00:01Z","inbound_endpoint":"/v1/chat/completions","outbound_endpoint":"/responses","user":"u","cache_hit":"false","cache_key":"","session_id":"s","session_source":"explicit","status":"success","owner_type":"personal","project_id":"","organization_id":"o","custom_llm_provider":"openai","credential_name":"provider","api_base":"http://example.invalid","label":"label","spend":0.25,"total_tokens":3,"prompt_tokens":1,"completion_tokens":2,"request_duration_ms":1000,"streamed":true,"ttft_ms":5,"tps":2,"messages":[{"content":"Original user input"}],"response":"non-json upstream body","metadata":{"http_status":200,"extra":null},"completionStartTime":"2026-01-01T00:00:00Z","cache_read_input_tokens":0,"error":"","proxy_server_request":{"body":{"unknown":true}},"session_total_count":1,"session_llm_count":1,"session_spend":0.25,"session_total_tokens":3,"future_extension":{"nested":null,"large":9007199254740993}}`)
	var value LogRecord
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"large":9007199254740993`) {
		t.Fatal("未知扩展整数精度丢失")
	}
	var before, after any
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("历史数据字段漂移: %s", encoded)
	}
	if _, ok := value.Get("missing"); ok {
		t.Fatal("未提供字段被猜测")
	}
	var empty LogRecord
	empty.Set("added", json.RawMessage(`true`))
	if v, ok := empty.Get("added"); !ok || string(v) != "true" {
		t.Fatal("未知扩展未保留")
	}
	// 每个固定JSON标量在类型错误时均必须显式失败；任意正文/metadata不受此限制。
	for key, original := range before.(map[string]any) {
		if key == "response" {
			continue
		} // 任意上游正文允许对象、字符串或其他JSON值。
		switch original.(type) {
		case string, float64, bool:
			invalid, _ := json.Marshal(map[string]any{key: map[string]any{"bad": "shape"}})
			if err := json.Unmarshal(invalid, new(LogRecord)); err == nil {
				t.Fatalf("字段%s接受错误类型", key)
			}
		}
	}
	if err := json.Unmarshal([]byte(`{`), new(LogRecord)); err == nil {
		t.Fatal("畸形JSON被接受")
	}
	value.Metadata = json.RawMessage(`{`)
	if _, err := json.Marshal(value); err == nil {
		t.Fatal("无效原始JSON被悄悄发布")
	}
}
