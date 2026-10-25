package regression

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSupplierServiceRuntimeRotation 验证独立管理服务保存的地址/秘密被真实执行链逐次读取，而非只验证 CRUD 返回成功。
// 前置隔离 PG、真实 HTTP 网关及两个严格校验凭据的本地供应商；涵盖普通用户调用、五字节掩码回传保留、合法单星号秘密轮换、401、恢复、用量及删除。
// 本地供应商不代替网关处理；显式删除模型/凭据，harness 清理用户和专属 schema，httptest 关闭监听。
func TestSupplierServiceRuntimeRotation(t *testing.T) {
	var hitsA, hitsB atomic.Int64
	// 新建不同地址和秘密的供应商替身；只模拟外部协议，收到错误秘密必须真实返回401。
	upstream := func(secret, content string, hits *atomic.Int64) *httptest.Server {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.URL.Path != "/v1/chat/completions" {
				t.Errorf("出站路径错误: %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+secret {
				w.WriteHeader(401)
				writeJSON(w, map[string]any{"error": map[string]any{"type": "authentication_error", "message": "fixture credential rejected"}})
				return
			}
			writeJSON(w, map[string]any{"id": "supplier-reply", "object": "chat.completion", "model": "supplier-runtime", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 8, "completion_tokens": 2, "total_tokens": 10}})
		}))
		t.Cleanup(up.Close)
		return up
	}
	a := upstream("abcde", "supplier-a", &hitsA)
	b := upstream("xy*z9", "supplier-b", &hitsB)
	c := newSupplierContractClient(t)
	h := c.h
	admin := c.admin
	owner := h.openScope(t, admin, "supplier-rotation")
	const name = "runtime-provider"
	c.call("POST", "/credentials", admin, map[string]any{"credential_name": name, "credential_info": map[string]any{"custom_llm_provider": "openai"}, "credential_values": map[string]any{"api_base": a.URL, "api_key": "abcde"}}, 200)
	created := h.ok("POST", "/model/new", admin, map[string]any{"model_name": "supplier-runtime", "litellm_params": map[string]any{"model": "supplier-runtime", "custom_llm_provider": "openai", "litellm_credential_name": name, "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}, "model_info": map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"chat"}, "pricing_source": "manual"}}).json()
	id := stringField(created["model_info"].(map[string]any), "id")
	// 调用真实数据面并验证独立供应商返回标记；每次提示不同，防止命中缓存掩盖配置读取。
	infer := func(prompt, marker string, status int) {
		t.Helper()
		r := h.do("POST", "/v1/chat/completions", owner.key, map[string]any{"model": "supplier-runtime", "messages": []any{map[string]any{"role": "user", "content": prompt}}})
		if r.status != status || !strings.Contains(r.text(), marker) {
			t.Fatalf("供应商执行结果不符 want=%d %s: %s", status, marker, r.describe())
		}
	}
	before := h.moneyOf(t, owner)
	infer("Verify initial provider.", "supplier-a", 200)
	read := c.call("GET", "/credentials/by_name/"+name, admin, nil, 200).json()
	mask := read["credential_values"].(map[string]any)["api_key"]
	c.call("PATCH", "/credentials/"+name, admin, map[string]any{"credential_values": map[string]any{"api_key": mask}, "note": "edited"}, 200)
	infer("Verify masked update preserves credentials.", "supplier-a", 200)
	c.call("PATCH", "/credentials/"+name, admin, map[string]any{"credential_values": map[string]any{"api_base": b.URL, "api_key": "xy*z9"}}, 200)
	infer("Verify replacement provider.", "supplier-b", 200)
	if hitsA.Load() != 2 || hitsB.Load() != 1 {
		t.Fatalf("新地址未立即生效 A=%d B=%d", hitsA.Load(), hitsB.Load())
	}
	c.call("PATCH", "/credentials/"+name, admin, map[string]any{"credential_values": map[string]any{"api_key": "fixture-invalid"}}, 200)
	infer("Verify invalid replacement fails.", "fixture credential rejected", 401)
	c.call("PATCH", "/credentials/"+name, admin, map[string]any{"credential_values": map[string]any{"api_key": "xy*z9"}}, 200)
	infer("Verify corrected credentials recover.", "supplier-b", 200)
	if hitsA.Load() != 2 || hitsB.Load() != 3 {
		t.Fatalf("凭据轮换未逐次执行 A=%d B=%d", hitsA.Load(), hitsB.Load())
	}
	if !h.moneyOf(t, owner).grewBy(before, 0.000048) {
		t.Fatal("四次成功真实用量结算不符，失败不应伪造费用")
	}
	h.ok("POST", "/model/delete", admin, map[string]any{"id": id})
	c.call("DELETE", "/credentials/"+name, admin, nil, 200)
	c.call("GET", "/credentials/by_name/"+name, admin, nil, 404)
}
