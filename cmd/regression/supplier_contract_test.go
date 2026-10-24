package regression

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/contracts/suppliers"
)

// supplierContractClient 将真实网关回复交给独立契约验证器；仅供隔离回归测试，无模拟业务处理器。
type supplierContractClient struct {
	t     *testing.T
	h     *harness
	v     *suppliers.Validator
	admin string
}

// newSupplierContractClient 为 t 建真实 HTTP/PG 环境并加载唯一契约；初始化失败终止测试，harness 自动清理私有 schema。
func newSupplierContractClient(t *testing.T) *supplierContractClient {
	t.Helper()
	h := newHarness(t)
	v, err := suppliers.NewValidator(suppliers.Document)
	if err != nil {
		t.Fatal(err)
	}
	return &supplierContractClient{t: t, h: h, v: v, admin: h.adminSession()}
}

// call 向真实网关发送 method/path/token/body，先验证合法输入、再核对状态 want 和响应契约；返回真实 reply 供业务断言。
// 仅用于合法 JSON 请求；错误状态也必须符合契约，但是否持久化由调用方另行回读核验，不修改鉴权或响应。
func (c *supplierContractClient) call(method, path, token string, body any, want int) reply {
	c.t.Helper()
	var raw []byte
	var err error
	if body != nil {
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
	if err := c.v.Request(req); err != nil {
		c.t.Fatalf("合法请求不符合契约 %s %s: %v", method, path, err)
	}
	r := c.h.do(method, path, token, body)
	c.check(req, r, want)
	return r
}

// check 检查实际 HTTP 状态及响应结构，错误带操作和完整测试响应定位；供 call 和畸形请求用例调用，不接受默认成功。
func (c *supplierContractClient) check(req *http.Request, r reply, want int) {
	c.t.Helper()
	if r.status != want {
		c.t.Fatalf("%s %s want=%d: %s", req.Method, req.URL.Path, want, r.describe())
	}
	if err := c.v.Response(req, r.status, r.headers, r.body); err != nil {
		c.t.Fatalf("真实响应违反契约 %s %s: %v; %s", req.Method, req.URL.Path, err, r.describe())
	}
}

// TestSupplierContractLifecycle 验证五个操作的真实持久化、脱敏、缺省/null/空值、生成名称、覆盖创建、upsert 与幂等删除。
// 前置私有 PG schema，无外部供应商；显式 HTTP 删除测试对象，harness 最终清理 schema，任何清理错误使测试失败。
func TestSupplierContractLifecycle(t *testing.T) {
	c := newSupplierContractClient(t)
	const name = "contract-provider"
	const path = "/credentials/" + name
	const detail = "/credentials/by_name/" + name
	call := func(method, path string, body any, status int) reply {
		return c.call(method, path, c.admin, body, status)
	}
	created := call("POST", "/credentials", map[string]any{"credential_name": name, "credential_values": map[string]any{"api_key": "fixture-contract-secret", "api_base": "http://127.0.0.1:1"}, "note": nil, "enabled": false, "zero": 0, "password": "ignored"}, 200)
	if _, ok := created.json()["credential_values"]; ok {
		t.Fatal("创建响应不应回传凭据对象")
	}
	if _, ok := created.json()["password"]; ok {
		t.Fatal("password 字段应忽略")
	}
	read := call("GET", detail, nil, 200)
	if strings.Contains(read.text(), "fixture-contract-secret") {
		t.Fatal("详情泄露明文测试凭据")
	}
	before := read.json()["credential_values"]
	for _, value := range []any{nil, "", "********", 42} {
		call("PATCH", path, map[string]any{"credential_values": map[string]any{"api_key": value}, "note": "saved"}, 200)
		if !reflect.DeepEqual(before, call("GET", detail, nil, 200).json()["credential_values"]) {
			t.Fatalf("空/脱敏 Key 覆盖原值: %v", value)
		}
	}
	call("PATCH", path, map[string]any{"credential_values": nil, "note": nil}, 200)
	obj := call("GET", detail, nil, 200).json()
	if value, ok := obj["note"]; !ok || value != nil {
		t.Fatal("显式null丢失")
	}
	if obj["enabled"] != false || obj["zero"] != float64(0) {
		t.Fatal("零值改变")
	}
	if _, ok := obj["absent"]; ok {
		t.Fatal("缺省扩展被添加")
	}
	list := call("GET", "/credentials", nil, 200).json()
	if !reflect.DeepEqual(list["data"], list["credentials"]) {
		t.Fatal("列表兼容别名不一致")
	}
	// 同名 POST 沿用替换语义；不得在契约试点中悄悄改成 conflict 或 merge。
	call("POST", "/credentials", map[string]any{"credential_name": name, "note": "replacement"}, 200)
	replaced := call("GET", detail, nil, 200).json()
	if _, ok := replaced["zero"]; ok {
		t.Fatal("重复创建未按原语义替换")
	}
	for _, count := range []float64{1, 0} {
		if got := call("DELETE", path, nil, 200).json()["deleted"]; got != count {
			t.Fatalf("删除数 want=%v got=%v", count, got)
		}
	}
	call("GET", detail, nil, 404)
	call("PATCH", path, map[string]any{"note": "upsert"}, 200)
	if call("GET", detail, nil, 200).json()["note"] != "upsert" {
		t.Fatal("缺失资源更新未创建")
	}
	call("DELETE", path, nil, 200)
	generated := call("POST", "/credentials", map[string]any{}, 200).json()["credential_name"].(string)
	if !strings.HasPrefix(generated, "credential_") {
		t.Fatal("空对象创建未生成名称")
	}
	call("DELETE", "/credentials/"+generated, nil, 200)
	if rows := rowsOf(call("GET", "/credentials", nil, 200), "credentials"); len(rows) != 0 {
		t.Fatal("供应商未清理")
	}
}

// TestSupplierContractDeniedAndMalformed 对每个试点操作验证真实未认证401和非管理员403，及非法 JSON/RPC 的400且目录不变。
// 不跳过网关鉴权；隔离普通用户由 openScope/harness 清理，非法操作不得生成供应商。
func TestSupplierContractDeniedAndMalformed(t *testing.T) {
	c := newSupplierContractClient(t)
	owner := c.h.openScope(t, c.admin, "supplier-contract-permission")
	for _, op := range []struct{ method, path string }{{"GET", "/credentials"}, {"POST", "/credentials"}, {"GET", "/credentials/by_name/contract-denied"}, {"PATCH", "/credentials/contract-denied"}, {"DELETE", "/credentials/contract-denied"}} {
		for _, auth := range []struct {
			token  string
			status int
		}{{"", 401}, {owner.session, 403}} {
			c.call(op.method, op.path, auth.token, nil, auth.status)
		}
	}
	for _, body := range []string{`null`, `[]`, `{"credential_name":`, `{} {}`} {
		req := httptest.NewRequest("POST", "/credentials", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if err := c.v.Request(req); err == nil {
			t.Fatalf("畸形输入未被契约拒绝: %s", body)
		}
		r := catalogRawRequest(t, c.h, "POST", "/credentials", c.admin, body)
		c.check(req, r, 400)
	}
	// GET 常规无正文；意外收到损坏正文也由同一解析器拒绝，错误响应仍必须记入契约。
	for _, path := range []string{"/credentials", "/credentials/by_name/contract-denied"} {
		req := httptest.NewRequest("GET", path, strings.NewReader("null"))
		c.check(req, catalogRawRequest(t, c.h, "GET", path, c.admin, "null"), 400)
	}
	c.call("POST", "/credentials", c.admin, map[string]any{"jsonrpc": "2.0", "method": "tools/list"}, 400)
	if rows := rowsOf(c.call("GET", "/credentials", c.admin, nil, 200), "credentials"); len(rows) != 0 {
		t.Fatal("失败操作产生记录")
	}
}

// TestSupplierContractStorageFailures 对供应商真实表注入写约束、删除触发器和读取故障，验证500契约及原数据不变，撤销故障后仍可正常使用。
// 私有 schema 不影响用户数据库；t.Cleanup 恢复 SQL 结构，最后删除供应商，清理错误同样失败。
func TestSupplierContractStorageFailures(t *testing.T) {
	c := newSupplierContractClient(t)
	const path = "/credentials/contract-storage"
	const detail = "/credentials/by_name/contract-storage"
	call := func(method, path string, body any, status int) reply {
		return c.call(method, path, c.admin, body, status)
	}
	catalogSQL(t, c.h, `ALTER TABLE kv ADD CONSTRAINT supplier_reject CHECK (kind <> 'credentials' OR body NOT LIKE '%reject-contract-write%')`)
	t.Cleanup(func() { catalogSQL(t, c.h, `ALTER TABLE kv DROP CONSTRAINT IF EXISTS supplier_reject`) })
	call("POST", "/credentials", map[string]any{"credential_name": "contract-storage", "note": "reject-contract-write"}, 500)
	call("GET", detail, nil, 404)
	call("POST", "/credentials", map[string]any{"credential_name": "contract-storage", "note": "original"}, 200)
	call("PATCH", path, map[string]any{"note": "reject-contract-write"}, 500)
	if call("GET", detail, nil, 200).json()["note"] != "original" {
		t.Fatal("失败写入污染数据")
	}
	catalogSQL(t, c.h, `CREATE FUNCTION supplier_reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.kind='credentials' THEN RAISE EXCEPTION 'private-contract-error'; END IF; RETURN OLD; END $$`)
	catalogSQL(t, c.h, `CREATE TRIGGER supplier_delete_failure BEFORE DELETE ON kv FOR EACH ROW EXECUTE FUNCTION supplier_reject_delete()`)
	t.Cleanup(func() {
		catalogSQL(t, c.h, `DROP TRIGGER IF EXISTS supplier_delete_failure ON kv`)
		catalogSQL(t, c.h, `DROP FUNCTION IF EXISTS supplier_reject_delete()`)
	})
	failed := call("DELETE", path, nil, 500)
	if strings.Contains(failed.text(), "private-contract-error") {
		t.Fatal("存储异常泄露")
	}
	if call("GET", detail, nil, 200).json()["note"] != "original" {
		t.Fatal("失败删除丢失数据")
	}
	catalogSQL(t, c.h, `DROP TRIGGER supplier_delete_failure ON kv`)
	catalogSQL(t, c.h, `CREATE FUNCTION supplier_read_failure() RETURNS text LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-contract-read'; END $$`)
	catalogSQL(t, c.h, `ALTER TABLE kv RENAME TO supplier_offline_kv`)
	restored := false
	t.Cleanup(func() {
		if !restored {
			catalogSQL(t, c.h, `DROP VIEW IF EXISTS kv`)
			catalogSQL(t, c.h, `ALTER TABLE supplier_offline_kv RENAME TO kv`)
		}
		catalogSQL(t, c.h, `DROP FUNCTION IF EXISTS supplier_read_failure()`)
	})
	catalogSQL(t, c.h, `CREATE VIEW kv AS SELECT kind,id,CASE WHEN kind='credentials' THEN supplier_read_failure() ELSE body END AS body,created_at FROM supplier_offline_kv`)
	call("GET", "/credentials", nil, 500)
	call("GET", detail, nil, 500)
	call("PATCH", path, map[string]any{"note": "changed"}, 500)
	catalogSQL(t, c.h, `DROP VIEW kv`)
	catalogSQL(t, c.h, `ALTER TABLE supplier_offline_kv RENAME TO kv`)
	restored = true
	if call("GET", detail, nil, 200).json()["note"] != "original" {
		t.Fatal("读失败覆盖原配置")
	}
	call("DELETE", path, nil, 200)
	call("GET", detail, nil, 404)
}
