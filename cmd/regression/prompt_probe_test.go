package regression

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestPromptProbeExecutesSavedPrompt 验证管理提示词测试实际执行保存模板并走正常数据面。
// 参数t为测试上下文；前置真实管理员、私有PostgreSQL和本地上游；核对模型、变量、响应、用量及日志，最后删除模板并由harness清理schema。
func TestPromptProbeExecutesSavedPrompt(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-probe"))
	admin := h.personalBillingSession()
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{
		"prompt_id": "probe-template", "model": "prompt-probe",
		"messages": []any{map[string]any{"role": "user", "content": "Hello {{name}}"}},
	})
	t.Cleanup(func() { h.ok(http.MethodDelete, "/prompts/probe-template", admin, nil) })
	r := h.ok(http.MethodPost, "/prompts/test", admin, map[string]any{
		"prompt_id": "probe-template", "prompt_variables": map[string]any{"name": "World"},
	})
	calls := h.upstreamCalls()
	if len(calls) != 1 {
		t.Fatalf("提示词测试未执行真实模型请求: calls=%d response=%s", len(calls), r.describe())
	}
	if calls[0].Body["model"] != "prompt-probe" || calls[0].Body["messages"].([]any)[0].(map[string]any)["content"] != "Hello World" {
		t.Fatalf("保存模板或变量未用于推理: %#v", calls[0].Body)
	}
	if got := r.json()["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"]; got != defaultReply.Content {
		t.Fatalf("返回内容并非真实上游回答: %s", r.describe())
	}
	usage := r.json()["usage"].(map[string]any)
	if usage["prompt_tokens"] != float64(defaultReply.PromptTokens) || usage["completion_tokens"] != float64(defaultReply.CompletionTokens) {
		t.Fatalf("测试未回传实际用量: %s", r.describe())
	}
	h.flushSpend()
	row := logDetail(t, h, admin, r.header("x-litellm-call-id"))
	if firstString(row, "model") != "prompt-probe" {
		t.Fatalf("提示词执行未进入标准请求日志: %#v", row)
	}
	want := float64(defaultReply.PromptTokens)*testInputRate + float64(defaultReply.CompletionTokens)*testOutputRate
	spend, _ := floatField(row, "spend")
	if !nearlyEqual(spend, want) || !nearlyEqual(parseFloatOrZero(r.header("x-litellm-response-cost")), want) {
		t.Fatalf("提示词真实计费不符: log=%v header=%s want=%v", spend, r.header("x-litellm-response-cost"), want)
	}
	user, err := h.db.UserByEmail(t.Context(), "regression-admin@example.com")
	if err != nil || !nearlyEqual(user.Spend, want) {
		t.Fatalf("提示词测试未计入调用用户额度: user=%+v err=%v", user, err)
	}
}

