package regression

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestModelMonitorResponsesStream 验证真实 Responses 流的首事件计时、生成速度及同步请求统一口径。
// 参数 t 为测试上下文，无返回值；本地供应商分段刷新并延迟终态，私有 schema 和服务自动清理。
func TestModelMonitorResponsesStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response := map[string]any{"id": "resp_monitor", "object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "hello"}}}}, "usage": map[string]any{"input_tokens": 11, "output_tokens": 5}}
		if body["stream"] != true {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			time.Sleep(30 * time.Millisecond)
			writeJSON(w, response)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// 延迟首事件和终态，确保测到两个独立时间点，不依赖立即写入造成的毫秒舍入。
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_monitor\"}}\n\n")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"hello\"}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
		raw, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
		fmt.Fprintf(w, "data: %s\n\n", raw)
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(upstream.Close)
	dep := chatDeployment("monitor-responses")
	dep.ModelInfo = map[string]any{"id": "monitor-responses-deployment", "transport": "bypass_openai_responses", "endpoint_types": []string{"responses"}}
	dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
	h := newHarness(t, dep)
	session := h.personalBillingSession()
	for _, stream := range []bool{true, false} {
		r := h.ok("POST", "/v1/responses", session, map[string]any{"model": dep.ModelName, "input": "Monitor streaming probe", "stream": stream, "store": false})
		if stream && !strings.Contains(string(r.body), "response.completed") {
			t.Fatal("流式请求没有完成事件", r.describe())
		}
	}
	h.flushSpend()
	logs := h.ok("GET", "/spend/logs/ui", session, nil).json()
	rows := listField(logs, "data")
	if len(rows) != 2 {
		t.Fatalf("日志请求数不匹配: %v", logs)
	}
	for _, row := range rows {
		if row["inbound_endpoint"] != "/v1/responses" || row["outbound_endpoint"] != "/v1/responses" {
			t.Fatalf("异步落库后的日志端点丢失: %v", row)
		}
	}
	summary := h.ok("GET", "/models/monitor", session, nil).json()["summary"].(map[string]any)
	if summary["requests"] != float64(2) || summary["total_tokens"] != float64(32) || summary["tps_samples"] != float64(2) || summary["ttft_samples"] != float64(2) {
		t.Fatal("真实流式请求必须产生性能样本，同步请求同样产生首字节性能样本", summary)
	}
	if tps, ok := summary["avg_tps"].(float64); !ok || tps <= 0 {
		t.Fatal("流式 TPS 必须为正数", summary)
	}
	if ttft, ok := summary["avg_ttft_ms"].(float64); !ok || ttft <= 0 {
		t.Fatal("流式首事件耗时必须为正数", summary)
	}
}

