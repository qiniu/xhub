package guard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunqirui1987/xhub/internal/config"
)

// TestGuardrailsUseOnlyCurrentNamespace 验证废弃单数空间不参与管理或默认执行。
// 前置隔离库中的负向规则，验证列表为空、查询和删除404、默认请求放行；当前空间规则仍拦截，schema 自动清理。
func TestGuardrailsUseOnlyCurrentNamespace(t *testing.T) {
	st := openGuardStore(t)
	h := &savedHost{store: st, allow: true}
	raw := `{"guardrail_id":"retired","guardrail_name":"same-name","litellm_params":{"default_on":true,"guardrail":"always_block"}}`
	if err := st.PutKV("guardrail", "retired", raw); err != nil {
		t.Fatal(err)
	}
	rows, err := storedRules(h)
	if err != nil || len(rows) != 0 {
		t.Fatalf("读取废弃空间: rows=%v err=%v", rows, err)
	}
	if blocked, reason := PreCall(h, map[string]any{"text": "hello"}); blocked {
		t.Fatalf("废弃规则被执行: %s", reason)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		w := httptest.NewRecorder()
		Manage(h, w, httptest.NewRequest(method, "/guardrails/retired", nil))
		if w.Code != 404 {
			t.Fatalf("%s 读取废弃规则: status=%d body=%s", method, w.Code, w.Body.String())
		}
	}
	putGuardrail(t, st, "current", map[string]any{"guardrail_id": "current", "guardrail_name": "same-name", "litellm_params": map[string]any{"default_on": true, "guardrail": "always_block"}})
	if blocked, _ := PreCall(h, map[string]any{"text": "hello"}); !blocked {
		t.Fatal("当前规则未执行")
	}
}

// TestManagementAndExecutionViewsRemainDistinct 固定配置视图与执行视图的既有差异，防止重构把继承值写回可编辑配置。
// 前置私有数据库和 YAML，先验证全局默认仅执行时继承，再制造跨来源重名确认执行失败关闭但管理仍可查看修正；schema 自动清理。
func TestManagementAndExecutionViewsRemainDistinct(t *testing.T) {
	st := openGuardStore(t)
	putGuardrail(t, st, "db-rule", localRule("rule", "block", true, 1))
	h := &configHost{savedHost: savedHost{store: st}, cfg: &config.Config{}}
	// 设置使用既有字段名，与管理 PATCH 内容分离。
	h.cfg.LiteLLMSettings = map[string]any{"skip_system_message_in_guardrail": true}
	editable, err := storedRules(h)
	if err != nil || len(editable) != 1 {
		t.Fatal("读取管理配置失败", editable, err)
	}
	if _, ok := editable[0]["litellm_params"].(map[string]any)["skip_system_message_in_guardrail"]; ok {
		t.Fatal("管理视图不应固化继承默认值")
	}
	executable := listGuardrails(h)
	if len(executable) != 1 || executable[0]["litellm_params"].(map[string]any)["skip_system_message_in_guardrail"] != true {
		t.Fatal("执行未继承原有全局默认", executable)
	}
	h.cfg.Guardrails = []map[string]any{localRule("rule", "redact", false, 2)}
	editable, err = storedRules(h)
	if err != nil || len(editable) != 2 {
		t.Fatal("管理视图应保留重名配置供修正", editable, err)
	}
	if blocked, _, _ := Evaluate(h, map[string]any{"text": "ordinary text"}); !blocked {
		t.Fatal("执行层遇到名称歧义必须失败关闭")
	}
}