// TestPromptProbeRejectsInvalidRequests 验证真实路由的鉴权、严格正文与未支持能力边界。
// 参数t为上下文；前置私有库和正常模板，所有拒绝应400/401/403且不拨上游；结束显式删除模板并清理schema。
func TestPromptProbeRejectsInvalidRequests(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-probe"))
	admin := h.personalBillingSession()
	user := h.openScope(t, admin, "prompt-probe-user")
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "probe-validation", "model": "prompt-probe", "content": "Hello {{name}}"})
	t.Cleanup(func() { h.ok(http.MethodDelete, "/prompts/probe-validation", admin, nil) })
	for _, tc := range []struct {
		name, token, raw string
		status           int
	}{
		{"anonymous", "", `{"prompt_id":"probe-validation"}`, 401},
		{"ordinary user", user.session, `{"prompt_id":"probe-validation"}`, 403},
		{"bad JSON", admin, `{"prompt_id":`, 400},
		{"trailing JSON", admin, `{"prompt_id":"probe-validation"} {}`, 400},
		{"array", admin, `[]`, 400}, {"missing id", admin, `{}`, 400},
		{"missing variable", admin, `{"prompt_id":"probe-validation"}`, 400},
		{"wrong variable type", admin, `{"prompt_id":"probe-validation","prompt_variables":{"name":1}}`, 400},
		{"stream", admin, `{"prompt_id":"probe-validation","stream":true}`, 400},
		{"version", admin, `{"prompt_id":"probe-validation","version":1}`, 400},
		{"dotprompt", admin, `{"dotprompt_content":"User: Hello"}`, 400},
		{"unknown field", admin, `{"prompt_id":"probe-validation","api_key":"ignored-secret"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := catalogRawRequest(t, h, http.MethodPost, "/prompts/test", tc.token, tc.raw)
			if r.status != tc.status || !strings.Contains(r.text(), `"error"`) {
				t.Fatalf("非法提示词测试未按契约拒绝: %s", r.describe())
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("被拒绝的提示词仍请求了上游")
			}
		})
	}
}

// TestPromptProbeUsesDataPlaneAdmission 验证管理权限不绕过个人预算、模型可用性和上游错误处理。
// 参数t为上下文；在私有库使用真实管理员与本地供应商，预算耗尽不请求上游，上游403真实返回并记零费日志；恢复配置后清理。
func TestPromptProbeUsesDataPlaneAdmission(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-probe"))
	admin := h.personalBillingSession()
	u, err := h.db.UserByEmail(t.Context(), "regression-admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "probe-admission", "model": "prompt-probe", "content": "Hello"})
	t.Cleanup(func() { h.ok(http.MethodDelete, "/prompts/probe-admission", admin, nil) })
	h.setUserBudget(t, admin, u.ID, 0)
	blocked := h.do(http.MethodPost, "/prompts/test", admin, map[string]any{"prompt_id": "probe-admission"})
	if blocked.status != 403 || !strings.Contains(blocked.text(), "budget") || len(h.upstreamCalls()) != 0 {
		t.Fatalf("提示词测试绕过个人预算: %s", blocked.describe())
	}
	h.setUserBudget(t, admin, u.ID, 100)
	unavailable := h.do(http.MethodPost, "/prompts/test", admin, map[string]any{"prompt_id": "probe-admission", "model": "not-configured"})
	if unavailable.status != 400 || !strings.Contains(unavailable.text(), "model not found") || len(h.upstreamCalls()) != 0 {
		t.Fatalf("不可用模型被伪装为执行成功: %s", unavailable.describe())
	}
	h.scriptStatus("prompt-probe", 403)
	r := h.do(http.MethodPost, "/prompts/test", admin, map[string]any{"prompt_id": "probe-admission"})
	if r.status != 403 || !strings.Contains(r.text(), "regression upstream status") || len(h.upstreamCalls()) != 1 {
		t.Fatalf("真实上游拒绝未传回: %s calls=%d", r.describe(), len(h.upstreamCalls()))
	}
	h.flushSpend()
	row := logDetail(t, h, admin, r.header("x-litellm-call-id"))
	if spend, _ := floatField(row, "spend"); spend != 0 {
		t.Fatalf("失败测试仍收费: %#v", row)
	}
	metadata, _ := row["metadata"].(map[string]any)
	if status, _ := floatField(metadata, "http_status"); status != 403 {
		t.Fatalf("失败日志没有保留真实状态: %#v", row)
	}
}

// TestPromptProbeStoredFormatsAndHistory 验证模板更新后的真实读回、模型覆盖和对话历史不被再解释。
// 参数t为上下文；使用两个本地部署与私有数据库，预期选中覆盖模型并按顺序发送保存模板和历史；没有付费外部依赖，最后删除模板。
func TestPromptProbeStoredFormatsAndHistory(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-old"), chatDeployment("prompt-selected"))
	admin := h.personalBillingSession()
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "probe-history", "model": "prompt-old", "content": "Original"})
	t.Cleanup(func() { h.ok(http.MethodDelete, "/prompts/probe-history", admin, nil) })
	h.ok(http.MethodPatch, "/prompts/probe-history", admin, map[string]any{"content": "Updated {{name}}", "config": map[string]any{"max_tokens": 12}})
	h.ok(http.MethodPost, "/prompts/test", admin, map[string]any{
		"prompt_id": "probe-history", "model": "prompt-selected", "prompt_variables": map[string]any{"name": "World"},
		"conversation_history": []any{map[string]any{"role": "assistant", "content": "Keep {{name}} literal"}, map[string]any{"role": "user", "content": "Follow up"}},
	})
	calls := h.upstreamCalls()
	if len(calls) != 1 || calls[0].Body["model"] != "prompt-selected" {
		t.Fatalf("没有按显式模型执行: %#v", calls)
	}
	messages, _ := calls[0].Body["messages"].([]any)
	if len(messages) != 3 || messages[0].(map[string]any)["content"] != "Updated World" || messages[1].(map[string]any)["content"] != "Keep {{name}} literal" || messages[2].(map[string]any)["content"] != "Follow up" {
		t.Fatalf("保存模板或历史丢失/被重解释: %#v", calls[0].Body)
	}
	if calls[0].Body["max_tokens"] != float64(12) && calls[0].Body["max_completion_tokens"] != float64(12) {
		t.Fatalf("保存的生成参数未发送: %#v", calls[0].Body)
	}
}

// TestPromptProbeStoreFailure 验证真实PostgreSQL查询故障不能降级为空模板或虚假成功。
// 参数t为上下文；私有schema视图仅令prompts读取报错，保留真实会话；期望500不泄露SQL且零上游，cleanup恢复表并删除模板/schema。
func TestPromptProbeStoreFailure(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-probe"))
	admin := h.personalBillingSession()
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "probe-store", "model": "prompt-probe", "content": "Hello"})
	catalogSQL(t, h, `CREATE FUNCTION probe_prompt_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-prompt-query-error'; END $$`)
	catalogSQL(t, h, `ALTER TABLE kv RENAME TO probe_prompt_offline_kv`)
	t.Cleanup(func() {
		catalogSQL(t, h, `DROP VIEW IF EXISTS kv`)
		catalogSQL(t, h, `ALTER TABLE probe_prompt_offline_kv RENAME TO kv`)
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS probe_prompt_read_failure()`)
		h.ok(http.MethodDelete, "/prompts/probe-store", admin, nil)
	})
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'prompts' THEN probe_prompt_read_failure() ELSE body END AS body, created_at FROM probe_prompt_offline_kv`)
	r := h.do(http.MethodPost, "/prompts/test", admin, map[string]any{"prompt_id": "probe-store"})
	if r.status != 500 || strings.Contains(r.text(), "private-prompt-query-error") || len(h.upstreamCalls()) != 0 {
		t.Fatalf("读库失败被掩盖或泄露SQL: %s", r.describe())
	}
}

// TestPromptProbeCancellation 验证浏览器取消上下文会终止真实上游HTTP请求，不产生伪成功或继续重试。
// 参数t为上下文；本地上游等待context取消，真实管理员和私有库保存模板；验证取消、零费日志与单次上游，cleanup关闭服务器并删schema。
func TestPromptProbeCancellation(t *testing.T) {
	started, stopped := make(chan struct{}, 1), make(chan struct{}, 1)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			stopped <- struct{}{}
		case <-time.After(4 * time.Second):
		}
	}))
	t.Cleanup(upstream.Close)
	dep := chatDeployment("prompt-cancel")
	dep.LiteLLMParams["api_base"] = upstream.URL
	h := newHarness(t, dep)
	admin := h.personalBillingSession()
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "probe-cancel", "model": "prompt-cancel", "content": "Hello"})
	t.Cleanup(func() { h.ok(http.MethodDelete, "/prompts/probe-cancel", admin, nil) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.server.URL+"/prompts/test", strings.NewReader(`{"prompt_id":"probe-cancel"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+admin)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan error, 1)
	go func() {
		response, err := h.server.Client().Do(req)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("提示词测试没有到达本地上游")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消请求意外返回成功: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("客户端取消未结束请求")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("取消没有传到上游")
	}
	rows := cancellationRows(t, h, admin, "prompt-cancel", 1)
	if calls.Load() != 1 || len(rows) != 1 {
		t.Fatalf("取消后仍重试或重复记账: calls=%d rows=%d", calls.Load(), len(rows))
	}
	if spend, _ := floatField(rows[0], "spend"); spend != 0 {
		t.Fatalf("没有响应的取消请求收费: %#v", rows[0])
	}
	detail := logDetail(t, h, admin, firstString(rows[0], "request_id", "id"))
	metadata, _ := detail["metadata"].(map[string]any)
	if status, _ := floatField(metadata, "http_status"); status != 499 || detail["status"] != "error" {
		t.Fatalf("取消日志未标明真实499失败: %#v", detail)
	}
}

// TestPromptProbeRejectsMissingPrompt 验证缺失模板无法仅凭ID获得成功；前置真实管理员和数据库，预期404及零上游调用，由harness清理schema。
func TestPromptProbeRejectsMissingPrompt(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-probe"))
	admin := h.personalBillingSession()
	r := h.do(http.MethodPost, "/prompts/test", admin, map[string]any{"prompt_id": "does-not-exist", "model": "prompt-probe"})
	if r.status != 404 || len(h.upstreamCalls()) != 0 {
		t.Fatalf("缺失提示词被伪装为测试成功: %s", r.describe())
	}
}

// TestPromptProbeAlwaysContactsUpstream 验证诊断测试不会用历史成功缓存掩盖当前供应商故障，也不会污染普通聊天缓存。
// 参数t为上下文；前置私有库、真实管理员与可切换403的本地上游，同一模板/输入反复执行；保留普通chat缓存语义并核对调用次数，结束删除模板和schema。
func TestPromptProbeAlwaysContactsUpstream(t *testing.T) {
	h := newHarness(t, chatDeployment("prompt-refresh"))
	admin := h.personalBillingSession()
	h.ok(http.MethodPost, "/prompts", admin, map[string]any{"prompt_id": "probe-refresh", "model": "prompt-refresh", "content": "Hello"})
	t.Cleanup(func() { h.ok(http.MethodDelete, "/prompts/probe-refresh", admin, nil) })
	body := map[string]any{"prompt_id": "probe-refresh"}
	h.ok(http.MethodPost, "/prompts/test", admin, body)
	h.scriptStatus("prompt-refresh", 403)
	failed := h.do(http.MethodPost, "/prompts/test", admin, body)
	if failed.status != 403 || len(h.upstreamCalls()) != 2 {
		t.Fatalf("相同输入的提示词测试复用了过期成功: calls=%d %s", len(h.upstreamCalls()), failed.describe())
	}
	// 精确复用渲染后的JSON，证明测试既不读普通缓存，也不把测试成功写成普通缓存。
	const chatBody = `{"messages":[{"role":"user","content":"Hello"}],"model":"prompt-refresh","stream":false}`
	ordinary := catalogRawRequest(t, h, http.MethodPost, "/v1/chat/completions", admin, chatBody)
	if ordinary.status != 403 || len(h.upstreamCalls()) != 3 {
		t.Fatalf("提示词测试成功污染了普通聊天缓存: calls=%d %s", len(h.upstreamCalls()), ordinary.describe())
	}
	h.scriptStatus("prompt-refresh", 0)
	ordinary = catalogRawRequest(t, h, http.MethodPost, "/v1/chat/completions", admin, chatBody)
	if ordinary.status != 200 || len(h.upstreamCalls()) != 4 {
		t.Fatalf("普通聊天恢复后未到达上游: calls=%d %s", len(h.upstreamCalls()), ordinary.describe())
	}
	cached := catalogRawRequest(t, h, http.MethodPost, "/v1/chat/completions", admin, chatBody)
	if cached.status != 200 || !isCacheHit(cached) || len(h.upstreamCalls()) != 4 {
		t.Fatalf("提示词专用缓存策略改变了普通聊天缓存: calls=%d %s", len(h.upstreamCalls()), cached.describe())
	}
	h.scriptStatus("prompt-refresh", 403)
	failed = h.do(http.MethodPost, "/prompts/test", admin, body)
	if failed.status != 403 || len(h.upstreamCalls()) != 5 {
		t.Fatalf("提示词测试错误读取普通聊天历史缓存: calls=%d %s", len(h.upstreamCalls()), failed.describe())
	}
	h.scriptStatus("prompt-refresh", 0)
	recovered := h.ok(http.MethodPost, "/prompts/test", admin, body)
	if isCacheHit(recovered) || len(h.upstreamCalls()) != 6 {
		t.Fatalf("恢复后的提示词测试没有重新读取上游: calls=%d %s", len(h.upstreamCalls()), recovered.describe())
	}
}
