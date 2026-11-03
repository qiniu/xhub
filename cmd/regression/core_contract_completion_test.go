package regression

import (
	"net/http/httptest"
	"testing"

	"github.com/sunqirui1987/xhub/contracts/usage"
)

// TestKeyContractRemaining 验证轮换两种入口、计费重置、别名、健康与部分批量更新真实结果；私有PG/本地上游，显式删除Key且harness清理schema。
func TestKeyContractRemaining(t *testing.T) {
	c := newKeyContractClient(t)
	call := func(m, p string, b any, w int) reply { t.Helper(); return c.call(m, p, c.admin, b, w, true) }
	created := call("POST", "/key/generate", map[string]any{"key_alias": "contract-rotate", "max_budget": 10}, 200).json()
	id, token := created["token_id"].(string), created["key"].(string)
	call("POST", "/key/health", nil, 200)
	c.call("POST", "/key/health", token, nil, 200, true)
	aliases := call("GET", "/key/aliases?search=CONTRACT-ROTATE", nil, 200).json()
	if aliases["total_count"] != float64(1) {
		t.Fatalf("别名搜索错误: %+v", aliases)
	}
	c.h.ok("POST", "/v1/chat/completions", token, chatRequest("key-contract-chat", "Verify rotation usage."))
	c.h.flushSpend()
	before := call("GET", "/key/info?key="+id, nil, 200).json()["info"].(map[string]any)
	if before["spend"].(float64) <= 0 {
		t.Fatal("推理未实际计费")
	}
	reset := call("POST", "/key/"+id+"/reset_spend", map[string]any{"reset_to": 0}, 200).json()
	if reset["spend"] != float64(0) || reset["max_budget"] != float64(10) {
		t.Fatal("重置改变额度或未清计数")
	}
	for _, path := range []string{"/key/regenerate", "/key/" + id + "/regenerate"} {
		old := token
		out := call("POST", path, map[string]any{"key": id}, 200).json()
		token = out["key"].(string)
		if token == old {
			t.Fatal("轮换未产生新Key")
		}
		if r := c.h.do("POST", "/v1/chat/completions", old, chatRequest("key-contract-chat", "Old key must fail.")); r.status != 401 {
			t.Fatal("旧Key未撤销")
		}
		c.h.ok("POST", "/v1/chat/completions", token, chatRequest("key-contract-chat", "New key must work."))
	}
	partial := call("POST", "/key/bulk_update", map[string]any{"keys": []string{id, "missing-key"}, "key_alias": "contract-updated"}, 200).json()
	if partial["updated"] != float64(1) || len(partial["keys"].([]any)) != 1 {
		t.Fatalf("部分成功被错误计数: %+v", partial)
	}
	zero := call("POST", "/key/bulk_update", map[string]any{"keys": []string{"missing-key"}, "key_alias": "none"}, 200).json()
	if zero["updated"] != float64(0) || len(zero["keys"].([]any)) != 0 {
		t.Fatal("零更新假成功")
	}
	c.call("POST", "/key/"+id+"/reset_spend", c.admin, map[string]any{"reset_to": -1}, 400, false)
	for _, p := range []string{"/key/regenerate", "/key/" + id + "/regenerate", "/key/" + id + "/reset_spend", "/key/bulk_update", "/key/health"} {
		c.call("POST", p, "", map[string]any{}, 401, false)
	}
	c.call("GET", "/key/aliases", "", nil, 401, false)
	call("POST", "/key/delete", map[string]any{"key": id}, 200)
}

// TestUserContractPassword 验证两种密码入口的成功、字节校验、未登录、旧会话撤销及Key仍可推理；隔离用户最终删除，无线上数据。
func TestUserContractPassword(t *testing.T) {
	c := newUserContractClient(t)
	a := c.h.provision(t, c.admin, "contract-password")
	for _, path := range []string{"/user/set_password", "/user/" + a.userID + "/password"} {
		c.call("POST", path, "", map[string]any{"user_id": a.userID, "password": "Contract-next-123"}, 401, true)
		c.call("POST", path, c.admin, map[string]any{"user_id": a.userID, "password": "short"}, 500, true)
		out := c.call("POST", path, c.admin, map[string]any{"user_id": a.userID, "password": "Contract-next-123"}, 200, true).json()
		if out["password_updated"] != true || out["user_id"] != a.userID {
			t.Fatal("重置确认错误")
		}
		if r := c.h.do("GET", "/auth/me", a.session, nil); r.status != 401 {
			t.Fatal("旧会话未撤销")
		}
		a.session = c.h.login(a.email, "Contract-next-123")
		c.h.ok("GET", "/auth/me", a.session, nil)
		c.h.ok("POST", "/v1/chat/completions", a.key, chatRequest("user-contract-chat", "Password reset retains keys."))
	}
	c.call("POST", "/user/delete", c.admin, map[string]any{"user_id": a.userID}, 200, true)
}

