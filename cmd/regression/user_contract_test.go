package regression

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/contracts/users"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// userContractClient 仅为测试把真实HTTP回复送入独立OpenAPI检查；持有隔离网关与数据库，不模拟授权/持久化。
type userContractClient struct {
	t     *testing.T
	h     *harness
	v     *users.Validator
	admin string
}

// newUserContractClient 为t启动真实网关与私有PG schema并加载契约；失败终止，harness最终清理schema。
func newUserContractClient(t *testing.T) *userContractClient {
	t.Helper()
	h := newHarness(t, chatDeployment("user-contract-chat"))
	v, err := users.NewValidator(users.Document)
	if err != nil {
		t.Fatal(err)
	}
	return &userContractClient{t: t, h: h, v: v, admin: h.adminSession()}
}

// call 向真实网关提交method/path/token/body，验证期望状态want和回复契约；valid决定是否验证受支持输入，非法输入也必须检查实际失败结果。
// 返回reply供持久化/业务断言；不自动把200当成功，不修改请求，不跳过服务器鉴权。
func (c *userContractClient) call(method, path, token string, body any, want int, valid bool) reply {
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

// TestUserContractLifecycle 走九个用户管理操作，验证分页/别名、创建不自动发Key、重复邮箱、单人和批量null/0语义及真实推理计费。
// 前置真实PG与受控HTTP上游；删除测试用户后旧会话和Key必须失效，harness回收其余隔离资源，不触及外部服务。
func TestUserContractLifecycle(t *testing.T) {
	c := newUserContractClient(t)
	call := func(method, path string, body any, want int) reply {
		t.Helper()
		return c.call(method, path, c.admin, body, want, true)
	}
	body := map[string]any{"user_email": "contract-user@example.invalid", "password": "Contract-password", "user_alias": "保留原始名字", "max_budget": "10", "budget_reset_day": 31}
	created := call("POST", "/user/new", body, 200).json()
	id := created["user_id"].(string)
	// 生成Go类型也必须能往返真实快照，不能因可空字段或生成器覆盖丢失响应字段。
	raw, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	var typed users.User
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
		t.Fatalf("生成用户类型丢失真实字段: original=%s encoded=%s", raw, encoded)
	}

	if created["user_alias"] != "保留原始名字" || created["max_budget"] != float64(10) || created["spend"] != float64(0) {
		t.Fatalf("创建结果错误: %+v", created)
	}
	count, err := c.h.db.Engine.Table("api_keys").Where("user_id = ?", id).Count()
	if err != nil || count != 0 {
		t.Fatalf("创建自动生成了Key count=%d err=%v", count, err)
	}
	call("POST", "/user/new", body, 409)
	a := call("GET", "/user/info?user_id="+id, nil, 200).json()
	b := call("GET", "/v2/user/info?user_id="+id, nil, 200).json()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("用户详情别名不同")
	}
	list := call("GET", "/user/list?search=contract-user&page_size=1&page=1", nil, 200).json()
	if list["total"] != float64(1) || list["total_count"] != list["total"] || list["current_page"] != list["page"] || list["page_size"] != list["size"] || len(list["users"].([]any)) != 1 {
		t.Fatalf("分页别名/过滤错误: %+v", list)
	}
	invalidPage := call("GET", "/user/list?page=bad&page_size=501", nil, 200).json()
	if invalidPage["page"] != float64(1) || invalidPage["page_size"] != float64(100) {
		t.Fatal("分页回退规则漂移")
	}
	for _, path := range []string{"/user/filter/ui", "/user/available_users"} {
		rows := call("GET", path+"?user_email=CONTRACT-USER", nil, 200).json()["users"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["user_id"] != id {
			t.Fatalf("选择器过滤错误: %+v", rows)
		}
	}
	session := c.h.login(body["user_email"].(string), body["password"].(string))
	key := firstString(c.h.ok("POST", "/key/generate", session, map[string]any{"key_alias": "contract-key"}).json(), "key")
	read := func() map[string]any {
		return call("GET", "/user/info?user_id="+id, nil, 200).json()["user_info"].(map[string]any)
	}
	call("POST", "/user/update", map[string]any{"user_id": id, "user_alias": "Updated"}, 200)
	if read()["max_budget"] != float64(10) {
		t.Fatal("省略预算改变了额度")
	}
	for _, budget := range []any{nil, "", 0, "10"} {
		got := call("POST", "/user/update", map[string]any{"user_id": id, "max_budget": budget}, 200).json()["user_info"].(map[string]any)
		want := any(nil)
		if budget == 0 {
			want = float64(0)
		}
		if budget == "10" {
			want = float64(10)
		}
		if got["max_budget"] != want || read()["max_budget"] != want {
			t.Fatalf("单人预算语义错误: input=%v response=%+v", budget, got)
		}
	}
	saved := call("POST", "/user/bulk_update", bulkBudgetRequest([]string{id}, 0), 200).json()
	if saved["successful_updates"] != float64(1) || saved["total_requested"] != float64(1) || len(saved["results"].([]any)) != 1 {
		t.Fatal("批次结果未与真实目标对应")
	}
	mark := len(c.h.upstreamCalls())
	chat := chatRequest("user-contract-chat", "Verify user budget contract.")
	if r := c.h.do("POST", "/v1/chat/completions", key, chat); r.status != 403 || len(c.h.upstreamCalls()) != mark {
		t.Fatalf("零预算未拦截数据面: %s", r.describe())
	}
	call("POST", "/user/bulk_update", bulkBudgetRequest([]string{id}, 10), 200)
	if r := c.h.ok("POST", "/v1/chat/completions", key, chat); !strings.Contains(r.text(), "regression-ok") {
		t.Fatalf("恢复后没有真实上游回答: %s", r.describe())
	}
	c.h.flushSpend()
	spent := read()["spend"].(float64)
	if spent <= 0 {
		t.Fatal("模型成功未产生用量")
	}
	call("POST", "/user/bulk_update", bulkBudgetRequest([]string{id, "missing-user"}, 4), 404)
	if read()["max_budget"] != float64(10) {
		t.Fatal("失败批次发生部分保存")
	}
	call("POST", "/user/update", map[string]any{"user_id": id, "max_budget": 0}, 400)
	call("POST", "/user/bulk_update", bulkBudgetRequest([]string{id}, nil), 200)
	if read()["max_budget"] != nil || read()["spend"] != spent {
		t.Fatal("取消上限重置历史消费")
	}
	call("POST", "/user/delete", map[string]any{"user_id": id}, 200)
	call("GET", "/user/info?user_id="+id, nil, 404)
	c.call("GET", "/user/info", session, nil, 401, false)
	if r := c.h.do("POST", "/v1/chat/completions", key, chat); r.status != 401 {
		t.Fatalf("删除用户后密钥仍有效: %s", r.describe())
	}
}

