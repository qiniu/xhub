package regression

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/contracts/users"
)

// TestUserContractPersonalLogCapture 使用真实网关、本地上游和独立 PostgreSQL 验证个人开关、接口契约与正文生命周期。
// 覆盖本人及其他用户、关闭、全局优先、唯一正文存储、到期和账单保留；夹具自动回收测试资源。
func TestUserContractPersonalLogCapture(t *testing.T) {
	t.Setenv("XHUB_LOG_BODY_GLOBAL_RETENTION_DAYS", "45")
	h := openHarness(t, false)
	v, err := users.NewValidator(users.Document)
	if err != nil {
		t.Fatal(err)
	}
	// 使用相同请求和实际返回结果验证 OpenAPI；错误输入只验证响应，避免以契约代替真实处理器。
	settingsCall := func(method, token string, body any, valid bool) reply {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "/account/log-capture", bytes.NewReader(raw))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if valid {
			if err := v.Request(req); err != nil {
				t.Fatalf("设置请求契约错误: %v", err)
			}
		}
		result := h.do(method, "/account/log-capture", token, body)
		if err := v.Response(req, result.status, result.headers, result.body); err != nil {
			t.Fatalf("设置响应契约错误: %v", err)
		}
		return result
	}
	admin := h.adminSession()
	member := h.provision(t, admin, "capture-member")
	other := h.provision(t, admin, "capture-other")
	addPricedModel(t, h, admin, "capture-model", tokenRates(0.000002, 0.00001), "chat")
	settings := settingsCall(http.MethodGet, member.session, nil, true).json()
	if settings["enabled"] != false || settings["global_enabled"] != false || settings["retention_days"] != float64(30) {
		t.Fatalf("默认设置错误: %v", settings)
	}
	if got := settingsCall(http.MethodPut, member.session, map[string]any{"enabled": true, "user_id": other.userID}, false); got.status != 400 {
		t.Fatalf("接口允许指定其他用户: %s", got.describe())
	}
	if got := settingsCall(http.MethodPut, member.key, map[string]any{"enabled": true}, true); got.status != 403 {
		t.Fatalf("密钥能修改个人设置: %s", got.describe())
	}
	if got := settingsCall(http.MethodGet, "", nil, false); got.status != 401 {
		t.Fatalf("未登录读取设置: %s", got.describe())
	}
	if got := settingsCall(http.MethodPut, member.session, map[string]any{"enabled": true}, true); got.status != 200 {
		t.Fatalf("启用失败: %s", got.describe())
	}
	if got := h.ok(http.MethodGet, "/account/log-capture", other.session, nil).json(); got["enabled"] != false {
		t.Fatalf("个人设置串号: %v", got)
	}
	request := chatRequest("capture-model", "Synthetic message for capture testing.")
	response := h.ok(http.MethodPost, "/v1/chat/completions", member.key, request)
	h.flushSpend()
	id := response.header("x-litellm-call-id")
	detail := logDetail(t, h, member.session, id)
	if detail["body_capture_status"] != "saved" || !strings.Contains(string(mustJSON(detail["messages"])), "Synthetic message") || detail["response"] == nil {
		t.Fatalf("未显示已保存正文: %v", detail)
	}
	if got := h.do(http.MethodGet, "/spend/logs/ui/"+id, other.session, nil); got.status != 404 && got.status != 403 {
		t.Fatalf("其他用户可以读正文: %s", got.describe())
	}
	var stored struct {
		RequestBody  string `xorm:"'request_body'"`
		ProxyRequest string `xorm:"'proxy_request'"`
		ResponseBody string `xorm:"'response_body'"`
	}
	if ok, err := h.db.Engine.Table("request_logs").Where("request_id = ?", id).Get(&stored); err != nil || !ok || stored.RequestBody != "" || stored.ProxyRequest != "" || stored.ResponseBody != "" {
		t.Fatalf("账单日志重复保存正文: %+v err=%v", stored, err)
	}
	before := detail["spend"]
	if _, err := h.db.Engine.Exec(`UPDATE request_log_captures SET expires_at = ? WHERE request_id = ?`, time.Now().Add(-time.Minute), id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Engine.Exec(`UPDATE request_logs SET body_capture_expires_at = ? WHERE request_id = ?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
	expired := logDetail(t, h, member.session, id)
	if expired["body_capture_status"] != "expired" || expired["spend"] != before || strings.Contains(string(mustJSON(expired["messages"])), "Synthetic message") {
		t.Fatalf("过期后正文或费用不符合预期: %v", expired)
	}
	if n, err := h.db.PurgeExpiredLogCaptures(t.Context(), time.Now(), 100); err != nil || n != 1 {
		t.Fatalf("到期清理失败: %d %v", n, err)
	}
	h.ok(http.MethodPut, "/account/log-capture", member.session, map[string]any{"enabled": false})
	response = h.ok(http.MethodPost, "/v1/chat/completions", member.key, request)
	h.flushSpend()
	if got := logDetail(t, h, member.session, response.header("x-litellm-call-id")); got["body_capture_status"] != "disabled" || strings.Contains(string(mustJSON(got["messages"])), "Synthetic message") {
		t.Fatalf("个人关闭后仍记录正文: %v", got)
	}
	h.ok(http.MethodPost, "/config/update", admin, map[string]any{"general_settings": map[string]any{"store_prompts_in_spend_logs": true}})
	if got := h.ok(http.MethodGet, "/account/log-capture", member.session, nil).json(); got["global_enabled"] != true || got["effective_enabled"] != true || got["enabled"] != false || got["global_retention_days"] != float64(45) {
		t.Fatalf("管理员全局优先状态错误: %v", got)
	}
	response = h.ok(http.MethodPost, "/v1/chat/completions", member.key, request)
	h.flushSpend()
	if got := logDetail(t, h, member.session, response.header("x-litellm-call-id")); got["body_capture_status"] != "saved" || got["messages"] == nil {
		t.Fatalf("全局开启时没有记录: %v", got)
	}
	globalCapture, err := h.db.GetLogCapture(t.Context(), response.header("x-litellm-call-id"), time.Now())
	if err != nil || globalCapture.Source != "global" || globalCapture.ExpiresAt.Sub(globalCapture.CreatedAt) != 45*24*time.Hour {
		t.Fatalf("全局正文来源或期限错误: %+v %v", globalCapture, err)
	}
}

// TestUserContractPersonalLogCaptureFailure 验证正文保存失败不影响真实对话和用量，日志明确说明失败。
// 使用私有 PostgreSQL 约束注入故障、本地上游和合成内容；测试结束自动删除私有 schema。
func TestUserContractPersonalLogCaptureFailure(t *testing.T) {
	h := openHarness(t, false)
	admin := h.adminSession()
	member := h.provision(t, admin, "capture-failure")
	addPricedModel(t, h, admin, "capture-failure-model", tokenRates(0.000002, 0.00001), "chat")
	h.ok(http.MethodPut, "/account/log-capture", member.session, map[string]any{"enabled": true})
	if _, err := h.db.Engine.Exec(`ALTER TABLE request_log_captures ADD CONSTRAINT reject_synthetic_capture CHECK (request_document NOT LIKE '%Save failure marker%')`); err != nil {
		t.Fatal(err)
	}
	response := h.ok(http.MethodPost, "/v1/chat/completions", member.key, chatRequest("capture-failure-model", "Save failure marker"))
	h.flushSpend()
	detail := logDetail(t, h, member.session, response.header("x-litellm-call-id"))
	if detail["body_capture_status"] != "storage_failed" || detail["status"] != "success" || detail["spend"].(float64) <= 0 {
		t.Fatalf("正文失败影响对话或账单: %v", detail)
	}
}

// TestUserContractPersonalLogCaptureInFlight 用本地上游暂停真实请求，在请求中切换个人开关。
// 验证请求按开始时的选择记录，之后的新请求使用新状态；私有 schema 和本地服务由夹具清理。
func TestUserContractPersonalLogCaptureInFlight(t *testing.T) {
	h := openHarness(t, false)
	admin := h.adminSession()
	member := h.provision(t, admin, "capture-inflight")
	addPricedModel(t, h, admin, "capture-inflight-model", tokenRates(0.000002, 0.00001), "chat")
	for _, tc := range []struct {
		initial, next bool
		want          string
	}{{false, true, "disabled"}, {true, false, "saved"}} {
		h.ok(http.MethodPut, "/account/log-capture", member.session, map[string]any{"enabled": tc.initial})
		entered := make(chan struct{}, 1)
		h.onUpstream("capture-inflight-model", func() { entered <- struct{}{} })
		release := h.holdUpstream("capture-inflight-model")
		t.Cleanup(release)
		result := make(chan reply, 1)
		go func() {
			result <- h.do(http.MethodPost, "/v1/chat/completions", member.key, chatRequest("capture-inflight-model", fmt.Sprintf("Synthetic switch %v.", tc.initial)))
		}()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("上游未接收到暂停请求")
		}
		h.ok(http.MethodPut, "/account/log-capture", member.session, map[string]any{"enabled": tc.next})
		release()
		var response reply
		select {
		case response = <-result:
		case <-time.After(10 * time.Second):
			t.Fatal("请求未完成")
		}
		if response.status != 200 {
			t.Fatalf("请求失败: %s", response.describe())
		}
		h.flushSpend()
		if got := logDetail(t, h, member.session, response.header("x-litellm-call-id")); got["body_capture_status"] != tc.want {
			t.Fatalf("在途请求未冻结开关: initial=%t next=%t result=%v", tc.initial, tc.next, got)
		}
	}
}
