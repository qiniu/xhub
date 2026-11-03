package regression

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"

	"testing"

	"github.com/sunqirui1987/xhub/contracts/models"
)

// modelContractClient 仅为测试把真实HTTP回复送入独立OpenAPI检查；持有隔离网关与数据库，不模拟授权/持久化。
type modelContractClient struct {
	t     *testing.T
	h     *harness
	v     *models.Validator
	admin string
}

// newModelContractClient 为t启动真实网关与私有PG schema并加载契约；失败终止，harness最终清理schema。
func newModelContractClient(t *testing.T) *modelContractClient {
	t.Helper()
	h := newHarness(t, chatDeployment("model-contract-yaml"))
	v, err := models.NewValidator(models.Document)
	if err != nil {
		t.Fatal(err)
	}
	return &modelContractClient{t: t, h: h, v: v, admin: h.adminSession()}
}

// call 向真实网关提交method/path/token/body，验证期望状态want和回复契约；valid决定是否验证受支持输入，非法输入也必须检查实际失败结果。
// 返回reply供持久化/业务断言；不自动把200当成功，不修改请求，不跳过服务器鉴权。
func (c *modelContractClient) call(method, path, token string, body any, want int, valid bool) reply {
	c.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if valid {
		if err := c.v.Request(req); err != nil {
			c.t.Fatalf("合法请求违反契约 %s %s: %v", method, path, err)
		}
	}
	r := c.h.do(method, path, token, body)
	if r.status != want {
		c.t.Fatalf("%s %s want=%d: %s", method, path, want, r.describe())
	}
	if err := c.v.Response(req, r.status, r.headers, r.body); err != nil {
		c.t.Fatalf("真实回复违反契约 %s %s: %v; %s", method, path, err, r.describe())
	}
	return r
}

// deploymentBody 构造测试部署声明，凭据和上游均属于h的私有环境；无写入，调用者通过真实HTTP创建并负责清理。
func (c *modelContractClient) deploymentBody(name string) map[string]any {
	return map[string]any{"model_name": name, "litellm_params": map[string]any{"model": name, "api_base": c.h.credentialAPIBase(), "api_key": "fixture-model-secret", "custom_llm_provider": "openai", "input_cost_per_token": "0.000001", "output_cost_per_token": 0.000002}, "model_info": map[string]any{"transport": "bypass_openai_chat", "endpoint_types": []string{"chat", "responses"}, "pricing_source": "manual"}}
}

