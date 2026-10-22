package regression

import (
	"net/http"
	"strings"
	"testing"
)

// TestTeamlessSessionBudgetHTTP 验证无团队管理员和普通用户会话/个人密钥的完整 HTTP 预算契约。
// 参数 t 为测试上下文，无返回；空预算直达上游，零预算拦截且无消费，有限预算按个人累计，清空恢复。
// harness 提供独立数据库及本地供应商，结束回收全部数据；不依赖外部凭据。
func TestTeamlessSessionBudgetHTTP(t *testing.T) {
	for _, role := range []string{"proxy_admin", "user"} {
		t.Run(role, func(t *testing.T) {
			h := newHarness(t, chatDeployment("solo-session"))
			admin := h.adminSession()
			email := role + "-solo@example.com"
			user := h.ok(http.MethodPost, "/user/new", admin, map[string]any{"user_email": email, "password": "solo-session-password", "user_role": role}).json()
			uid := firstString(user, "user_id")
			session := h.login(email, "solo-session-password")
			key := h.ok(http.MethodPost, "/key/generate", session, map[string]any{}).json()
			keyID, secret := firstString(key, "token_id"), firstString(key, "key")
			credentials := []string{session, secret}
			// 零预算必须在尚未消费时设置；额度分配契约禁止把上限降至历史消费以下。
			h.setUserBudget(t, admin, uid, 0)
			markZero := len(h.upstreamCalls())
			for _, credential := range credentials {
				r := h.do(http.MethodPost, "/chat/completions", credential, chatRequest("solo-session", "zero "+role))
				if r.status != 403 || !strings.Contains(r.describe(), "User budget") {
					t.Fatalf("零预算未阻止个人调用: %s", r.describe())
				}
			}
			if len(h.upstreamSince(markZero)) != 0 {
				t.Fatal("零预算拒绝仍调用上游")
			}
			h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": uid, "max_budget": nil})
			// 无团队默认不限额，两种路径均实际转发，不能因管理员角色或凭证类型改变规则。
			for i, credential := range credentials {
				path := []string{"/chat/completions", "/v1/chat/completions"}[i]
				response := h.ok(http.MethodPost, path, credential, chatRequest("solo-session", "blank "+role+path))
				if !strings.Contains(response.describe(), "regression-ok") {
					t.Fatalf("个人不限额调用未返回上游正文: %s", response.describe())
				}
			}
			h.flushSpend()
			before, err := h.db.GetUser(t.Context(), uid)
			if err != nil || before.Spend <= 0 {
				t.Fatalf("个人消费未记账: user=%+v err=%v", before, err)
			}
			// 拒绝请求也保留审计用量；只统计实际产生费用的两次成功推理。
			events, err := h.db.Engine.QueryString("SELECT user_id, team_id, organization_id FROM usage_events WHERE user_id = ? AND cost > 0", uid)
			if err != nil || len(events) != 2 {
				t.Fatalf("个人会话/密钥用量数量错误: %v err=%v", events, err)
			}
			for _, event := range events {
				if event["team_id"] != "" || event["organization_id"] != "" {
					t.Fatalf("无团队用量被错误归属: %v", event)
				}
			}
			for _, ceiling := range []float64{before.Spend} {
				h.setUserBudget(t, admin, uid, ceiling)
				mark := len(h.upstreamCalls())
				for _, credential := range credentials {
					response := h.do(http.MethodPost, "/chat/completions", credential, chatRequest("solo-session", "blocked "+role))
					if response.status != 403 || !strings.Contains(response.describe(), "User budget has been exceeded") {
						t.Fatalf("个人消费上限未阻止调用: %s", response.describe())
					}
				}
				h.flushSpend()
				after, err := h.db.GetUser(t.Context(), uid)
				if err != nil || after.Spend != before.Spend || len(h.upstreamSince(mark)) != 0 {
					t.Fatalf("预算拒绝仍转发或扣费: user=%+v err=%v", after, err)
				}
			}
			h.setUserBudget(t, admin, uid, 10)
			h.ok(http.MethodPost, "/chat/completions", session, chatRequest("solo-session", "finite "+role))
			h.ok(http.MethodPost, "/user/update", admin, map[string]any{"user_id": uid, "max_budget": nil})
			h.ok(http.MethodPost, "/v1/chat/completions", secret, chatRequest("solo-session", "cleared "+role))
			h.ok(http.MethodPost, "/key/delete", session, map[string]any{"keys": []string{keyID}})
			if response := h.do(http.MethodPost, "/chat/completions", secret, chatRequest("solo-session", "deleted")); response.status != 401 {
				t.Fatalf("已删除密钥仍有效: %s", response.describe())
			}
			h.ok(http.MethodPost, "/user/delete", admin, map[string]any{"user_id": uid})
			if response := h.do(http.MethodGet, "/user/info?user_id="+uid, admin, nil); response.status != 404 {
				t.Fatalf("测试账号未清理: %s", response.describe())
			}
		})
	}
}
