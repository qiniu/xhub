package regression

import (
	"reflect"
	"testing"
)

// TestKeyContractBasicEditPreservesBoundaries 用真实HTTP/PG核对精简Key编辑的省略字段语义；独立schema和本地上游为前置。
// 修改名称后模型/路由/限额/到期时间不变，越权和零预算仍拒绝；显式删除Key，harness最终回收schema，无外部供应商依赖。
func TestKeyContractBasicEditPreservesBoundaries(t *testing.T) {
	c := newKeyContractClient(t)
	made := c.call("POST", "/key/generate", c.admin, map[string]any{"key_alias": "original 用户", "models": []string{"key-contract-chat"}, "allowed_routes": []string{"llm_api_routes"}, "max_budget": 10, "rpm_limit": 600, "tpm_limit": 100000, "duration": "1h"}, 200, true).json()
	id, token := made["token_id"].(string), made["key"].(string)
	before := c.call("GET", "/key/info?key="+id, c.admin, nil, 200, true).json()["info"].(map[string]any)
	c.call("POST", "/key/update", c.admin, map[string]any{"key": id, "key_alias": "renamed 用户"}, 200, true)
	after := c.call("GET", "/key/info?key="+id, c.admin, nil, 200, true).json()["info"].(map[string]any)
	if after["key_alias"] != "renamed 用户" {
		t.Fatal("名称未真实保存", after)
	}
	for _, field := range []string{"models", "allowed_routes", "max_budget", "rpm_limit", "tpm_limit", "expires", "user_id", "team_id", "blocked"} {
		if !reflect.DeepEqual(before[field], after[field]) {
			t.Fatalf("基本编辑改变%s: before=%v after=%v", field, before[field], after[field])
		}
	}
	c.call("POST", "/key/generate", token, map[string]any{}, 403, true)
	out := c.h.do("POST", "/v1/chat/completions", token, chatRequest("not-allowed", "Verify model restrictions remain enforced."))
	if out.status != 401 {
		t.Fatal("模型拒绝状态改变", out.describe())
	}
	c.h.ok("POST", "/v1/chat/completions", token, chatRequest("key-contract-chat", "Verify basic key editing leaves inference available."))
	if len(c.h.upstreamCalls()) != 1 {
		t.Fatal("允许调用未抵达上游或拒绝请求已外发")
	}
	c.call("POST", "/key/update", c.admin, map[string]any{"key": id, "max_budget": 0}, 200, true)
	out = c.h.do("POST", "/v1/chat/completions", token, chatRequest("key-contract-chat", "Verify zero budget remains blocked."))
	if out.status != 403 || len(c.h.upstreamCalls()) != 1 {
		t.Fatal("零预算规则改变", out.describe())
	}
	c.call("POST", "/key/delete", c.admin, map[string]any{"key": id}, 200, true)
	c.call("GET", "/key/info?key="+id, c.admin, nil, 404, true)
}
