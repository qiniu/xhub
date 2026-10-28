package regression

import (
	"reflect"
	"strings"
	"testing"
)

// TestLegacyInferenceBoundary 使用真实HTTP/PG验证旧执行别名不能写入通用配置并伪报成功。
// 前置独立schema与本地模型；检查权限、错误、存量配置、零上游副作用及正常对话，显式清理夹具并由harness清理schema。
func TestLegacyInferenceBoundary(t *testing.T) {
	h := newHarness(t, chatDeployment("legacy-boundary"))
	admin := h.personalBillingSession()
	owner := h.openScope(t, admin, "legacy-boundary")
	for _, kind := range []string{"rag", "ocr", "models"} {
		if err := h.store.PutKV(kind, "sentinel", `{"id":"sentinel","name":"must-remain","enabled":false}`); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/rag/ingest", "/rag/query", "/ocr", "/models/demo:generateContent", "/models/demo:streamGenerateContent", "/models/demo:countTokens", "/models/demo:version:generateContent"} {
		t.Run(path, func(t *testing.T) {
			before := map[string][]map[string]any{}
			for _, kind := range []string{"rag", "ocr", "models"} {
				rows, err := h.store.ListKV(kind)
				if err != nil {
					t.Fatal(err)
				}
				before[kind] = rows
			}
			body := map[string]any{"model": "legacy-boundary", "name": "not-a-job", "contents": []any{}, "private_fixture": "do-not-echo"}
			for _, actor := range []struct {
				token  string
				status int
			}{{"", 401}, {owner.session, 403}, {owner.key, 403}, {admin, 501}} {
				out := h.do("POST", path, actor.token, body)
				if out.status != actor.status {
					t.Errorf("旧入口状态错误 %s: %s", path, out.describe())
				}
				if actor.token == admin && (!strings.Contains(out.text(), "unsupported_operation") || strings.Contains(out.text(), "do-not-echo") || out.header("Cache-Control") != "no-store") {
					t.Errorf("旧入口不能回显请求或伪报执行: %s", out.describe())
				}
			}
			for _, kind := range []string{"rag", "ocr", "models"} {
				rows, err := h.store.ListKV(kind)
				if err != nil || !reflect.DeepEqual(rows, before[kind]) {
					t.Errorf("旧执行请求误写配置 kind=%s rows=%v err=%v", kind, rows, err)
				}
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("不支持的入口意外发送上游")
			}
		})
	}
	if out := h.do("POST", "/models/a/b:generateContent", admin, map[string]any{}); out.status != 404 {
		t.Fatal("未注册的多层路径不应被接管", out.describe())
	}
	// 旧管理入口的对象JSON与JSON-RPC校验仍先于执行边界。
	for _, invalid := range []any{[]any{}, "invalid-object", map[string]any{"jsonrpc": "2.0", "method": "x"}} {
		if out := h.do("POST", "/rag/query", admin, invalid); out.status != 400 {
			t.Fatal("管理JSON校验改变", out.describe())
		}
	}
	// 版本化RAG/OCR已有真实拒绝边界，保持LLM Key准入后的501，不改成管理权限。
	for _, path := range []string{"/v1/rag/ingest", "/v1/rag/query", "/v1/ocr"} {
		if out := h.do("POST", path, owner.key, map[string]any{"model": "legacy-boundary"}); out.status != 501 {
			t.Errorf("版本化拒绝边界改变: %s", out.describe())
		}
	}
	got := h.ok("POST", "/v1/chat/completions", owner.key, chatRequest("legacy-boundary", "Verify unsupported legacy aliases leave chat available."))
	h.flushSpend()
	cost, _ := floatField(logDetail(t, h, admin, got.header("x-litellm-call-id")), "spend")
	if len(h.upstreamCalls()) != 1 || cost <= 0 {
		t.Fatal("真实对话或费用异常")
	}
	blocked := h.openScope(t, admin, "legacy-zero")
	h.setUserBudget(t, admin, blocked.userID, 0)
	if out := h.do("POST", "/v1/chat/completions", blocked.key, chatRequest("legacy-boundary", "Verify zero budget remains blocked.")); out.status != 403 || len(h.upstreamCalls()) != 1 {
		t.Fatal("预算规则改变", out.describe())
	}
	for _, kind := range []string{"rag", "ocr", "models"} {
		rows, err := h.store.ListKV(kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if err := h.store.DeleteKV(kind, row["id"].(string)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
