package dialogue

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestNativeResponsesEnvelope 验证独立信封接受原生扩展且不修改输入，非法核心形状和异步请求失败；纯内存无需清理。
func TestNativeResponsesEnvelope(t *testing.T) {
	body := map[string]any{"input": []any{map[string]any{"type": "future_item", "payload": map[string]any{"x": "keep"}}}, "stream": true, "future_options": map[string]any{"a": true}}
	raw, _ := json.Marshal(body)
	d, err := ParseNativeResponses(body)
	if err != nil || !d.Stream || !reflect.DeepEqual(d.Native, body) {
		t.Fatalf("原生字段未保留: %+v %v", d, err)
	}
	d.Native["input"].([]any)[0].(map[string]any)["payload"] = "changed"
	after, _ := json.Marshal(body)
	if string(raw) != string(after) {
		t.Fatal("修改了调用方正文")
	}
	for _, bad := range []map[string]any{nil, {}, {"input": 123}, {"input": []any{}}, {"input": []any{"bad"}}, {"input": "Hello", "stream": "yes"}, {"input": "Hello", "background": true}, {"input": "Hello", "tools": "bad"}} {
		if _, err := ParseNativeResponses(bad); err == nil {
			t.Fatalf("非法信封未拒绝: %v", bad)
		}
	}
}

// TestNativeResponsesTerminal 验证未知输出内容保留，终态/用量形状错误失败，incomplete继续保留；纯内存无需清理。
func TestNativeResponsesTerminal(t *testing.T) {
	raw := []byte(`{"id":"resp_test","status":"completed","output":[{"type":"message","content":[{"type":"future_output","data":{"x":1}}]}],"usage":{"input_tokens":3,"output_tokens":2}}`)
	r, err := ParseNativeResponsesResult(raw)
	if err != nil || r.ID != "resp_test" || r.Stop != "stop" || r.Native["output"] == nil {
		t.Fatalf("原生终态失败: %+v %v", r, err)
	}
	if _, err := ParseDialogueResult("openai-responses", raw); err == nil {
		t.Fatal("测试必须覆盖旧公共投影不支持的响应")
	}
	for _, bad := range []string{`null`, `{}`, `{"id":"x","status":"queued","output":[]}`, `{"id":"x","status":"completed","output":[1]}`, `{"id":"x","status":"completed","output":[],"usage":1}`, `{"id":"x","status":"completed","output":[],"error":{}}`} {
		if _, err := ParseNativeResponsesResult([]byte(bad)); err == nil {
			t.Fatalf("非法终态通过: %s", bad)
		}
	}
	r, err = ParseNativeResponsesResult([]byte(`{"id":"x","status":"incomplete","output":[]}`))
	if err != nil || r.Stop != "length" {
		t.Fatal("截断终态丢失")
	}
}

// TestNativeResponsesOptionalSearch 验证原生可选搜索降级只移除声明，未知历史不被重新投影拒绝；必需搜索不能删除。
// 纯内存结构，无外部资源；调用方原始工具数组不变。
func TestNativeResponsesOptionalSearch(t *testing.T) {
	body := map[string]any{"input": []any{map[string]any{"type": "future_item", "payload": "keep"}}, "tools": []any{map[string]any{"type": "web_search"}}}
	d, err := ParseNativeResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	d.NativeResponses = true
	out, dropped, err := PrepareWebSearch(d, false, true)
	if err != nil || !dropped || !out.NativeResponses || len(out.Native["tools"].([]any)) != 0 || len(d.Native["tools"].([]any)) != 1 {
		t.Fatalf("可选搜索处理错误: %v", err)
	}
	d.Native["tool_choice"] = "required"
	if _, _, err := PrepareWebSearch(d, false, true); err == nil {
		t.Fatal("必需搜索被删除")
	}
}
