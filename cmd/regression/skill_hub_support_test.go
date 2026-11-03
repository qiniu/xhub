package regression

import (
	"reflect"
	"testing"
)

// TestSkillHubSupportBoundary 核对前端支持提示对应真实后台：公开Skill发现已移除，管理配置不授予普通用户权限。
// 使用私有schema和本地上游，验证配置可读、失败不修改配置、正常费用与零预算；显式删配置，harness清理账号和schema。
func TestSkillHubSupportBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("skill-hub-support"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "skill-hub-support")
	const path = "/claude-code/plugins/skill-hub-support"
	h.ok("PUT", path, admin, map[string]any{"name": "skill-hub-support", "enabled": true, "description": "Original metadata", "source": map[string]any{"source": "github", "repo": "example/fixture"}})
	before, err := h.store.ListKV("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", owner.session, owner.key, admin} {
		if out := h.do("GET", "/public/skill_hub", token, nil); out.status != 404 {
			t.Errorf("已移除的公开目录不能伪报可用: %s", out.describe())
		}
	}
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.session, 403}, {owner.key, 403}, {admin, 200}} {
		if out := h.do("GET", "/claude-code/plugins", tc.token, nil); out.status != tc.status {
			t.Errorf("配置读取权限改变: %s", out.describe())
		}
	}
	after, err := h.store.ListKV("claude-code")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("读取改变配置", err)
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("skill-hub-support", "Verify metadata support messages leave inference available."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if cost <= 0 || len(h.upstreamCalls()) != 1 {
		t.Fatal("真实推理或费用异常")
	}
	blocked := h.openScope(t, admin, "skill-hub-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("skill-hub-support", "Verify zero budget remains blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("预算规则改变", out.describe())
	}
	h.ok("DELETE", path, admin, nil)
	if out := h.do("GET", path, admin, nil); out.status != 404 {
		t.Fatal("配置清理失败", out.describe())
	}
}