// TestModelContractLifecycle 验证九个操作的实际HTTP、PG持久化、扩展字段、遮罩补丁、启停及真实调用计费。
// 前置私有schema与受控上游；显式删除部署，harness兜底清理所有租户和账务，清理失败不算通过。
func TestModelContractLifecycle(t *testing.T) {
	c := newModelContractClient(t)
	call := func(m, p string, b any, w int) reply { return c.call(m, p, c.admin, b, w, true) }
	created := call("POST", "/model/new", c.deploymentBody("contract-deployment"), 200).json()
	id := created["model_info"].(map[string]any)["id"].(string)
	if created["litellm_params"].(map[string]any)["api_key"] != "*****" {
		t.Fatal("创建泄漏密钥")
	}
	read := func() map[string]any {
		rows := call("GET", "/v2/model/info?modelId="+id, nil, 200).json()["data"].([]any)
		if len(rows) != 1 {
			t.Fatalf("部署回读数量=%d", len(rows))
		}
		return rows[0].(map[string]any)
	}
	if read()["db_model"] != true {
		t.Fatal("数据库部署标记缺失")
	}
	// 两种补丁路由保留原始端点与密钥；未知供应商扩展需在真实库回读，不能仅测试生成类型。
	call("POST", "/model/update", map[string]any{"model_info": map[string]any{"id": id, "extension": "原样保留"}, "litellm_params": map[string]any{"api_key": "*****"}}, 200)
	call("PATCH", "/model/"+id+"/update", map[string]any{"model_info": map[string]any{"max_output_tokens": 512}}, 200)
	row := read()
	if row["model_info"].(map[string]any)["extension"] != "原样保留" {
		t.Fatal("补丁丢扩展字段")
	}
	stored, err := c.h.store.ListProxyModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Params["api_key"] != "fixture-model-secret" {
		t.Fatal("遮罩补丁覆盖数据库密钥")
	}
	group := call("GET", "/model/groups?search=contract-deployment&size=1", nil, 200).json()
	if group["total_count"] != float64(1) || len(group["data"].([]any)) != 1 {
		t.Fatal("分组计数错误")
	}
	empty := call("GET", "/v2/model/info?modelId=missing", nil, 200).json()
	if len(empty["data"].([]any)) != 0 {
		t.Fatal("未知部署未返回空列表")
	}
	page := call("GET", "/model/groups?size=999&page=999", nil, 200).json()
	if len(page["data"].([]any)) != 0 {
		t.Fatal("越界页不为空")
	}
	scope := c.h.openScope(t, c.admin, "model-contract-user")
	request := map[string]any{"model": "contract-deployment", "messages": []any{map[string]any{"role": "user", "content": "Reply with a short greeting."}}}
	c.h.ok("POST", "/v1/chat/completions", scope.key, request)
	c.h.flushSpend()
	calls := len(c.h.upstreamCalls())
	if calls != 1 {
		t.Fatalf("真实调用次数=%d", calls)
	}
	probe := map[string]any{"model_info": map[string]any{"id": id}, "mode": "chat", "litellm_params": map[string]any{"api_base": "http://127.0.0.1:1", "api_key": "must-not-be-used"}}
	if call("POST", "/health/test_connection", probe, 200).json()["healthy"] != true {
		t.Fatal("保存凭据未被探测使用")
	}
	if len(c.h.upstreamCalls()) != calls+1 {
		t.Fatal("探测未实际出站")
	}
	call("POST", "/model/disable", map[string]any{"id": id}, 200)
	if read()["model_info"].(map[string]any)["disabled"] != true {
		t.Fatal("停用未持久化")
	}
	before := len(c.h.upstreamCalls())
	denied := c.h.do("POST", "/v1/chat/completions", scope.key, request)
	if denied.status < 400 || len(c.h.upstreamCalls()) != before {
		t.Fatalf("停用部署仍可调用: %s", denied.describe())
	}
	call("POST", "/model/enable", map[string]any{"id": id}, 200)
	request["messages"] = []any{map[string]any{"role": "user", "content": "Reply with a different greeting after enable."}}
	c.h.ok("POST", "/v1/chat/completions", scope.key, request)
	c.h.flushSpend()
	if len(c.h.upstreamCalls()) != before+1 {
		t.Fatal("启用未恢复真实调用")
	}
	c.h.scriptStatus("contract-deployment", 403)
	bad := call("POST", "/health/test_connection", probe, 200).json()
	if bad["healthy"] != false || bad["status"] != "error" || bad["error"] != "upstream_http_error" {
		t.Fatalf("上游403被当成成功: %v", bad)
	}
	c.h.scriptStatus("contract-deployment", 0)
	c.h.scriptBody("contract-deployment", []byte(`{"status":"ok"}`))
	mismatch := call("POST", "/health/test_connection", probe, 200).json()
	if mismatch["healthy"] != false || mismatch["error"] != "protocol_mismatch" {
		t.Fatalf("错误协议被当成功: %v", mismatch)
	}
	c.h.scriptBody("contract-deployment", nil)
	before = len(c.h.upstreamCalls())
	probe["mode"] = "image_generation"
	unsupported := call("POST", "/health/test_connection", probe, 200).json()
	if unsupported["healthy"] != false || unsupported["error"] != "unsupported_mode" || len(c.h.upstreamCalls()) != before {
		t.Fatal("不支持模式仍出站或报成功")
	}
	if spend := c.h.ok("GET", "/key/info?key="+scope.keyID, c.admin, nil).json()["info"].(map[string]any)["spend"].(float64); spend <= 0 {
		t.Fatal("真实推理未计费")
	}
	call("POST", "/model/delete", map[string]any{"id": id}, 200)
	if rows := call("GET", "/v2/model/info?modelId="+id, nil, 200).json()["data"].([]any); len(rows) != 0 {
		t.Fatal("删除后仍在目录")
	}
	stored, err = c.h.store.ListProxyModels()
	if err != nil || len(stored) != 0 {
		t.Fatalf("部署清理失败 %v", err)
	}
}

