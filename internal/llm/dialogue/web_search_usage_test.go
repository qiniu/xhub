package dialogue

import "testing"

// TestResponsesSearchUsage 验证完整终态按动作计数而非查询数收费，缺失/重复/未完成项不能被当作零搜索。
// 前置纯内存响应夹具，覆盖正常、边界和失败；无外部依赖，GC清理。
func TestResponsesSearchUsage(t *testing.T) {
	item := func(id, action string) map[string]any {
		return map[string]any{"type": "web_search_call", "id": id, "status": "completed", "action": map[string]any{"type": action, "queries": []any{"a", "b"}}}
	}
	for _, tc := range []struct {
		name            string
		output          any
		valid           bool
		calls, searches int
	}{
		{"zero", []any{}, true, 0, 0},
		{"actions", []any{item("s", "search"), item("o", "open_page"), item("f", "find_in_page")}, true, 3, 1},
		{"duplicate", []any{item("s", "search"), item("s", "search")}, false, 0, 0},
		{"unknown", []any{item("s", "image_search")}, false, 0, 0},
		{"missing", nil, false, 0, 0},
		{"malformed", []any{"bad"}, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := map[string]any{"status": "completed", "output": tc.output}
			u := ResponsesSearchUsage(doc, map[string]any{"input_tokens": 10, "xhub_web_search_calls": 99})
			_, valid := u["xhub_web_search_calls"]
			if valid != tc.valid || valid && (u["xhub_web_search_calls"] != tc.calls || u["xhub_web_search_searches"] != tc.searches) {
				t.Fatalf("搜索计量错误: %v", u)
			}
		})
	}
	bad := item("s", "search")
	bad["status"] = "in_progress"
	if ValidWebSearchHistory(bad) {
		t.Fatal("未完成历史被接受")
	}
	bad = item("s", "search")
	bad["opaque_payload"] = "unknown"
	if ValidWebSearchHistory(bad) {
		t.Fatal("未知历史字段被接受")
	}
}

// TestWebSearchHistoryRoundTrip 验证已完成搜索在原生续轮保留、跨协议明确拒绝；非法历史拒绝，不产生外部数据。
func TestWebSearchHistoryRoundTrip(t *testing.T) {
	body := map[string]any{"input": []any{map[string]any{"type": "web_search_call", "id": "ws_history", "status": "completed", "action": map[string]any{"type": "search", "query": "Go"}}, map[string]any{"role": "user", "content": "Continue."}}}
	d, err := ParseDialogue("openai-responses", body)
	if err != nil {
		t.Fatal(err)
	}
	native, err := EncodeDialogue(d, "openai-responses", "fixture")
	if err != nil || len(native["input"].([]any)) != 2 {
		t.Fatalf("原生历史丢失: %v %v", native, err)
	}
	if _, err := EncodeDialogue(d, "openai-chat", "fixture"); err == nil {
		t.Fatal("搜索历史被静默跨协议丢弃")
	}
	body["input"].([]any)[0].(map[string]any)["status"] = "in_progress"
	if _, err := ParseDialogue("openai-responses", body); err == nil {
		t.Fatal("未完成搜索历史被接受")
	}
}
