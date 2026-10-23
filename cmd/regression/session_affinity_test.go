package regression

import (
	"fmt"
	"strings"
	"testing"
)

// TestSessionAffinityRoutingContract 验证模板、模型默认权重、动态偏好与回退在真实请求链中统一生效。
// 前置隔离 PostgreSQL、真实网关和本地受控上游；覆盖显式/推断身份、配置失效、回退复用、权限和日志费用。
// 所有数据由当前 harness 创建；defer 解除模板、策略和部署引用，harness 最终删除独立 schema。
func TestSessionAffinityRoutingContract(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "session-routing")
	for i, name := range []string{"session-alpha", "session-beta", "session-backup"} {
		public := "session-shared"
		if i == 2 {
			public = "session-backup"
		}
		h.addDBModel(t, admin, public, name, "dep-"+name, nil)
		h.ok("POST", "/model/update", admin, map[string]any{"id": "dep-" + name, "model_info": map[string]any{"endpoint_types": []string{"chat", "bypass:openai-chat"}}})
	}
	body := routeTemplateBody([]any{}, 2, 60, 0, 0)
	templateID := routeTemplate(t, h, admin, "session routing contract", body)
	bindTemplate(t, h, admin, "key", owner.keyID, templateID)
	defer func() {
		bindTemplate(t, h, admin, "key", owner.keyID, "")
		h.ok("POST", "/route_template/"+templateID+"/delete", admin, map[string]any{})
		h.ok("PUT", "/model/fallback", admin, map[string]any{"model_name": "session-shared", "policy": map[string]any{}})
		for _, name := range []string{"session-alpha", "session-beta", "session-backup"} {
			h.ok("POST", "/model/delete", admin, map[string]any{"id": "dep-" + name})
		}
	}()
	// weights 保存两条同名部署的默认分配；参数是相对权重，接口返回由 h.ok 校验，无外部服务。
	weights := func(a, b int) {
		h.ok("PUT", "/model/default", admin, map[string]any{"model_name": "session-shared", "weights": map[string]any{"allocations": []any{
			map[string]any{"deployment_id": "dep-session-alpha", "weight": a}, map[string]any{"deployment_id": "dep-session-beta", "weight": b},
		}}})
	}
	sequence := 0
	// call 发起独立正文的同一显式会话，断言响应身份、真实调用链和一次成功计费；返回响应供日志检查。
	// 参数为协议入口和预期上游顺序，清空的只是本 harness 的观测记录，费用和配置保留用于下一轮。
	call := func(path, want string) reply {
		t.Helper()
		sequence++
		h.resetUpstream()
		before := h.moneyOf(t, owner)
		req := chatRequest("session-shared", fmt.Sprintf("Conversation round %d", sequence))
		req["session_id"] = "session-contract"
		result := h.doHeaders("POST", path, owner.key, req, map[string]string{"X-Session-Id": "session-contract"})
		if result.status != 200 || result.header("X-Session-Id") != "session-contract" {
			t.Fatalf("会话身份未保持: %s", result.describe())
		}
		if got := strings.Join(h.upstreamSince(0), ","); got != want {
			t.Fatalf("实际选路=%s，预期=%s", got, want)
		}
		for _, observation := range h.upstreamCalls() {
			if _, exists := observation.Body["session_id"]; exists {
				t.Fatal("网关会话字段泄露到上游")
			}
		}
		h.flushSpend()
		if cost := parseFloatOrZero(result.header("x-litellm-response-cost")); cost <= 0 || !h.moneyOf(t, owner).grewBy(before, cost) {
			t.Fatal("本轮未按唯一成功部署结算")
		}
		return result
	}
	weights(1, 0)
	call("/v1/chat/completions", "session-alpha")
	// 默认归零应即时失效；恢复正权重不会抢走已经合法保持的 beta。
	weights(0, 1)
	call("/v1/chat/completions", "session-beta")
	weights(1, 1)
	call("/v1/chat/completions", "session-beta")
	// 模板独立分配覆盖默认并令 beta 失效，移除覆盖恢复继承。
	body["model_routes"] = []any{map[string]any{"model": "session-shared", "strategy": "traffic-split", "allocations": []any{
		map[string]any{"deployment_id": "dep-session-alpha", "weight": 1}, map[string]any{"deployment_id": "dep-session-beta", "weight": 0},
	}}}
	h.ok("POST", "/route_template/"+templateID+"/update", admin, map[string]any{"body": body})
	call("/v1/chat/completions", "session-alpha")
	body["model_routes"] = []any{}
	h.ok("POST", "/route_template/"+templateID+"/update", admin, map[string]any{"body": body})
	h.scriptStatus("session-alpha", 500)
	call("/v1/chat/completions", "session-alpha,session-alpha,session-beta")
	call("/v1/chat/completions", "session-beta")
	h.scriptStatus("session-beta", 500)
	h.ok("PUT", "/model/fallback", admin, map[string]any{"model_name": "session-shared", "policy": map[string]any{"fallbacks": []string{"session-backup"}}})
	call("/v1/chat/completions", "session-beta,session-beta,session-alpha,session-alpha,session-backup")
	last := call("/v1/chat/completions", "session-backup")
	log := h.ok("GET", "/spend/logs/ui/"+last.header("x-litellm-call-id"), admin, nil).json()
	if log["session_id"] != "session-contract" || log["session_source"] != "explicit" {
		t.Fatalf("日志丢失会话来源: %v", log)
	}
	// 禁用回退必须拒绝已缓存的跨模型目标；错误调用不扣费。
	h.resetUpstream()
	before := h.moneyOf(t, owner)
	req := chatRequest("session-shared", "Disabled fallback round")
	req["disable_fallbacks"] = true
	r := h.doHeaders("POST", "/v1/chat/completions", owner.key, req, map[string]string{"X-Session-Id": "session-contract"})
	if r.status != 500 || strings.Contains(strings.Join(h.upstreamSince(0), ","), "session-backup") {
		t.Fatalf("禁用回退仍复用缓存: %s", r.describe())
	}
	h.flushSpend()
	if !h.moneyOf(t, owner).same(before) {
		t.Fatal("全部失败仍扣费")
	}
	// 模型权限收紧后不能通过合法回退链绕过鉴权。
	h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{"session-shared"}})
	h.resetUpstream()
	r = h.doHeaders("POST", "/v1/chat/completions", owner.key, chatRequest("session-shared", "Permission check"), map[string]string{"X-Session-Id": "session-contract"})
	if r.status != 401 || len(h.upstreamCalls()) != 0 {
		t.Fatalf("缓存目标绕过权限: %s", r.describe())
	}
	h.ok("POST", "/key/update", admin, map[string]any{"key": owner.key, "models": []string{}})
	// 撤销链后不得继续使用旧 backup；改权重确定当前合法目标。
	h.ok("PUT", "/model/fallback", admin, map[string]any{"model_name": "session-shared", "policy": map[string]any{}})
	h.scriptStatus("session-alpha", 0)
	h.scriptStatus("session-beta", 0)
	weights(1, 0)
	call("/v1/chat/completions", "session-alpha")
	call("/bypass/openai/v1/chat/completions", "session-alpha")
	// 完整历史无 ID 可稳定推断；模型和工具追加不改变首轮指纹。
	first := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("session-shared", "Explain dynamic routing"))
	inferred := first.header("X-Session-Id")
	history := chatRequest("session-shared", "Explain dynamic routing")
	history["messages"] = append(history["messages"].([]any), map[string]any{"role": "assistant", "content": "A route selects a deployment."}, map[string]any{"role": "user", "content": "Explain the next round"})
	next := h.ok("POST", "/v1/chat/completions", owner.key, history)
	if !strings.HasPrefix(inferred, "prompt:v1:") || next.header("X-Session-Id") != inferred {
		t.Fatal("完整历史推断未保持")
	}
	h.flushSpend()
	log = h.ok("GET", "/spend/logs/ui/"+next.header("x-litellm-call-id"), admin, nil).json()
	if log["session_source"] != "prompt" {
		t.Fatalf("推断来源未记录: %v", log)
	}
	grouped := h.ok("GET", "/spend/logs/ui?group_by_session=true&session_id="+inferred, admin, nil).json()
	rows := listField(grouped, "data")
	if len(rows) != 1 || rows[0]["session_total_count"] != 2.0 {
		t.Fatalf("推断日志未正确归组: %v", grouped)
	}
	// 信号冲突和未知父响应在发出上游请求前明确拒绝。
	for _, invalid := range []map[string]any{{"session_id": "other"}, {"previous_response_id": "unknown-response"}} {
		req := chatRequest("session-shared", "Invalid identity")
		for key, value := range invalid {
			req[key] = value
		}
		h.resetUpstream()
		r := h.doHeaders("POST", "/v1/chat/completions", owner.key, req, map[string]string{"X-Session-Id": "session-contract"})
		if r.status != 400 || len(h.upstreamCalls()) != 0 {
			t.Fatalf("非法身份未提前拒绝: %s", r.describe())
		}
	}
}