// TestUserContractPermissionsAndIdentity 验证成员隔离、组织/团队预算权限不扩大、SSO绑定及重复身份回滚；所有回复由契约验证。
// 前置隔离账户与本地企业配置，无真实IdP；schema清理覆盖账户、身份、团队、组织和会话。
func TestUserContractPermissionsAndIdentity(t *testing.T) {
	c := newUserContractClient(t)
	a := c.h.provision(t, c.admin, "contract-owner")
	b := c.h.provision(t, c.admin, "contract-other")
	// 通用租户夹具授予组织管理员，先撤销该测试身份，确保“普通成员”断言的前置条件真实。
	if err := c.h.db.SetOrgAdmin(t.Context(), iam.Actor{Kind: "master"}, a.orgID, a.userID, false); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ m, p string }{{"GET", "/user/list"}, {"GET", "/user/info"}, {"GET", "/v2/user/info"}, {"GET", "/user/filter/ui"}, {"GET", "/user/available_users"}, {"POST", "/user/new"}, {"POST", "/user/update"}, {"POST", "/user/bulk_update"}, {"POST", "/user/delete"}} {
		c.call(op.m, op.p, "", nil, 401, false)
	}
	own := c.call("GET", "/user/list", a.session, nil, 200, true).json()["users"].([]any)
	if len(own) != 1 || own[0].(map[string]any)["user_id"] != a.userID {
		t.Fatalf("普通成员列表范围错误: %+v", own)
	}
	c.call("GET", "/user/info?user_id="+b.userID, a.session, nil, 404, true)
	for _, p := range []string{"/user/filter/ui", "/user/available_users"} {
		c.call("GET", p, a.session, nil, 403, true)
	}
	for _, token := range []string{a.session, a.key} {
		c.call("POST", "/user/new", token, map[string]any{"user_email": "denied@example.invalid", "password": "Contract-password"}, 403, true)
		c.call("POST", "/user/bulk_update", token, bulkBudgetRequest([]string{a.userID}, 10), 403, true)
	}
	c.call("POST", "/user/update", a.session, map[string]any{"user_id": a.userID, "max_budget": 10}, 403, true)
	c.call("POST", "/user/delete", a.session, map[string]any{"user_id": a.userID}, 400, true)
	c.call("POST", "/user/update", a.session, map[string]any{"user_alias": "Personal edit", "user_role": "proxy_admin"}, 200, true)
	if got := c.call("GET", "/user/info", a.session, nil, 200, true).json()["user_info"].(map[string]any); got["user_role"] != "internal_user" || got["user_alias"] != "Personal edit" {
		t.Fatal("个人资料更新扩大了角色权限")
	}
	colleague := c.call("POST", "/user/new", c.admin, map[string]any{"user_email": "colleague@example.invalid", "password": "Contract-password", "team_id": a.teamID}, 200, true).json()["user_id"].(string)
	c.h.ok("POST", "/team/member_update", c.admin, map[string]any{"team_id": a.teamID, "user_id": a.userID, "role": "admin"})
	c.call("POST", "/user/update", a.session, map[string]any{"user_id": a.userID, "max_budget": 10}, 200, true)
	c.call("POST", "/user/update", a.session, map[string]any{"user_id": b.userID, "max_budget": 10}, 403, true)
	c.call("POST", "/user/update", a.session, map[string]any{"user_id": colleague, "max_budget": 12}, 200, true)
	c.call("POST", "/user/update", a.session, map[string]any{"user_id": colleague, "user_alias": "Forbidden profile"}, 404, true)
	// 受管团队列表可以包含管理员成员，但必须包含同事且不能泄露另一个组织的用户。
	rows := c.call("GET", "/user/list", a.session, nil, 200, true).json()["users"].([]any)
	found := false
	for _, row := range rows {
		id := row.(map[string]any)["user_id"]
		if id == b.userID {
			t.Fatal("团队管理员列表泄露其他组织用户")
		}
		if id == colleague {
			found = true
		}
	}
	if !found {
		t.Fatal("团队管理员列表缺少受管成员")
	}

	c.call("POST", "/user/bulk_update", a.session, bulkBudgetRequest([]string{a.userID}, 10), 403, true)
	if err := c.h.db.SetOrgAdmin(t.Context(), iam.Actor{Kind: "master"}, a.orgID, a.userID, true); err != nil {
		t.Fatal(err)
	}
	c.call("POST", "/user/bulk_update", a.session, bulkBudgetRequest([]string{a.userID}, 10), 403, true)
	// 撤销团队管理员后仍以组织管理员授权同组织预算，证明不是依靠团队角色误通过。
	c.h.ok("POST", "/team/member_update", c.admin, map[string]any{"team_id": a.teamID, "user_id": a.userID, "role": "user"})
	c.call("POST", "/user/update", a.session, map[string]any{"user_id": colleague, "max_budget": 13}, 200, true)
	c.call("POST", "/user/update", a.session, map[string]any{"user_id": b.userID, "max_budget": 13}, 403, true)
	config, err := c.h.db.CreateEnterpriseOIDC(t.Context(), iam.Actor{Kind: "master"}, a.orgID, iam.EnterpriseOIDC{Name: "Contract SSO", Issuer: "https://identity.example.test", ClientID: "contract", RedirectURI: "https://console.example.test/auth/enterprise/callback", ReturnURI: "https://console.example.test/ui/login", IdentityClaim: "uid", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	binding := map[string]any{"user_id": b.userID, "sso_connection_id": config.ConnectionID, "sso_external_id": "contract-employee"}
	c.call("POST", "/user/update", c.admin, binding, 200, true)
	bound := c.call("GET", "/v2/user/info?user_id="+b.userID, c.admin, nil, 200, true).json()["user_info"].(map[string]any)
	if bound["sso_external_id"] != "contract-employee" || bound["sso_connection_id"] != config.ConnectionID {
		t.Fatal("SSO字段未持久化")
	}
	c.call("POST", "/user/update", a.session, map[string]any{"sso_connection_id": config.ConnectionID, "sso_external_id": "self"}, 403, true)
	c.call("POST", "/user/new", c.admin, map[string]any{"user_email": "conflict@example.invalid", "password": "Contract-password", "sso_connection_id": config.ConnectionID, "sso_external_id": "contract-employee"}, 409, true)
	if list := c.call("GET", "/user/list?user_email=conflict@example.invalid", c.admin, nil, 200, true).json(); list["total"] != float64(0) {
		t.Fatal("身份冲突留下部分用户")
	}
}

// TestUserContractFailedWrites 对真实用户表注入写约束并提交非法更新/批量；验证500与400不能伪装成功，原预算/名称不变。
// 私有schema中设置约束并在Cleanup撤销；最终删除用户，harness回收整个测试数据库范围。
func TestUserContractFailedWrites(t *testing.T) {
	c := newUserContractClient(t)
	body := map[string]any{"user_email": "failure@example.invalid", "password": "Contract-password", "user_alias": "Original", "max_budget": 10}
	id := c.call("POST", "/user/new", c.admin, body, 200, true).json()["user_id"].(string)
	catalogSQL(t, c.h, `ALTER TABLE users ADD CONSTRAINT user_contract_reject CHECK (name <> 'reject-contract-write')`)
	t.Cleanup(func() { catalogSQL(t, c.h, `ALTER TABLE users DROP CONSTRAINT IF EXISTS user_contract_reject`) })
	c.call("POST", "/user/update", c.admin, map[string]any{"user_id": id, "user_alias": "reject-contract-write", "max_budget": 20}, 500, true)
	for _, body := range []map[string]any{{"user_id": id, "budget_reset_day": nil}, {"user_id": id, "max_budget": -1}, {"user_id": id, "max_budget": "bad"}} {
		c.call("POST", "/user/update", c.admin, body, 400, false)
	}
	c.call("POST", "/user/bulk_update", c.admin, bulkBudgetRequest([]string{id}, "20"), 400, false)
	for _, raw := range []string{`null`, `{} {}`, `{"user_id":"` + id + `","max_budget":1e309}`} {
		r := catalogRawRequest(t, c.h, "POST", "/user/update", c.admin, raw)
		req := httptest.NewRequest("POST", "/user/update", nil)
		if r.status != 400 {
			t.Fatalf("损坏更新被接受: %s", r.describe())
		}
		if err := c.v.Response(req, r.status, r.headers, r.body); err != nil {
			t.Fatal(err)
		}
	}
	got := c.call("GET", "/user/info?user_id="+id, c.admin, nil, 200, true).json()["user_info"].(map[string]any)
	if got["user_alias"] != "Original" || got["max_budget"] != float64(10) {
		t.Fatalf("失败操作污染账户: %+v", got)
	}
	c.call("POST", "/user/delete", c.admin, map[string]any{"user_id": id}, 200, true)
}