// TestModelMonitorHTTP 使用真实网关、私有数据库和本地供应商验证推理到预聚合、授权、筛选和错误响应。
// 参数 t 为测试上下文，无返回值；harness 自动清理服务及 schema，非流式不伪造性能样本。
func TestModelMonitorHTTP(t *testing.T) {
	a := chatDeployment("monitor-alias-a")
	a.LiteLLMParams["model"] = "actual-model"
	b := chatDeployment("monitor-alias-b")
	b.LiteLLMParams["model"] = "actual-model"
	c := chatDeployment("monitor-unused")
	c.LiteLLMParams["model"] = "unused-actual"
	h := newHarness(t, a, b, c)
	admin := h.adminSession()
	tn := h.provision(t, admin, "monitor")
	other := h.provision(t, admin, "other-monitor")
	empty := h.ok("GET", "/models/monitor", h.login(other.email, other.password), nil).json()
	if empty["summary"].(map[string]any)["requests"] != float64(0) {
		t.Fatal("新用户统计非空", empty)
	}
	options := empty["options"].([]any)
	if len(options) != 2 || options[0].(map[string]any)["supplier"] == "" || options[1].(map[string]any)["model"] != "unused-actual" {
		t.Fatal("没有调用记录时仍应列出已配置连接及实际模型", empty)
	}
	h.ok("POST", "/key/update", admin, map[string]any{"key": other.key, "models": []string{"monitor-unused"}})
	restricted := h.ok("GET", "/models/monitor", other.key, nil).json()["options"].([]any)
	if len(restricted) != 1 || restricted[0].(map[string]any)["model"] != "unused-actual" {
		t.Fatal("受限密钥不能看到未授权配置模型", restricted)
	}
	for _, alias := range []string{"monitor-alias-a", "monitor-alias-b"} {
		h.ok("POST", "/v1/chat/completions", tn.key, map[string]any{"model": alias, "messages": []any{map[string]any{"role": "user", "content": "Monitor regression probe"}}})
	}
	h.flushSpend()
	body := h.ok("GET", "/models/monitor", h.login(tn.email, tn.password), nil).json()
	summary := body["summary"].(map[string]any)
	if summary["requests"] != float64(2) || summary["ttft_samples"] != float64(2) || math.Abs(summary["cost"].(float64)-2*expectedCost()) > 1e-9 {
		t.Fatal("预聚合统计错误", body)
	}
	models := body["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["model"] != "actual-model" {
		t.Fatal("别名应合并到同连接实际模型", models)
	}
	filtered := h.ok("GET", "/models/monitor?model=missing", admin, nil).json()
	if len(filtered["models"].([]any)) != 0 {
		t.Fatal("模型筛选未生效", filtered)
	}
	if len(filtered["options"].([]any)) != 2 {
		t.Fatal("目录不能随统计筛选变空", filtered)
	}
	filtered = h.ok("GET", "/models/monitor?supplier=missing", admin, nil).json()
	if filtered["summary"].(map[string]any)["requests"] != float64(0) {
		t.Fatal("供应商筛选未生效", filtered)
	}
	hidden := h.ok("GET", "/models/monitor", h.login(other.email, other.password), nil).json()
	if hidden["summary"].(map[string]any)["requests"] != float64(0) {
		t.Fatal("监控越权", hidden)
	}
	for _, query := range []string{"timezone=1", "start_date=invalid", "start_date=2026-01-01&end_date=2026-04-01"} {
		r := h.do("GET", "/models/monitor?"+query, admin, nil)
		if r.status != 400 || r.json()["error"] == nil {
			t.Fatal("错误日期必须返回 JSON 400", r.describe())
		}
	}
	if r := h.do("GET", "/models/monitor", "", nil); r.status != http.StatusUnauthorized {
		t.Fatal("匿名必须拒绝", r.describe())
	}
}