// TestUsageContractRealTraffic 真实成功/失败流量核对日志列表、详情、会话和日报契约及数值；私有PG、本地故障上游，harness清理全部日志/账户。
func TestUsageContractRealTraffic(t *testing.T) {
	h := newHarness(t, chatDeployment("usage-contract-chat"))
	admin := h.adminSession()
	a := h.provision(t, admin, "usage-a")
	b := h.provision(t, admin, "usage-b")
	v, err := usage.NewValidator(usage.Document)
	if err != nil {
		t.Fatal(err)
	}
	call := func(path, token string, want int) reply {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if err := v.Request(req); err != nil {
			t.Fatal(err)
		}
		r := h.do("GET", path, token, nil)
		if r.status != want {
			t.Fatalf("%s: %s", path, r.describe())
		}
		if err := v.Response(req, r.status, r.headers, r.body); err != nil {
			t.Fatalf("%s: %v; %s", path, err, r.describe())
		}
		return r
	}
	success := h.ok("POST", "/v1/chat/completions", a.key, chatRequest("usage-contract-chat", "Contract log success."))
	id := success.headers.Get("x-litellm-call-id")
	h.scriptStatus("usage-contract-chat", 403)
	failed := h.do("POST", "/v1/chat/completions", a.key, chatRequest("usage-contract-chat", "Contract log failure."))
	if failed.status != 403 {
		t.Fatalf("预期403: %s", failed.describe())
	}
	h.flushSpend()
	for _, path := range []string{"/spend/logs/ui", "/spend/logs/v2"} {
		rows := call(path+"?user_id="+a.userID, admin, 200).json()["data"].([]any)
		if len(rows) != 2 {
			t.Fatalf("两条实际调用应有两条日志: %d", len(rows))
		}
		filtered := call(path+"?error_code=403", a.session, 200).json()["data"].([]any)
		if len(filtered) != 1 {
			t.Fatal("403筛选不对应真实错误")
		}
		call(path, "", 401)
	}
	detail := call("/spend/logs/ui/"+id, a.session, 200).json()
	if detail["spend"].(float64) <= 0 {
		t.Fatal("成功未计费")
	}
	call("/spend/logs/ui/"+id, b.session, 404)
	call("/spend/logs/ui/"+id, admin, 200)
	call("/spend/logs/ui/missing", admin, 404)
	session := detail["session_id"].(string)
	call("/spend/logs/session/ui?session_id="+session, a.session, 200)
	// 兼容空会话查询在鉴权前返回空页；仅锁定既有行为，不能误称为鉴权成功。
	empty := call("/spend/logs/session/ui", "", 200).json()
	if empty["total"] != float64(0) {
		t.Fatal("空会话泄露数据")
	}
	for _, path := range []string{"/user/daily/activity", "/user/daily/activity/aggregated", "/team/daily/activity", "/team/daily/activity/aggregated", "/organization/daily/activity"} {
		out := call(path+"?user_id="+a.userID, admin, 200).json()
		meta := out["metadata"].(map[string]any)
		if meta["total_api_requests"] != float64(2) || meta["total_successful_requests"] != float64(1) || meta["total_failed_requests"] != float64(1) {
			t.Fatalf("日报计数不一致: %+v", meta)
		}
		call(path, "", 401)
	}
	// 隔离库真实SQL故障必须返回500，不能用空列表当成功；约束/表名均在测试schema内恢复。
	catalogSQL(t, h, `ALTER TABLE usage_events RENAME TO usage_events_contract_hidden`)
	t.Cleanup(func() { catalogSQL(t, h, `ALTER TABLE usage_events_contract_hidden RENAME TO usage_events`) })
	call("/spend/logs/ui", admin, 500)
}
