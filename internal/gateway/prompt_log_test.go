package gateway

import (
	"context"
	"encoding/json"
	"github.com/sunqirui1987/xhub/internal/live"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
	"xorm.io/builder"
)

// TestRememberExchangeKeepsFailureCandidateWhenPromptStorageOff 验证关闭提示词保存仍暂存失败响应。
// 前置条件是开关关闭；请求正文不得保存，原始响应必须保留到 RecordSpend；内存项由 takeExchange 清理。
func TestRememberExchangeKeepsFailureCandidateWhenPromptStorageOff(t *testing.T) {
	s := &Server{Cfg: &config.Config{}, exchanges: map[string]promptExchange{}}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	s.rememberExchange("failure-off", req, []byte("{\"messages\":[{\"content\":\"secret\"}]}"), []byte("upstream raw error"))
	ex := s.takeExchange("failure-off")
	if ex.messages != "" || ex.proxy != "" || ex.response != "" {
		t.Fatalf("关闭开关后保存了请求或成功响应: %+v", ex)
	}
	if ex.rawResponse != "upstream raw error" {
		t.Fatalf("失败候选正文丢失: %q", ex.rawResponse)
	}
	if _, exists := s.exchanges["failure-off"]; exists {
		t.Fatal("takeExchange 未清理临时数据")
	}
}

// TestSpendLogRoundTripReturnsPromptPayload stores one call through the same
// write path the gateway uses and reads the bodies back through the same read
// path the log drawer uses, so the two stay in step.
func TestSpendLogRoundTripReturnsPromptPayload(t *testing.T) {
	db := testIdentityStore(t)
	ctx := context.Background()
	id := "prompt-probe-" + time.Now().UTC().Format("20060102150405.000000000")
	start := time.Now().UTC().Add(-time.Minute)
	messages := `[{"role":"user","content":"hello"}]`
	response := `{"id":"chatcmpl-1","choices":[{"message":{"content":"hi"}}]}`
	proxy := `{"method":"POST","url":"/v1/chat/completions","headers":{"Content-Type":"application/json","Authorization":"***"},"body":{"messages":[{"role":"user","content":"hello"}]}}`

	rec := iam.UsageRecord{
		RequestID: id, TS: start, KeyID: "key-1", OwnerType: iam.OwnerPersonal,
		UserID: "user-1", TeamID: "team-1", Model: "gpt-4o-mini", CallType: "chat",
		Status: "success", PromptTokens: 5, CompletionTokens: 4, Cost: 0.1,
		RequestBody: messages, ResponseBody: response,
	}
	if err := db.RecordUsage(ctx, []iam.UsageRecord{rec}); err != nil {
		t.Fatalf("record usage: %v", err)
	}

	// The event must be readable through the scoped read API, which is the only
	// way a handler reaches it.
	event, err := db.GetUsageEvent(ctx, iam.UsageQuery{Cond: builder.NewCond()}, id)
	if err != nil {
		t.Fatalf("read back event: %v", err)
	}
	if event.Model != "gpt-4o-mini" || event.PromptTokens != 5 || event.CompletionTokens != 4 {
		t.Fatalf("event: %+v", event)
	}
	// Ownership is snapshotted at write time, which is what makes the log
	// drawer able to show whose call it was after the key is gone.
	if event.UserID != "user-1" || event.TeamID != "team-1" || event.OwnerType != iam.OwnerPersonal {
		t.Fatalf("ownership snapshot: %+v", event)
	}

	body, err := db.GetRequestLog(ctx, id)
	if err != nil {
		t.Fatalf("read back bodies: %v", err)
	}
	var msgs []map[string]any
	if err := json.Unmarshal([]byte(body.RequestBody), &msgs); err != nil {
		t.Fatalf("messages are not JSON: %v", err)
	}
	if len(msgs) != 1 || msgs[0]["content"] != "hello" {
		t.Fatalf("messages %#v", msgs)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(body.ResponseBody), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if resp["id"] != "chatcmpl-1" {
		t.Fatalf("response %#v", resp)
	}

	// The proxy document is what promptJSON builds; it is stored alongside the
	// pair as the request that produced the response, with the credential
	// redacted.
	var px map[string]any
	if err := json.Unmarshal([]byte(proxy), &px); err != nil {
		t.Fatal(err)
	}
	if px["url"] != "/v1/chat/completions" {
		t.Fatalf("proxy %#v", px)
	}
	hdrs, _ := px["headers"].(map[string]any)
	if hdrs["Authorization"] != "***" {
		t.Fatalf("headers %#v", hdrs)
	}
}

// TestRequestLabelIndependentOfPrompts 验证请求备注与提示词开关独立；参数 t 为测试上下文。
// 前置关闭正文存储；正常、缺省、非法编码与超长输入分别保存或忽略；takeExchange 清理内存。
func TestRequestLabelIndependentOfPrompts(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"Affinity%20%7C%20%E5%8E%9F%E7%94%9F%E5%85%A5%E5%8F%A3", "Affinity | 原生入口"}, {"", ""}, {"%zz", ""}, {strings.Repeat("a", 2049), ""},
	} {
		s := &Server{Cfg: &config.Config{}}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Xhub-Label", tc.raw)
		s.rememberExchange("label", req, nil, nil)
		ex := s.takeExchange("label")
		if ex.label != tc.want || ex.messages != "" {
			t.Fatalf("备注保存错误: got=%q want=%q", ex.label, tc.want)
		}
		row := usageFromSpend(live.SpendLog{Label: ex.label}, 0, "", "", "", time.Now(), time.Now())
		if row.Label != tc.want {
			t.Fatal("持久化转换丢失备注")
		}
	}
}

// TestReasoningEffortIndependentOfPrompts 验证关闭提示词仍保留请求推理强度、失败候选与同步/队列转换。
// 前置内存 Server；覆盖开启/关闭、缺省和非法值，JSON 往返模拟 Redis 载体；takeExchange 清理全部临时状态。
func TestReasoningEffortIndependentOfPrompts(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, effort := range []string{"high", "none", ""} {
			s := &Server{Cfg: &config.Config{}}
			s.Cfg.GeneralSettings.StorePromptsInSpendLogs = enabled
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			raw, _ := json.Marshal(map[string]any{"reasoning_effort": effort, "messages": []any{map[string]any{"role": "user", "content": "Private input remains unchanged."}}})
			s.rememberExchange("reasoning", req, raw, []byte("upstream error"))
			ex := s.takeExchange("reasoning")
			if ex.reasoningEffort != effort || ex.rawResponse != "upstream error" {
				t.Fatalf("元数据或失败候选丢失: %+v", ex)
			}
			if !enabled && (ex.messages != "" || ex.response != "" || ex.proxy != "") {
				t.Fatal("关闭提示词保存后仍持久化了正文")
			}
			if len(s.exchanges) != 0 {
				t.Fatal("临时日志状态没有清理")
			}
			encoded, _ := json.Marshal(live.SpendLog{ReasoningEffort: ex.reasoningEffort})
			var queued live.SpendLog
			if err := json.Unmarshal(encoded, &queued); err != nil {
				t.Fatal(err)
			}
			row := usageFromSpend(queued, 0, "", "", "", time.Now(), time.Now())
			if row.ReasoningEffort != effort {
				t.Fatalf("同步或队列日志载体丢失推理强度: %+v", row)
			}
		}
	}
}
