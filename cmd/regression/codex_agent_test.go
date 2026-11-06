package regression

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestCodexAgentConversation 验证 Codex 客户端通过个人密钥进行三轮增量会话。
// 参数 t 为测试上下文；分别走直接落库和专用 Redis 队列，核对三轮来源、历史及非法续接。
// 返回值为空，失败终止测试；夹具清理服务、私有 schema 和临时 Redis，不访问已有缓存。
func TestCodexAgentConversation(t *testing.T) {
	for _, mode := range []string{"direct", "redis"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "redis" {
				isolatedSpendRedis(t)
			} else {
				t.Setenv("XHUB_REGRESSION_REDIS_URL", "")
			}
			verifyCodexAgentConversation(t)
		})
	}
}

// verifyCodexAgentConversation 执行当前存储模式下的真实三轮 HTTP 会话与账单校验。
// 参数 t 为调用子测试；无返回值，失败立即报告；由上层选择 Redis，harness 清理数据库及 HTTP 服务。
func verifyCodexAgentConversation(t *testing.T) {
	t.Helper()
	dep := chatDeployment("codex-model")
	dep.ModelInfo["endpoint_types"] = []string{"chat", "responses"}
	dep.ModelInfo["id"] = "codex-agent-deployment"
	h := newHarness(t)
	// 默认夹具重复使用同一响应 ID；本例必须模拟真实供应商逐轮生成独立 ID，才能验证续接链。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.replies = append(h.replies, upstreamCall{Path: r.URL.Path, Body: body})
		turn := len(h.replies)
		h.mu.Unlock()
		answer := chatAnswer("codex-model", nil)
		answer["id"] = fmt.Sprintf("chatcmpl-codex-%d", turn)
		writeJSON(w, answer)
	}))
	t.Cleanup(upstream.Close)
	admin := h.adminSession()
	// 通过管理接口创建带独立供应商的数据库部署，保留真实持久化与端点契约。
	dep.LiteLLMParams["api_base"] = upstream.URL + "/v1"
	dep.LiteLLMParams["litellm_credential_name"] = "codex-upstream"
	dep.ModelInfo["pricing_source"] = "manual"
	h.ok("POST", "/credentials", admin, map[string]any{"credential_name": "codex-upstream", "credential_info": map[string]any{"custom_llm_provider": "openai"}, "credential_values": map[string]any{"api_base": upstream.URL + "/v1", "api_key": "sk-fake"}})
	h.ok("POST", "/model/new", admin, map[string]any{"model_name": dep.ModelName, "litellm_params": dep.LiteLLMParams, "model_info": dep.ModelInfo})
	owner := h.openScope(t, admin, "codex-agent")
	headers := map[string]string{"User-Agent": "codex_cli_rs/0.1.0 (xhub acceptance simulator)", "X-Session-Id": "codex-regression", "Originator": "codex_cli_rs"}
	previous := ""
	ids := map[string]bool{}
	prompts := []string{"Remember project-alpha", "Recall the project", "Confirm the project"}
	for index, prompt := range prompts {
		body := map[string]any{"model": "codex-model", "instructions": "You are Codex, a coding agent.", "input": []any{map[string]any{"role": "user", "content": prompt}}, "store": false, "stream": false, "max_output_tokens": 128}
		if previous != "" {
			body["previous_response_id"] = previous
		}
		reply := h.doHeaders(http.MethodPost, "/v1/responses", owner.key, body, headers)
		if reply.status != 200 {
			t.Fatalf("Codex 第 %d 轮失败：%s", index+1, reply.describe())
		}
		answer := reply.json()
		if reply.headers.Get("X-Session-Id") != "codex-regression" {
			t.Fatalf("Codex 第 %d 轮响应会话头错误：%s", index+1, reply.describe())
		}
		next := stringField(answer, "id")
		if next == "" || next == previous || answer["status"] != "completed" {
			t.Fatalf("Codex 响应契约错误：%v", answer)
		}
		calls := h.upstreamCalls()
		if len(calls) != index+1 {
			t.Fatalf("Codex 每轮应外发一次：%v", calls)
		}
		messages := calls[index].Body["messages"].([]any)
		if len(messages) != 2*(index+1) || messages[0].(map[string]any)["role"] != "system" {
			t.Fatalf("Codex 历史长度或指令丢失：%v", messages)
		}
		for j := 0; j <= index; j++ {
			if messages[j*2+1].(map[string]any)["content"] != prompts[j] {
				t.Fatalf("Codex 用户历史丢失：%v", messages)
			}
			if j < index && messages[j*2+2].(map[string]any)["content"] != defaultReply.Content {
				t.Fatalf("Codex 助手历史丢失：%v", messages)
			}
		}
		callID := reply.headers.Get("x-litellm-call-id")
		if callID == "" || ids[callID] {
			t.Fatal("Codex 三轮应有独立日志 ID")
		}
		ids[callID] = true
		wantSource := "explicit"
		if index > 0 {
			wantSource = "parent"
		}
		// 同步响应确认后直接持久化；仍核对 Redis 确实连接，且账单在刷写前已正确入库。
		if h.gw.Live != nil {
			event, err := h.db.GetUsageEvent(t.Context(), iam.UsageQuery{}, callID)
			if err != nil || event.SessionSource != wantSource {
				t.Fatalf("Codex 第%d轮同步账单未持久化或会话来源错误：预期=%s 事件=%+v 错误=%v", index+1, wantSource, event, err)
			}
		}
		h.flushSpend()
		bill := h.ok("GET", "/spend/logs/ui/"+callID, admin, nil).json()
		if bill["session_source"] != wantSource {
			t.Fatalf("Codex 第%d轮会话来源错误：预期=%s 实际=%v", index+1, wantSource, bill["session_source"])
		}
		for field, want := range map[string]string{"model": "codex-model", "session_id": "codex-regression", "api_key": owner.keyID, "user": owner.userID, "team_id": owner.teamID, "project_id": owner.projectID, "organization_id": owner.orgID} {
			if bill[field] != want {
				t.Fatalf("Codex 第 %d 轮归属 %s 错误：%v", index+1, field, bill)
			}
		}
		cost, _ := floatField(bill, "spend")
		// 接口金额已规范化；用夹具 11 个输入、5 个输出的精确十进制结果核对，不能比较结算内部的浮点尾数。
		const wantCost = 11*testInputRate + 5*testOutputRate
		if cost != wantCost {
			t.Fatalf("Codex 第 %d 轮金额错误：实际=%.18g 预期=%.18g", index+1, cost, float64(wantCost))
		}
		proxy := bill["proxy_server_request"].(map[string]any)
		storedBody := proxy["body"].(map[string]any)
		if len(storedBody["input"].([]any)) != 2*index+1 || storedBody["store"] != nil || storedBody["previous_response_id"] != nil {
			t.Fatalf("Codex 日志未恢复完整历史或泄漏代理字段：%v", proxy)
		}
		previous = next
	}
	stranger := h.openScope(t, admin, "codex-stranger")
	for _, token := range []string{owner.key, stranger.key} {
		prior := previous
		if token == owner.key {
			prior = "missing-response"
		}
		reply := h.doHeaders("POST", "/v1/responses", token, map[string]any{"model": "codex-model", "input": "next", "previous_response_id": prior}, headers)
		if reply.status != 400 || reply.json()["error"] == nil {
			t.Fatalf("Codex 非法或跨身份续接未拒绝：%s", reply.describe())
		}
	}
	if len(h.upstreamCalls()) != 3 {
		t.Fatal("Codex 拒绝请求不应外发")
	}
	successes := 0
	for _, row := range h.spendLogs(t, admin) {
		if row["status"] == "success" {
			successes++
		} else if cost, _ := floatField(row, "spend"); cost != 0 {
			t.Fatal("Codex 拒绝请求不应记费")
		}
	}
	if successes != 3 {
		t.Fatalf("Codex 成功账单应恰好三笔：%d", successes)
	}
}
