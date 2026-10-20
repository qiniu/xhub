package dialogue

import "testing"

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
