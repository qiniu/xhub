package regression

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/contracts/keys"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// keyContractClient 仅为测试把真实HTTP回复送入独立OpenAPI检查；持有隔离网关与数据库，不模拟授权/持久化。
type keyContractClient struct {
	t     *testing.T
	h     *harness
	v     *keys.Validator
	admin string
}

// newKeyContractClient 为t启动真实网关与私有PG schema并加载契约；失败终止，harness最终清理schema。
func newKeyContractClient(t *testing.T) *keyContractClient {
	t.Helper()
	h := newHarness(t, chatDeployment("key-contract-chat"))
	v, err := keys.NewValidator(keys.Document)
	if err != nil {
		t.Fatal(err)
	}
	return &keyContractClient{t: t, h: h, v: v, admin: h.adminSession()}
}

// call 向真实网关提交method/path/token/body，验证期望状态want和回复契约；valid决定是否验证受支持输入，非法输入也必须检查实际失败结果。
// 返回reply供持久化/业务断言；不自动把200当成功，不修改请求，不跳过服务器鉴权。
func (c *keyContractClient) call(method, path, token string, body any, want int, valid bool) reply {
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

// TestKeyContractLifecycle 覆盖八个真实HTTP操作及持久化/推理：不回显密钥、单条别名、分页、补丁、禁用恢复、消费和删除撤销。
// 前置私有PG和受控上游；显式删除Key/预占，harness兜底回收测试schema，无线上依赖。
func TestKeyContractLifecycle(t *testing.T) {
	c := newKeyContractClient(t)
	call := func(m, p string, b any, w int) reply { t.Helper(); return c.call(m, p, c.admin, b, w, true) }
	created := call("POST", "/key/generate", map[string]any{"team_id": nil, "key_alias": "保留原始名称", "max_budget": "10", "duration": "1h", "models": []string{"key-contract-chat"}}, 200).json()
	id, token := created["token_id"].(string), created["key"].(string)
	raw, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	var typed keys.CreatedKey
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created, restored) {
		t.Fatalf("生成类型丢字段: %s", encoded)
	}
	read := func() map[string]any {
		return call("GET", "/key/info?key="+id, nil, 200).json()["info"].(map[string]any)
	}
	a := read()
	b := call("POST", "/v2/key/info", map[string]any{"key": id}, 200).json()["info"]
	if !reflect.DeepEqual(a, b) {
		t.Fatal("详情别名不一致")
	}
	if a["key"] != nil || a["token"] == token || a["token"] != created["key_prefix"].(string)+"..." {
		t.Fatal("详情泄露明文或遮罩错误")
	}
	list := call("GET", "/key/list?scope=personal&size=1&page=1", nil, 200).json()
	rows := list["keys"].([]any)
	if list["total_count"] != float64(1) || len(rows) != 1 || rows[0].(map[string]any)["token"] != id {
		t.Fatal("个人列表ID/分页错误")
	}
	ordinary := call("GET", "/key/list?key_hash="+id, nil, 200).json()["keys"].([]any)
	if len(ordinary) != 1 || ordinary[0].(map[string]any)["token"] != a["token"] {
		t.Fatal("普通列表遮罩错误")
	}
	empty := call("GET", "/key/list?search=not-present", nil, 200).json()
	if empty["total_count"] != float64(0) || empty["total_pages"] != float64(1) {
		t.Fatal("空分页错误")
	}
	call("POST", "/key/update", map[string]any{"key": id, "key_alias": "Renamed"}, 200)
	got := read()
	if got["max_budget"] != float64(10) || got["expires"] != a["expires"] || !reflect.DeepEqual(got["models"], a["models"]) {
		t.Fatal("省略字段覆盖旧配置")
	}
	for _, budget := range []any{nil, "", 0, "10"} {
		out := call("POST", "/key/update", map[string]any{"key": id, "max_budget": budget}, 200).json()
		var want any
		if budget == 0 {
			want = float64(0)
		}
		if budget == "10" {
			want = float64(10)
		}
		if out["max_budget"] != want || read()["max_budget"] != want {
			t.Fatalf("预算语义错误 input=%v", budget)
		}
	}
	// 未发送上游的已知预占允许主动释放；详情必须呈现真实占用而非固定零。
	if _, err := c.h.db.ReserveBudget(t.Context(), "key-contract-hold", "key", id, 0.25); err != nil {
		t.Fatal(err)
	}
	pending := read()
	if pending["budget_pending"] != 0.25 || len(pending["budget_reservations"].([]any)) != 1 {
		t.Fatal("预占详情未反映真实存储")
	}
	if err := c.h.db.ReleaseBudget(t.Context(), "key-contract-hold"); err != nil {
		t.Fatal(err)
	}
	chat := chatRequest("key-contract-chat", "Verify key contract lifecycle.")
	call("POST", "/key/block", map[string]any{"key": id}, 200)
	if read()["blocked"] != true {
		t.Fatal("禁用未持久化")
	}
	mark := len(c.h.upstreamCalls())
	if r := c.h.do("POST", "/v1/chat/completions", token, chat); r.status != 401 || len(c.h.upstreamCalls()) != mark {
		t.Fatalf("禁用仍能推理: %s", r.describe())
	}
	call("POST", "/key/unblock", map[string]any{"key": id}, 200)
	if read()["blocked"] != false {
		t.Fatal("恢复未持久化")
	}
	if r := c.h.ok("POST", "/v1/chat/completions", token, chat); !strings.Contains(r.text(), "regression-ok") {
		t.Fatal("未调用真实测试上游")
	}
	c.h.flushSpend()
	if read()["spend"].(float64) <= 0 {
		t.Fatal("成功推理未计费")
	}
	call("POST", "/key/delete", map[string]any{"keys": []string{id}}, 200)
	call("GET", "/key/info?key="+id, nil, 404)
	call("POST", "/key/delete", map[string]any{"key": id}, 404)
	if r := c.h.do("POST", "/v1/chat/completions", token, chat); r.status != 401 {
		t.Fatalf("删除后仍能推理: %s", r.describe())
	}
}

