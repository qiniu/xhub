package regression

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestLogFilterOptionsScope 真实 PostgreSQL 与网关验证候选的权限、启用状态、时间窗口、分页、精确搜索和失败响应。
// 前置隔离 schema 的两个用户及三个部署；只写独立夹具，失败时恢复表列，harness 在结束时清理所有数据。
func TestLogFilterOptionsScope(t *testing.T) {
	a, b, disabled := chatDeployment("suggest-a"), chatDeployment("suggest-b"), chatDeployment("suggest-a")
	empty, inline := chatDeployment("suggest-empty"), chatDeployment("suggest-inline")
	// 非法地址不产生供应商身份；带凭据的地址只应在日志中留下脱敏地址。
	empty.LiteLLMParams["api_base"] = "http://%zz"
	inline.LiteLLMParams["api_base"] = "https://user:secret@provider.example/v1?token=secret#fragment"
	a.LiteLLMParams["litellm_credential_name"] = "suggest-provider-a"
	b.LiteLLMParams["litellm_credential_name"] = "suggest-provider-b"
	disabled.LiteLLMParams["litellm_credential_name"] = "suggest-disabled"
	if disabled.ModelInfo == nil {
		disabled.ModelInfo = map[string]any{}
	}
	disabled.ModelInfo["disabled"] = true
	h := newHarness(t, a, b, disabled, empty, inline)
	admin := h.adminSession()
	owner := h.provision(t, admin, "suggest-owner")
	other := h.provision(t, admin, "suggest-other")
	h.ok(http.MethodPatch, "/organization/update", admin, map[string]any{"organization_id": owner.orgID, "models": []string{a.ModelName, empty.ModelName, inline.ModelName}})
	now := time.Now().UTC().Truncate(time.Second)
	records := []iam.UsageRecord{
		{RequestID: "suggest-a1", UserID: owner.userID, Model: a.ModelName, Supplier: "suggest-provider-a", KeyAlias: "alpha", KeyHash: "sha256-a", InboundEndpoint: "/v1/responses", Status: "success", HTTPStatus: 200, TS: now},
		{RequestID: "suggest-a2", UserID: owner.userID, Model: a.ModelName, Supplier: "suggest-provider-a", KeyAlias: "beta_%", KeyHash: "sha256-b", InboundEndpoint: "/v1/chat/completions", Status: "success", HTTPStatus: 200, TS: now},
		{RequestID: "suggest-disabled1", UserID: owner.userID, Model: a.ModelName, Supplier: "suggest-disabled", KeyAlias: "disabled-key", Status: "success", HTTPStatus: 200, TS: now},
		{RequestID: "suggest-revoked", UserID: owner.userID, Model: b.ModelName, Supplier: "suggest-provider-b", KeyAlias: "old-key", Status: "success", HTTPStatus: 200, TS: now},
		{RequestID: "suggest-empty", UserID: owner.userID, Model: empty.ModelName, Supplier: "", Status: "success", HTTPStatus: 200, TS: now},
		{RequestID: "suggest-inline", UserID: owner.userID, Model: inline.ModelName, Supplier: "https://provider.example/v1", Status: "success", HTTPStatus: 200, TS: now},
		{RequestID: "suggest-foreign", UserID: other.userID, Model: a.ModelName, Supplier: "suggest-provider-a", KeyAlias: "foreign-secret-alias", KeyHash: "foreign-secret-hash", Status: "success", HTTPStatus: 200, TS: now},
		{RequestID: "suggest-old", UserID: owner.userID, Model: a.ModelName, Supplier: "suggest-provider-a", KeyAlias: "out-of-range", Status: "success", HTTPStatus: 200, TS: now.Add(-48 * time.Hour)},
	}
	if err := h.db.RecordUsage(t.Context(), records); err != nil {
		t.Fatal(err)
	}
	base := "/spend/logs/ui/filter-options?start_date=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339)) + "&end_date=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))
	// check 验证真实接口输出与授权范围一致，无额外数据需要清理。
	check := func(field string, want []any) {
		t.Helper()
		response := h.ok("GET", base+"&field="+field, owner.session, nil)
		if response.headers.Get("Cache-Control") != "no-store" {
			t.Fatal("候选响应必须禁用缓存", response.headers)
		}
		body := response.json()
		if !reflect.DeepEqual(body["data"], want) {
			t.Fatalf("%s 候选权限或内容错误：%v，期望 %v", field, body, want)
		}
	}
	check("supplier", []any{"https://provider.example/v1", "suggest-provider-a"})
	check("model", []any{"suggest-a", "suggest-empty", "suggest-inline"})
	check("key_hash", []any{"sha256-a", "sha256-b"})
	check("inbound_endpoint", []any{"/v1/chat/completions", "/v1/responses"})
	page := h.ok("GET", base+"&field=key_alias&page_size=1", owner.session, nil).json()
	if page["has_more"] != true || !reflect.DeepEqual(page["data"], []any{"alpha"}) {
		t.Fatal("候选分页第一页错误", page)
	}
	page = h.ok("GET", base+"&field=key_alias&page_size=1&page=2", owner.session, nil).json()
	if !reflect.DeepEqual(page["data"], []any{"beta_%"}) {
		t.Fatal("候选分页第二页错误", page)
	}
	literal := h.ok("GET", base+"&field=key_alias&q="+url.QueryEscape("_%"), owner.session, nil).json()
	if !reflect.DeepEqual(literal["data"], []any{"beta_%"}) {
		t.Fatal("候选搜索应按字面匹配特殊字符", literal)
	}
	foreign := h.ok("GET", base+"&field=key_alias&user_id="+other.userID, owner.session, nil).json()
	if !reflect.DeepEqual(foreign["data"], []any{}) {
		t.Fatal("用户筛选不能扩大可见范围", foreign)
	}
	for _, extra := range []string{"field=bad", "field=key_hash&page=0", "field=model&page_size=51", "field=supplier&page=101"} {
		if got := h.do("GET", base+"&"+extra, owner.session, nil).status; got != 400 {
			t.Fatalf("无效条件 %s 返回 %d", extra, got)
		}
	}
	if got := h.do("GET", "/spend/logs/ui/filter-options?field=model", owner.session, nil).status; got != 400 {
		t.Fatalf("缺少日期返回 %d", got)
	}
	if got := h.do("GET", base+"&field=key_alias", "invalid-key", nil); got.status != 401 || got.headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("无效凭据应返回 401 且禁止缓存：%s，headers=%v", got.describe(), got.headers)
	}
	// 故障必须是非 2xx，不能伪装成空候选；提前注册清理以覆盖断言失败。
	if _, err := h.db.Engine.Exec("ALTER TABLE usage_events RENAME COLUMN key_alias TO suggest_hidden_alias"); err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			_, _ = h.db.Engine.Exec("ALTER TABLE usage_events RENAME COLUMN suggest_hidden_alias TO key_alias")
		}
	})
	if got := h.do("GET", base+"&field=key_alias", owner.session, nil); got.status < 500 || strings.Contains(got.text(), "suggest_hidden_alias") || got.headers.Get("Cache-Control") != "no-store" {
		t.Fatal(fmt.Sprintf("数据库失败必须返回安全错误：%s", got.describe()))
	}
	if _, err := h.db.Engine.Exec("ALTER TABLE usage_events RENAME COLUMN suggest_hidden_alias TO key_alias"); err != nil {
		t.Fatal(err)
	}
	restored = true
	check("key_hash", []any{"sha256-a", "sha256-b"})
}
