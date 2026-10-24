package regression

import (
	"fmt"
	"strings"
	"testing"
)

// TestKeyContractStateWriteFailure 在隔离PG注入状态更新约束，验证禁用/恢复写失败返回500、状态不变且真实推理准入一致。
// 使用真实HTTP和本地受控上游，不模拟持久化；结束显式删除Key，Cleanup撤销约束，harness清理schema。
func TestKeyContractStateWriteFailure(t *testing.T) {
	c := newKeyContractClient(t)
	created := c.call("POST", "/key/generate", c.admin, map[string]any{"key_alias": "State confirmation"}, 200, true).json()
	id, token := created["token_id"].(string), created["key"].(string)
	body := map[string]any{"key": id}
	t.Cleanup(func() { catalogSQL(t, c.h, `ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS key_state_reject`) })
	sequence := 0
	// verify读取真实状态并调用数据面；blocked为预期，拒绝时不能调用上游，成功时必须看到受控上游响应。
	verify := func(blocked bool) {
		t.Helper()
		info := c.call("GET", "/key/info?key="+id, c.admin, nil, 200, true).json()["info"].(map[string]any)
		if info["blocked"] != blocked {
			t.Fatalf("写失败改变了密钥状态: expected blocked=%v got=%v", blocked, info["blocked"])
		}
		sequence++ // 每次请求使用不同提示词，避免缓存命中掩盖真实上游准入验证。
		mark := len(c.h.upstreamCalls())
		r := c.h.do("POST", "/v1/chat/completions", token, chatRequest("key-contract-chat", fmt.Sprintf("Verify key state after a failed write, check %d.", sequence)))
		if blocked {
			if r.status != 401 || len(c.h.upstreamCalls()) != mark {
				t.Fatalf("禁用密钥未被真实数据面拒绝: %s", r.describe())
			}
		} else if r.status != 200 || !strings.Contains(r.text(), "regression-ok") || len(c.h.upstreamCalls()) <= mark {
			t.Fatalf("有效密钥未能访问真实测试上游: %s", r.describe())
		}
	}
	for _, state := range []struct {
		path, rejected string
		blocked        bool
	}{{"/key/block", "blocked", true}, {"/key/unblock", "active", false}} {
		// NOT VALID只允许保留其他夹具已有行；后续真实写入仍受约束，避免修改无关Key来制造失败。
		catalogSQL(t, c.h, `ALTER TABLE api_keys ADD CONSTRAINT key_state_reject CHECK (status <> '`+state.rejected+`') NOT VALID`)
		c.call("POST", state.path, c.admin, body, 500, true)
		verify(!state.blocked)
		catalogSQL(t, c.h, `ALTER TABLE api_keys DROP CONSTRAINT key_state_reject`)
		out := c.call("POST", state.path, c.admin, body, 200, true).json()
		if out["blocked"] != state.blocked {
			t.Fatalf("恢复存储后响应未确认目标状态: %v", out)
		}
		verify(state.blocked)
	}
	c.h.flushSpend()
	c.call("POST", "/key/delete", c.admin, map[string]any{"keys": []string{id}}, 200, true)
	c.call("GET", "/key/info?key="+id, c.admin, nil, 404, true)
}
