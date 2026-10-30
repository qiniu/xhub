// 本文件验证 routing 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package routing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/config"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	_ "github.com/sunqirui1987/xhub/internal/provider/all"
	providerdeployment "github.com/sunqirui1987/xhub/internal/provider/deployment"
	"github.com/sunqirui1987/xhub/internal/router"
)

// DiagnosticDeployment 构造路由诊断测试的隔离部署。
// 参数 name/id/transport/disabled 为公开别名、稳定 ID、协议及停用标志；返回新配置。
// 故意使用带假密码及查询参数的地址检验日志投影，纯内存，不拨号或持久化。
func DiagnosticDeployment(name, id, transport string, disabled bool) config.ModelEntry {
	return config.ModelEntry{ModelName: name, LiteLLMParams: map[string]any{
		"model": "real", "api_key": "arbitrary-private-secret", "api_base": "https://user:private-password@upstream.example/v1?token=private-query",
		"custom_llm_provider": "custom",
	}, ModelInfo: map[string]any{"id": id, "transport": transport, "disabled": disabled}}
}

// TestRouteDiagnosisStages 验证正常、空目录、无效声明、暂停、冷却、零权重及组/通配符边界。
// 前置独立内存目录；验证分类、计数及无共享配置改写；日志恢复由 captureLog 清理，无外部数据。
// 参数 t 为 Go 测试上下文；返回值无，断言失败时终止当前测试。
func TestRouteDiagnosisStages(t *testing.T) {
	good := DiagnosticDeployment("gpt-5.6-sol", "live", "bypass_openai_chat", false)
	bad := DiagnosticDeployment("gpt-5.6-sol", "bad", "missing-transport", false)
	paused := DiagnosticDeployment("gpt-5.6-sol", "paused", "bypass_openai_chat", true)
	other := DiagnosticDeployment("other", "other", "bypass_openai_chat", true)
	wildcard := DiagnosticDeployment("gpt-*", "wild", "bypass_openai_chat", false)
	settings := router.BuiltinSettings().ForModel("gpt-5.6-sol")
	for _, tc := range []struct {
		name                                   string
		list                                   []config.ModelEntry
		state                                  router.State
		group                                  bool
		matched, compatible, available, status int
		kind, message                          string
	}{
		{name: "healthy", list: []config.ModelEntry{good, bad, other}, matched: 2, compatible: 1, available: 1},
		{name: "empty", status: 400, kind: "invalid_request", message: "model not found"},
		{name: "unrelated disabled", list: []config.ModelEntry{other}, status: 400, kind: "invalid_request", message: "model not found"},
		{name: "matching disabled", list: []config.ModelEntry{paused}, status: 400, kind: "model_disabled", message: "model is disabled"},
		{name: "invalid transport", list: []config.ModelEntry{bad}, matched: 1, status: 400, kind: "model_unavailable", message: "exists"},
		{name: "cooldown", list: []config.ModelEntry{good}, state: router.State{Cooldown: map[string]bool{router.CooldownID(good): true}}, matched: 1, compatible: 1, status: 503, kind: "model_unavailable", message: "cooling down"},
		{name: "zero weight stale positive", list: []config.ModelEntry{good}, state: router.State{Allocations: map[string]float64{"live": 0, "deleted": 100}}, matched: 1, compatible: 1, status: 503, kind: "model_unavailable", message: "no allocated traffic"},
		{name: "wildcard", list: []config.ModelEntry{wildcard}, matched: 1, compatible: 1, available: 1},
		{name: "group", list: []config.ModelEntry{good}, group: true, matched: 1, compatible: 1, available: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := settings
			alias := "gpt-5.6-sol"
			if tc.group {
				alias = "group"
				s.RoutingGroups = []router.Group{{Name: alias, Models: []string{good.ModelName}}}
			}
			if tc.state.Allocations != nil {
				s.Policy.Allocations = []router.Allocation{{DeploymentID: "live", Weight: 0}, {DeploymentID: "deleted", Weight: 100}}
			}
			active, _ := DropDisabled(tc.list)
			eligible, _ := providerdeployment.Candidates(active, "chat", nil)
			d := DiagnoseRoute(tc.list, eligible, alias, "chat", s, tc.state, nil, false)
			snapshot := d.Snapshot(s, "live")
			raw, err := json.Marshal(snapshot)
			if err != nil || snapshot["compatible"] != tc.compatible || snapshot["available"] != tc.available || strings.Contains(string(raw), "private") {
				t.Fatalf("路由持久化诊断错误或泄漏: %s %v", raw, err)
			}
			if tc.name == "cooldown" && snapshot["required_deployment_reason"] != "deployment is cooling down" {
				t.Fatalf("续接冷却原因不明确: %v", snapshot)
			}
			if d.matched != tc.matched || d.compatible != tc.compatible || d.available != tc.available {
				t.Fatalf("阶段计数错误: %+v", d)
			}
			if tc.status != 0 {
				status, kind, message := d.Failure(alias)
				if status != tc.status || kind != tc.kind || !strings.Contains(message, tc.message) {
					t.Fatalf("空路由分类错误: %d %s %s", status, kind, message)
				}
			}
			if tc.name == "zero weight stale positive" && len(d.stale) != 1 {
				t.Fatalf("没有识别过期部署权重: %+v", d.stale)
			}
			if !paused.Disabled() || wildcard.ParamString("model", "") != "real" {
				t.Fatal("诊断修改了原目录")
			}
		})
	}
}

// TestRouteDiagnosisRequestCompatibility 验证对话能力排除不会冒充模型缺失；前置严格工具请求。
// 参数 t 为测试上下文，结果必须保留排除理由，所有对象均在内存中，无数据清理需求。
func TestRouteDiagnosisRequestCompatibility(t *testing.T) {
	dep := DiagnosticDeployment("chat", "anthropic", "bypass_anthropic_messages", false)
	dialogue, err := llmdialogue.ParseDialogue("openai-chat", map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "f", "strict": true, "parameters": map[string]any{"type": "object"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	eligible, _ := providerdeployment.Candidates([]config.ModelEntry{dep}, "chat", &dialogue)
	d := DiagnoseRoute([]config.ModelEntry{dep}, eligible, "chat", "chat", router.BuiltinSettings().ForModel("chat"), router.State{}, &dialogue, true)
	if d.compatible != 0 || len(d.decisions) != 1 || !strings.Contains(d.decisions[0].Reason, "strict") {
		t.Fatalf("严格工具不兼容未解释: %+v", d)
	}
}