// TestKeyContractPermissions 验证未登录、普通成员跨用户读写隔离、管理Key能力拒绝与个人列表；所有操作走真实授权和PG。
// 测试先撤销夹具组织管理员，禁止用特权用户伪装成员通过；harness清理租户及Key。
func TestKeyContractPermissions(t *testing.T) {
	c := newKeyContractClient(t)
	a := c.h.provision(t, c.admin, "contract-key-a")
	b := c.h.provision(t, c.admin, "contract-key-b")
	if err := c.h.db.SetOrgAdmin(t.Context(), iam.Actor{Kind: "master"}, a.orgID, a.userID, false); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ m, p string }{{"POST", "/key/generate"}, {"GET", "/key/list"}, {"GET", "/key/info"}, {"POST", "/v2/key/info"}, {"POST", "/key/update"}, {"POST", "/key/block"}, {"POST", "/key/unblock"}, {"POST", "/key/delete"}} {
		c.call(op.m, op.p, "", nil, 401, false)
	}
	c.call("GET", "/key/info?scope=personal&key="+b.key, a.session, nil, 404, true)
	for _, path := range []string{"/key/update", "/key/block", "/key/unblock"} {
		c.call("POST", path, a.session, map[string]any{"key": b.key}, 404, true)
	}
	c.call("POST", "/key/delete", a.session, map[string]any{"keys": []string{a.key, b.key}}, 404, true)
	c.call("GET", "/key/info?key="+a.key, a.session, nil, 200, true)
	rows := c.call("GET", "/key/list?scope=personal&user_id="+b.userID, a.session, nil, 200, true).json()["keys"].([]any)
	if len(rows) != 0 {
		t.Fatal("筛选扩大个人范围")
	}
	c.call("POST", "/key/generate", a.key, map[string]any{}, 403, true)
	for _, body := range []map[string]any{{"auto_rotate": true}, {"key_type": "management"}, {"max_budget": -1}, {"rpm_limit": 1.5}} {
		c.call("POST", "/key/generate", a.session, body, 400, false)
	}
	// 未使用的旧前端批量查询适配器不能作为接口支持证据，真实后台应明确400。
	c.call("POST", "/v2/key/info", a.session, map[string]any{"keys": []string{a.key}}, 400, false)
	for _, path := range []string{"/key/list?page=bad", "/key/list?size=101", "/key/list?sort_by=bad"} {
		c.call("GET", path, a.session, nil, 400, false)
	}
}

