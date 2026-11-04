package dialogue

import (
	"strings"
	"testing"
)

// TestWorkBuddyChatTrackingBridge 使用实际客户端的消息字段复现 Chat 转 Responses 失败；
// 验证客户端跟踪字段不会泄漏或改变正文、同协议原始请求保留，未知及错误字段仍拒绝；无需外部数据或清理。
func TestWorkBuddyChatTrackingBridge(t *testing.T) {
	body := map[string]any{"model": "example", "messages": []any{
		map[string]any{"role": "user", "content": "hello", "startsNewUserRequest": true, "agent": "cli", "conversationRequestId": "req-1"},
		map[string]any{"role": "assistant", "content": "hello back", "conversationRequestId": "req-1", "messageId": "msg-1", "stepSeq": 1, "model": "example", "requestModelId": "example", "requestModelName": "example", "traceId": "trace-1", "rawUsage": map[string]any{"prompt_tokens": 1}, "agent": "cli", "usage": map[string]any{"completion_tokens": 1}},
	}}
	d, err := ParseDialogue("openai-chat", body)
	if err != nil {
		t.Fatalf("真实 WorkBuddy 消息应可解析: %v", err)
	}
	out, err := EncodeDialogue(d, "openai-responses", "example")
	if err != nil {
		t.Fatalf("WorkBuddy 消息应能转换为 Responses: %v", err)
	}
	if strings.Contains(string(mustEncode(out)), "conversationRequestId") || strings.Contains(string(mustEncode(out)), `"agent"`) {
		t.Fatal("客户端跟踪字段不应作为上游模型能力外发")
	}
	if !strings.Contains(string(mustEncode(out)), "hello back") {
		t.Fatal("转换丢失对话正文")
	}
	native, err := EncodeDialogue(d, "openai-chat", "example")
	if err != nil || !strings.Contains(string(mustEncode(native)), "conversationRequestId") {
		t.Fatal("同协议原始请求应保留")
	}
}

// TestWorkBuddyChatTrackingRejectsUnknown 验证带未知字段或伪造跟踪信息不能静默丢弃；无需外部数据或清理。
func TestWorkBuddyChatTrackingRejectsUnknown(t *testing.T) {
	for _, extra := range []map[string]any{{"agent": "cli", "business": "must-keep"}, {"agent": 7}, {"agent": "other", "startsNewUserRequest": true}, {"startsNewUserRequest": true}, {"agent": "cli", "stepSeq": -1}, {"agent": "cli", "usage": "invalid"}, {"agent": "cli", "conversationRequestId": 123}} {
		m := map[string]any{"role": "user", "content": "hello"}
		for k, v := range extra {
			m[k] = v
		}
		if _, err := ParseDialogue("openai-chat", map[string]any{"messages": []any{m}}); err == nil {
			t.Fatalf("不应静默接收未知跟踪字段: %v", extra)
		}
	}
}