// TestModelMonitorCatalogPermissions 验证真实会话默认目录与模型发现一致、组织收窄立即生效、同名模型按供应商隔离。
// 前置两个隔离租户及本地上游；覆盖 Key、显式/伪造团队、空交集和统计归属，harness 回收全部 schema 与服务。
func TestModelMonitorCatalogPermissions(t *testing.T) {
	a, b, c := chatDeployment("monitor-grant-a"), chatDeployment("monitor-grant-b"), chatDeployment("monitor-hidden")
	a.LiteLLMParams["model"], b.LiteLLMParams["model"] = "shared-actual", "shared-actual"
	a.LiteLLMParams["litellm_credential_name"] = "monitor-provider-a"
	b.LiteLLMParams["litellm_credential_name"] = "monitor-provider-b"
	h := newHarness(t, a, b, c)
	admin := h.adminSession()
	for _, supplier := range []string{"monitor-provider-a", "monitor-provider-b"} {
		h.ok("POST", "/credentials", admin, map[string]any{
			"credential_name":   supplier,
			"credential_info":   map[string]any{"custom_llm_provider": "openai"},
			"credential_values": map[string]any{"api_base": h.prices.URL + "/v1", "api_key": "sk-fake"},
		})
	}
	mine, other := h.provision(t, admin, "monitor-granted"), h.provision(t, admin, "monitor-foreign")
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": mine.orgID, "models": []string{a.ModelName}})
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": other.orgID, "models": []string{b.ModelName}})
	for _, probe := range []struct{ key, alias string }{{mine.key, a.ModelName}, {other.key, b.ModelName}} {
		h.ok("POST", "/v1/chat/completions", probe.key, map[string]any{"model": probe.alias, "messages": []any{map[string]any{"role": "user", "content": "Verify monitor authorization."}}})
	}
	h.flushSpend()
	want := []any{map[string]any{"supplier": "monitor-provider-a", "model": "shared-actual"}}
	for _, principal := range []string{mine.session, mine.key} {
		for _, query := range []string{"", "?team_id=" + mine.teamID} {
			body := h.ok("GET", "/models/monitor"+query, principal, nil).json()
			if !reflect.DeepEqual(body["options"], want) {
				t.Fatalf("省略团队参数不能泄露未授权供应商: options=%v want=%v", body["options"], want)
			}
			if body["summary"].(map[string]any)["requests"] != float64(1) {
				t.Fatal("监控统计不得包含其他租户的调用", body)
			}
			rows := body["models"].([]any)
			if len(rows) != 1 || rows[0].(map[string]any)["supplier"] != "monitor-provider-a" {
				t.Fatal("同名模型的其他供应商统计泄露", rows)
			}
		}
	}
	listed := h.ok("GET", "/v1/models", mine.session, nil).json()["data"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["id"] != a.ModelName {
		t.Fatal("监控目录应与会话模型发现使用相同组织上限", listed)
	}
	for _, query := range []string{"?team_id=" + other.teamID, "?team_id=missing-team"} {
		response := h.do("GET", "/models/monitor"+query, mine.session, nil)
		if response.status != http.StatusNotFound || response.json()["error"] == nil {
			t.Fatal("伪造团队应返回明确 404，不能降级成全量目录", response.describe())
		}
	}
	// 已有会话必须实时应用组织扩展，而密钥仍保留更窄的自身限制。
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": mine.orgID, "models": []string{a.ModelName, b.ModelName}})
	h.ok("POST", "/key/update", admin, map[string]any{"key": mine.key, "models": []string{a.ModelName}})
	if got := h.ok("GET", "/models/monitor", mine.session, nil).json()["options"].([]any); len(got) != 2 {
		t.Fatal("已有会话没有实时读取组织模型扩展", got)
	}
	if got := h.ok("GET", "/models/monitor?team_id="+other.teamID, mine.key, nil).json()["options"]; !reflect.DeepEqual(got, want) {
		t.Fatal("Key 不能通过团队提示扩大目录权限", got)
	}
	// 团队保留 a 而组织改成 b，交集为空，不能把空结果误当成继承全部。
	h.ok("POST", "/team/update", admin, map[string]any{"team_id": mine.teamID, "models": []string{a.ModelName}})
	h.ok("PATCH", "/organization/update", admin, map[string]any{"organization_id": mine.orgID, "models": []string{b.ModelName}})
	for _, principal := range []string{mine.session, mine.key} {
		body := h.ok("GET", "/models/monitor", principal, nil).json()
		if got := body["options"].([]any); len(got) != 0 {
			t.Fatal("组织/团队空交集必须返回空目录", got)
		}
		if body["summary"].(map[string]any)["requests"] != float64(1) {
			t.Fatal("模型授权变更不应抹去本人历史统计", body)
		}
	}
	if got := h.ok("GET", "/models/monitor", admin, nil).json()["options"].([]any); len(got) != 3 {
		t.Fatal("平台管理员仍应看到完整目录", got)
	}
	if got := h.ok("GET", "/models/monitor?model=shared-actual&supplier=monitor-provider-b", mine.session, nil).json()["summary"].(map[string]any)["requests"]; got != float64(0) {
		t.Fatal("显式选择未授权供应商不能读取其他用户统计", got)
	}
}