// TestKeyContractFailedWrites 对私有Key表注入SQL约束并发送畸形更新；验证错误不能假成功且名称/额度不变。
// 约束在Cleanup撤销，Key显式删除，harness最终清理schema；不影响线上。
func TestKeyContractFailedWrites(t *testing.T) {
	c := newKeyContractClient(t)
	created := c.call("POST", "/key/generate", c.admin, map[string]any{"key_alias": "Original", "max_budget": 10}, 200, true).json()
	id := created["token_id"].(string)
	catalogSQL(t, c.h, `ALTER TABLE api_keys ADD CONSTRAINT key_contract_reject CHECK (name <> 'reject-contract-write')`)
	t.Cleanup(func() { catalogSQL(t, c.h, `ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS key_contract_reject`) })
	c.call("POST", "/key/update", c.admin, map[string]any{"key": id, "key_alias": "reject-contract-write", "max_budget": 20}, 500, true)
	c.call("POST", "/key/generate", c.admin, map[string]any{"key_alias": "reject-contract-write"}, 500, true)
	for _, raw := range []string{`null`, `{} {}`, `{"key":"` + id + `","max_budget":1e309}`, `{"key":"` + id + `","max_budget":-1}`} {
		r := catalogRawRequest(t, c.h, "POST", "/key/update", c.admin, raw)
		if r.status != 400 {
			t.Fatalf("坏更新被接受: %s", r.describe())
		}
		if err := c.v.Response(httptest.NewRequest("POST", "/key/update", nil), r.status, r.headers, r.body); err != nil {
			t.Fatal(err)
		}
	}
	got := c.call("GET", "/key/info?key="+id, c.admin, nil, 200, true).json()["info"].(map[string]any)
	if got["key_alias"] != "Original" || got["max_budget"] != float64(10) {
		t.Fatal("失败更新污染原数据")
	}
	if rows := c.call("GET", "/key/list?search=reject-contract-write", c.admin, nil, 200, true).json()["keys"].([]any); len(rows) != 0 {
		t.Fatal("失败创建留下数据")
	}
	c.call("POST", "/key/delete", c.admin, map[string]any{"key": id}, 200, true)
}

// TestKeyContractInheritance 真实HTTP验证模板及模型继承、省略与清空、到期空值不撤销期限；不调整既有规则。
// 私有PG与本地上游，模板创建/绑定均为测试资源；显式解除绑定/删除，harness兜底清理。
func TestKeyContractInheritance(t *testing.T) {
	c := newKeyContractClient(t)
	owner := c.h.provision(t, c.admin, "key-inheritance")
	body := routeTemplateBody([]any{}, 1, 60, 0, 0)
	teamTemplate := routeTemplate(t, c.h, c.admin, "contract-team", body)
	ownTemplate := routeTemplate(t, c.h, c.admin, "contract-key", body)
	bindTemplate(t, c.h, c.admin, "team", owner.teamID, teamTemplate)
	created := c.call("POST", "/key/generate", c.admin, map[string]any{"user_id": owner.userID, "route_template_id": ownTemplate, "duration": "1h", "models": []string{"key-contract-chat"}}, 200, true).json()
	id, token := created["token_id"].(string), created["key"].(string)
	for _, patch := range []map[string]any{{"key": id, "key_alias": "Unrelated"}, {"key": id, "duration": nil}, {"key": id, "duration": ""}} {
		got := c.call("POST", "/key/update", c.admin, patch, 200, true).json()
		if got["route_template_id"] != ownTemplate || got["expires"] != created["expires"] {
			t.Fatal("无关编辑或空期限改变模板/到期")
		}
	}
	c.call("POST", "/key/update", c.admin, map[string]any{"key": id, "route_template_id": nil, "models": []string{"all-proxy-models"}}, 200, true)
	info := c.call("GET", "/key/info?key="+id, c.admin, nil, 200, true).json()["info"].(map[string]any)
	if info["route_template_id"] != nil || len(info["models"].([]any)) != 0 {
		t.Fatal("模板/模型未恢复继承")
	}
	if got := effectiveTemplate(t, c.h, c.admin, "key", id); got["template_id"] != teamTemplate || got["scope_type"] != "team" {
		t.Fatalf("未继承团队模板: %+v", got)
	}
	c.h.ok("POST", "/v1/chat/completions", token, chatRequest("key-contract-chat", "Verify inherited route."))
	c.call("POST", "/key/delete", c.admin, map[string]any{"key": id}, 200, true)
	bindTemplate(t, c.h, c.admin, "team", owner.teamID, "")
	for _, id := range []string{teamTemplate, ownTemplate} {
		c.h.ok("POST", "/route_template/"+id+"/delete", c.admin, map[string]any{})
	}
}
