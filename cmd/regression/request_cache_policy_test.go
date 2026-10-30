package regression

import "testing"

// TestRequestCachePolicy 用真实HTTP/PG与可计数本地上游验证客户端读写控制、缓存账单和准入边界。
// 每种策略使用独立正文；默认命中、绕过刷新、禁止写入及错误参数均有断言，所有数据由私有schema清理。
func TestRequestCachePolicy(t *testing.T) {
	h := newHarness(t, chatDeployment("request-cache"))
	admin := h.adminSession()
	owner := h.openScope(t, admin, "request-cache")
	for _, tc := range []struct {
		name   string
		flags  map[string]any
		stored bool
	}{
		{"no-cache", map[string]any{"no-cache": true}, true},
		{"no-store", map[string]any{"no-store": true}, false},
		{"both", map[string]any{"no-cache": true, "no-store": true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := chatRequest("request-cache", "Test cache policy "+tc.name)
			req["cache"] = tc.flags
			start := len(h.upstreamCalls())
			for i := 0; i < 2; i++ {
				got := h.ok("POST", "/v1/chat/completions", owner.key, req)
				if isCacheHit(got) || parseFloatOrZero(got.header("x-litellm-response-cost")) <= 0 || len(h.upstreamCalls()) != start+i+1 {
					t.Fatal("客户端控制未真实访问上游并计费", got.describe())
				}
			}
			delete(req, "cache")
			normal := h.ok("POST", "/v1/chat/completions", owner.key, req)
			if isCacheHit(normal) != tc.stored {
				t.Fatal("缓存写入策略不符", normal.describe())
			}
			// no-store允许读取已有值；它只禁止本轮写入，而no-cache必须重新请求上游。
			req["cache"] = map[string]any{"no-store": true}
			if got := h.ok("POST", "/v1/chat/completions", owner.key, req); !isCacheHit(got) {
				t.Fatal("no-store误禁已有缓存读取", got.describe())
			}
			req["cache"] = map[string]any{"no-cache": true}
			before := len(h.upstreamCalls())
			if got := h.ok("POST", "/v1/chat/completions", owner.key, req); isCacheHit(got) || len(h.upstreamCalls()) != before+1 {
				t.Fatal("no-cache未绕过已有值", got.describe())
			}
		})
	}
	for _, flags := range []any{"true", []any{}, map[string]any{"no-cache": "true"}, map[string]any{"no-store": 1}} {
		req := chatRequest("request-cache", "Reject invalid cache policy.")
		req["cache"] = flags
		before := len(h.upstreamCalls())
		if got := h.do("POST", "/v1/chat/completions", owner.key, req); got.status != 400 || len(h.upstreamCalls()) != before {
			t.Fatal("无效控制未明确拒绝", got.describe())
		}
	}
	for _, path := range []string{"/v1/messages", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			req := chatRequest("request-cache", "Verify another protocol "+path)
			req["cache"] = map[string]any{"no-cache": true, "no-store": true}
			if path == "/v1/messages" {
				req["max_tokens"] = 16
			} else {
				delete(req, "messages")
				req["input"] = "Verify Responses cache control."
			}
			before := len(h.upstreamCalls())
			for i := 0; i < 2; i++ {
				got := h.ok("POST", path, owner.key, req)
				if isCacheHit(got) || len(h.upstreamCalls()) != before+i+1 || parseFloatOrZero(got.header("x-litellm-response-cost")) <= 0 {
					t.Fatal("其他协议控制或费用错误", got.describe())
				}
			}
		})
	}
	req := chatRequest("request-cache", "Check authorization.")
	req["cache"] = map[string]any{"no-cache": true}
	before := len(h.upstreamCalls())
	if got := h.do("POST", "/v1/chat/completions", "", req); got.status != 401 {
		t.Fatal(got.describe())
	}
	blocked := h.openScope(t, admin, "request-cache-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if got := h.do("POST", "/v1/chat/completions", blocked.key, req); got.status != 403 || len(h.upstreamCalls()) != before {
		t.Fatal("缓存参数绕过预算", got.describe())
	}
}