// TestModelContractPermissionsAndInvalid 使用真实会话/Key验证管理边界、YAML不可编辑、非法协议与缺参；失败不得写部署，最终schema清理。
func TestModelContractPermissionsAndInvalid(t *testing.T) {
	c := newModelContractClient(t)
	scope := c.h.openScope(t, c.admin, "model-contract-denied")
	ops := []struct{ method, path string }{{"GET", "/v2/model/info"}, {"GET", "/model/groups"}, {"POST", "/model/new"}, {"POST", "/model/update"}, {"PATCH", "/model/missing/update"}, {"POST", "/model/delete"}, {"POST", "/model/disable"}, {"POST", "/model/enable"}, {"POST", "/health/test_connection"}}
	for _, op := range ops {
		for _, auth := range []struct {
			token  string
			status int
		}{{"", 401}, {scope.key, 403}, {scope.session, 403}} {
			c.call(op.method, op.path, auth.token, nil, auth.status, false)
		}
	}
	for _, path := range []string{"/model/new", "/model/update", "/model/delete", "/model/disable", "/model/enable", "/health/test_connection"} {
		c.call("POST", path, c.admin, map[string]any{}, 400, false)
	}
	for _, path := range []string{"/model/delete", "/model/disable", "/model/enable"} {
		c.call("POST", path, c.admin, map[string]any{"id": "model-contract-yaml"}, 400, true)
	}
	c.call("POST", "/model/update", c.admin, map[string]any{"id": "model-contract-yaml"}, 400, true)
	body := c.deploymentBody("bad-protocol")
	body["model_info"].(map[string]any)["transport"] = "not-a-transport"
	c.call("POST", "/model/new", c.admin, body, 400, true)
	c.call("POST", "/health/test_connection", c.admin, map[string]any{"model_info": map[string]any{"id": "missing"}}, 404, true)
	rows, err := c.h.store.ListProxyModels()
	if err != nil || len(rows) != 0 {
		t.Fatalf("非法操作写入部署: %v", err)
	}
}

// TestModelContractStorageFailure 注入真实PG约束，证明失败创建/更新不污染内存和数据库；恢复约束后可删除，schema兜底清理。
func TestModelContractStorageFailure(t *testing.T) {
	c := newModelContractClient(t)
	catalogSQL(t, c.h, `ALTER TABLE proxy_models ADD CONSTRAINT model_contract_reject CHECK (model_name <> 'reject-contract-write')`)
	t.Cleanup(func() { catalogSQL(t, c.h, `ALTER TABLE proxy_models DROP CONSTRAINT IF EXISTS model_contract_reject`) })
	c.call("POST", "/model/new", c.admin, c.deploymentBody("reject-contract-write"), 500, true)
	created := c.call("POST", "/model/new", c.admin, c.deploymentBody("contract-original"), 200, true).json()
	id := created["model_info"].(map[string]any)["id"].(string)
	c.call("PATCH", "/model/"+id+"/update", c.admin, map[string]any{"model_name": "reject-contract-write"}, 500, true)
	rows := c.call("GET", "/v2/model/info?modelId="+id, c.admin, nil, 200, true).json()["data"].([]any)
	if rows[0].(map[string]any)["model_name"] != "contract-original" {
		t.Fatal("失败补丁污染内存目录")
	}
	stored, err := c.h.store.ListProxyModels()
	if err != nil || len(stored) != 1 || stored[0].ModelName != "contract-original" {
		t.Fatal("失败补丁污染数据库")
	}
	c.call("POST", "/model/delete", c.admin, map[string]any{"id": id}, 200, true)
}
