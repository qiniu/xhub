package regression

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSearchProbeUsesUpstream 通过真实管理员路由和HTTP搜索夹具验证探测确实发出请求；私有数据库由harness清理。
// 参数t为测试上下文；从真实发现接口取得供应商，夹具核对请求正文与凭据，并用不同协议响应证明目录和执行一致。
func TestSearchProbeUsesUpstream(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	providers := rowsOf(h.ok("GET", "/search_tools/ui/available_providers", admin, nil), "providers")
	if len(providers) != 3 {
		t.Fatal("发现目录未列出三个已有适配器", providers)
	}
	for _, row := range providers {
		provider, ok := row["provider_name"].(string)
		if !ok || provider == "" {
			t.Fatal("供应商名称契约错误", row)
		}
		t.Run(provider, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				queryKey, header, secret := "query", "Authorization", "Bearer search-fixture-secret"
				result := `{"results":[{"title":"Fixture result","url":"https://example.com/search-probe","content":"Fixture snippet","snippet":"Fixture snippet"}]}`
				if provider == "serper" {
					queryKey, header, secret = "q", "X-API-KEY", "search-fixture-secret"
					result = `{"organic":[{"title":"Fixture result","link":"https://example.com/search-probe","snippet":"Fixture snippet"}]}`
				}
				if r.Method != "POST" || r.URL.Path != "/probe/search" || body[queryKey] != "Search fixture query" || r.Header.Get(header) != secret {
					t.Errorf("搜索上游收到错误请求: %s %s %#v", r.Method, r.URL.Path, body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(result))
			}))
			defer upstream.Close()
			got := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"query": "Search fixture query", "litellm_params": map[string]any{"search_provider": provider, "api_key": "search-fixture-secret", "api_base": upstream.URL + "/probe/search"}})
			if got.status != 200 || got.json()["status"] != "success" || got.json()["results_count"] != float64(1) || calls.Load() != 1 || !strings.Contains(got.text(), "Fixture snippet") {
				t.Fatalf("搜索探测没有执行真实HTTP查询并读回结果: calls=%d %s", calls.Load(), got.describe())
			}
			if strings.Contains(got.text(), "search-fixture-secret") {
				t.Fatal("搜索探测泄漏API Key")
			}
		})
	}
}

// TestSearchProbeRejectsFalseSuccess 使用不存在资源、损坏正文和错误上游证明失败不能返回成功；不访问付费服务，夹具和schema自动清理。
// 参数t为测试上下文；预期未知ID404、缺少配置400，上游401/403/429/5xx及坏JSON返回明确错误且不泄漏正文。
func TestSearchProbeRejectsFalseSuccess(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	for name, body := range map[string]any{"missing-id": map[string]any{"search_tool_id": "missing"}, "missing-params": map[string]any{"search_tool_name": "name-only"}} {
		t.Run(name, func(t *testing.T) {
			got := h.do("POST", "/search_tools/test_connection", admin, body)
			want := 400
			if name == "missing-id" {
				want = 404
			}
			if got.status != want {
				t.Fatalf("无配置或不存在资源被报成功: %s", got.describe())
			}
		})
	}
	for _, status := range []int{401, 403, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"search-fixture-secret"}`))
			}))
			defer upstream.Close()
			got := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"litellm_params": map[string]any{"search_provider": "tavily", "api_key": "search-fixture-secret", "api_base": upstream.URL}})
			if got.status != status || got.json()["status"] != "error" || got.json()["upstream_status"] != float64(status) || calls.Load() != 1 || strings.Contains(got.text(), "search-fixture-secret") {
				t.Fatalf("上游失败被隐藏或泄漏: calls=%d %s", calls.Load(), got.describe())
			}
		})
	}
}

// TestSearchProbeSavedConfiguration 验证探测只读真实保存记录，并使用服务器持有的Key；参数t为上下文，私有schema及HTTP夹具自动清理。
// 创建/列表/详情/更新均不得公开Key，省略Key更新仍能成功调用；探测不能额外创建目录对象，删除后必须404。
func TestSearchProbeSavedConfiguration(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer saved-search-secret" {
			t.Error("保存的搜索凭据丢失或误用掩码")
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer upstream.Close()
	const id = "saved-search-probe"
	created := h.ok("POST", "/search_tools", admin, map[string]any{"search_tool_id": id, "search_tool_name": "Saved probe", "litellm_params": map[string]any{"search_provider": "tavily", "api_key": "saved-search-secret", "api_base": upstream.URL}})
	for _, got := range []reply{created, h.ok("GET", "/search_tools/"+id, admin, nil), h.ok("GET", "/search_tools/list", admin, nil)} {
		if strings.Contains(got.text(), "saved-search-secret") {
			t.Fatalf("管理响应泄漏搜索凭据: %s", got.describe())
		}
	}
	updated := h.ok("PUT", "/search_tools/"+id, admin, map[string]any{"litellm_params": map[string]any{"api_base": upstream.URL}})
	if strings.Contains(updated.text(), "saved-search-secret") {
		t.Fatal("更新响应泄漏搜索凭据")
	}
	before := h.ok("GET", "/search_tools/list", admin, nil).text()
	got := h.ok("POST", "/search_tools/test_connection", admin, map[string]any{"search_tool_id": id})
	if got.json()["status"] != "success" || got.json()["results_count"] != float64(0) || got.json()["test_query"] != "OpenAI API documentation" || calls.Load() != 1 {
		t.Fatalf("保存的配置未真实执行空结果查询: calls=%d %s", calls.Load(), got.describe())
	}
	after := h.ok("GET", "/search_tools/list", admin, nil).text()
	if before != after {
		t.Fatal("连接测试意外修改目录")
	}
	h.ok("DELETE", "/search_tools/"+id, admin, nil)
	if missing := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"search_tool_id": id}); missing.status != 404 {
		t.Fatalf("已删除配置仍被报成功: %s", missing.describe())
	}
}

// TestSearchProbeAuthorizationAndValidation 验证管理鉴权先于探测，以及未知供应商/掩码/坏JSON不访问上游。
// 参数t为测试上下文；真实登录、数据库和HTTP边界，无付费凭据；harness与defer关闭夹具并清理身份数据。
func TestSearchProbeAuthorizationAndValidation(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "search-probe-auth")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{"results":[]}`)) }))
	defer upstream.Close()
	params := map[string]any{"search_provider": "tavily", "api_key": "fixture-secret", "api_base": upstream.URL}
	for _, tc := range []struct {
		name, token string
		status      int
	}{{"anonymous", "", 401}, {"ordinary-user", owner.session, 403}} {
		t.Run(tc.name, func(t *testing.T) {
			got := h.do("POST", "/search_tools/test_connection", tc.token, map[string]any{"litellm_params": params})
			if got.status != tc.status {
				t.Fatalf("探测权限错误: %s", got.describe())
			}
		})
	}
	for name, body := range map[string]any{"both": map[string]any{"search_tool_id": "id", "litellm_params": params}, "unsupported": map[string]any{"litellm_params": map[string]any{"search_provider": "unknown", "api_key": "fixture", "api_base": upstream.URL}}, "masked": map[string]any{"litellm_params": map[string]any{"search_provider": "tavily", "api_key": "********", "api_base": upstream.URL}}} {
		t.Run(name, func(t *testing.T) {
			got := h.do("POST", "/search_tools/test_connection", admin, body)
			if got.status != 400 || got.json()["status"] != "error" {
				t.Fatalf("非法探测配置未拒绝: %s", got.describe())
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, `{"litellm_params":`, `{} {}`, `{"litellm_params":[]}`} {
		got := catalogRawRequest(t, h, "POST", "/search_tools/test_connection", admin, raw)
		if got.status != 400 {
			t.Fatalf("损坏JSON未拒绝: %s", got.describe())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("被拒绝请求仍访问上游: %d", calls.Load())
	}
}

// TestSearchProbeMalformedUpstream 验证坏JSON、错误形状和重定向都不能变成200成功；参数t为上下文，本地HTTP夹具自动关闭。
// 真实网关消费供应商响应而非mock管理API，跳转目标的计数证明Key未被转发出去。
func TestSearchProbeMalformedUpstream(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	for name, body := range map[string]string{"bad-json": `not-json fixture-secret`, "no-results": `{"message":"OK"}`, "null-results": `{"results":null}`, "bad-result": `{"results":[{"title":2}]}`, "error-envelope": `{"error":"fixture-secret","results":[]}`} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(body)) }))
			defer upstream.Close()
			got := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"litellm_params": map[string]any{"search_provider": "tavily", "api_key": "fixture-secret", "api_base": upstream.URL}})
			if got.status != 502 || got.json()["error_type"] != "invalid_search_response" || calls.Load() != 1 || strings.Contains(got.text(), "fixture-secret") {
				t.Fatalf("坏响应被报成功或泄密: %s", got.describe())
			}
		})
	}
	var redirectCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectCalls.Add(1)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer upstream.Close()
	got := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"litellm_params": map[string]any{"search_provider": "serper", "api_key": "fixture-secret", "api_base": upstream.URL}})
	if got.status != 502 || got.json()["upstream_status"] != float64(302) || redirectCalls.Load() != 0 {
		t.Fatalf("重定向未被阻止: redirected=%d %s", redirectCalls.Load(), got.describe())
	}
}

// TestSearchProbeStorageFailure 验证真实SQL读取故障不能伪装成成功；参数t为上下文，仅让本例搜索目录视图抛错。
// 登录会话KV行保持可读，探测返回500且不公开SQL错误；恢复表后删除配置，harness清理私有schema。
func TestSearchProbeStorageFailure(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	h.ok("POST", "/search_tools", admin, map[string]any{"search_tool_id": "search-storage-failure", "litellm_params": map[string]any{"search_provider": "tavily", "api_key": "fixture-secret"}})
	catalogSQL(t, h, `CREATE FUNCTION search_probe_read_failure() RETURNS text LANGUAGE plpgsql VOLATILE AS $$ BEGIN RAISE EXCEPTION 'private-search-sql-error'; END $$`)
	catalogSQL(t, h, `ALTER TABLE kv RENAME TO search_probe_original_kv`)
	restored := false
	t.Cleanup(func() {
		if !restored {
			catalogSQL(t, h, `DROP VIEW IF EXISTS kv`)
			catalogSQL(t, h, `ALTER TABLE search_probe_original_kv RENAME TO kv`)
		}
		catalogSQL(t, h, `DROP FUNCTION IF EXISTS search_probe_read_failure()`)
	})
	catalogSQL(t, h, `CREATE VIEW kv AS SELECT kind, id, CASE WHEN kind = 'search_tools' THEN search_probe_read_failure() ELSE body END AS body, created_at FROM search_probe_original_kv`)
	// 真实SQL故障使配置列表失败，但适配器目录不读取配置，仍须返回既有三个供应商。
	if out := h.do("GET", "/search_tools/list", admin, nil); out.status != 500 {
		t.Fatal("故障注入未命中真实配置读取", out.describe())
	}
	if rows := rowsOf(h.ok("GET", "/search_tools/ui/available_providers", admin, nil), "providers"); len(rows) != 3 {
		t.Fatal("配置读取故障污染静态适配器目录", rows)
	}
	got := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"search_tool_id": "search-storage-failure"})
	if got.status != 500 || got.json()["error_type"] != "store_unavailable" || strings.Contains(got.text(), "private-search-sql-error") {
		t.Fatalf("真实SQL读取故障被隐藏或泄漏: %s", got.describe())
	}
	catalogSQL(t, h, `DROP VIEW kv`)
	catalogSQL(t, h, `ALTER TABLE search_probe_original_kv RENAME TO kv`)
	restored = true
	h.ok("DELETE", "/search_tools/search-storage-failure", admin, nil)
}

// TestSearchProbeTimeoutAndCancellation 验证真实管理HTTP路由继承超时和客户端取消；参数t为上下文，本地上游读取请求后等待取消。
// 预期超时返回504，客户端中断能取消出站请求；所有HTTP服务和隔离schema自动清理，不以mock代替网关路径。
func TestSearchProbeTimeoutAndCancellation(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	t.Run("timeout", func(t *testing.T) {
		var calls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			calls.Add(1)
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		}))
		defer upstream.Close()
		original := h.gw.Client
		client := *original
		client.Timeout = 25 * time.Millisecond
		h.gw.Client = &client
		defer func() { h.gw.Client = original }()
		got := h.do("POST", "/search_tools/test_connection", admin, map[string]any{"litellm_params": map[string]any{"search_provider": "tavily", "api_key": "fixture", "api_base": upstream.URL}})
		if got.status != 504 || got.json()["error_type"] != "request_timeout" || calls.Load() != 1 {
			t.Fatalf("真实上游超时未正确传播: calls=%d %s", calls.Load(), got.describe())
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		started := make(chan struct{})
		cancelled := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			select {
			case <-r.Context().Done():
				close(cancelled)
			case <-time.After(time.Second):
			}
		}))
		defer upstream.Close()
		body, _ := json.Marshal(map[string]any{"litellm_params": map[string]any{"search_provider": "tavily", "api_key": "fixture", "api_base": upstream.URL}})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, "POST", h.server.URL+"/search_tools/test_connection", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+admin)
		request.Header.Set("Content-Type", "application/json")
		completed := make(chan error, 1)
		go func() {
			response, err := http.DefaultClient.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			completed <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("取消测试未真正访问上游")
		}
		cancel()
		select {
		case err := <-completed:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("客户端取消结果错误: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("客户端取消没有终止请求")
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("客户端取消没有传播到上游")
		}
	})
}

// TestSearchProbeMaskedRoundTrip 验证搜索配置真实HTTP保存、脱敏读取、完整PUT回传与后续探测，防止嵌套掩码污染持久化秘密。
// 参数t为上下文；本地HTTP夹具核对原api_key，私有库只为无法从脱敏接口观察的嵌套秘密作补充读回；删除记录并由harness清理schema。
func TestSearchProbeMaskedRoundTrip(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer roundtrip-search-key" {
			t.Error("整体回传后探测未使用原API Key")
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"Roundtrip","url":"https://example.com/roundtrip","content":"Preserved credential"}]}`))
	}))
	defer upstream.Close()
	const path = "/search_tools/search-roundtrip"
	initial := map[string]any{"search_tool_id": "search-roundtrip", "search_tool_name": "Roundtrip search", "litellm_params": map[string]any{
		"search_provider": "tavily", "api_key": "roundtrip-search-key", "api_base": upstream.URL,
		"headers":     map[string]any{"Authorization": "Bearer nested-search-secret", "X-Region": "us"},
		"credentials": []any{map[string]any{"name": "first", "token": "array-first-secret"}, map[string]any{"name": "second", "secret": "array-second-secret"}},
		"targets":     []any{map[string]any{"name": "primary", "token": "target-primary-secret"}, map[string]any{"name": "secondary", "token": "target-secondary-secret"}},
	}}
	created := h.ok("POST", "/search_tools", admin, initial)
	read := h.ok("GET", path, admin, nil)
	for _, result := range []reply{created, read, h.ok("GET", "/search_tools/list", admin, nil)} {
		for _, secret := range []string{"roundtrip-search-key", "nested-search-secret", "array-first-secret", "array-second-secret", "target-primary-secret", "target-secondary-secret"} {
			if strings.Contains(result.text(), secret) {
				t.Fatal("嵌套搜索配置秘密出现在公开响应")
			}
		}
	}
	body := read.json()
	params := body["litellm_params"].(map[string]any)
	headers := params["headers"].(map[string]any)
	if headers["Authorization"] != "********" || params["api_key"] != "********" {
		t.Fatalf("详情未给出嵌套秘密掩码: %v", params)
	}
	headers["X-Region"] = "cn"
	// targets为非敏感数组名，内部token被逐项遮盖；故意调换数组顺序，防止合并器按索引把旧秘密绑定到错误目标。
	targets := params["targets"].([]any)
	if targets[0].(map[string]any)["token"] != "********" || targets[1].(map[string]any)["token"] != "********" {
		t.Fatal("数组内部秘密未脱敏")
	}
	targets[0], targets[1] = targets[1], targets[0]
	updated := h.ok("PUT", path, admin, body)
	if strings.Contains(updated.text(), "nested-search-secret") || strings.Contains(updated.text(), "roundtrip-search-key") {
		t.Fatal("PUT响应泄漏秘密")
	}
	// 无公共接口可合法回传明文；只读本例私有store验证隐藏数据未被掩码覆盖，不借此替代HTTP生命周期与实际出站证据。
	stored, err := h.store.GetKV("search_tools", "search-roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	storedParams := stored["litellm_params"].(map[string]any)
	storedHeaders := storedParams["headers"].(map[string]any)
	if storedParams["api_key"] != "roundtrip-search-key" || storedHeaders["Authorization"] != "Bearer nested-search-secret" || storedHeaders["X-Region"] != "cn" {
		t.Fatal("整体回传覆盖了原秘密或漏保存普通嵌套字段")
	}
	credentials, ok := storedParams["credentials"].([]any)
	if !ok || len(credentials) != 2 || credentials[0].(map[string]any)["token"] != "array-first-secret" || credentials[1].(map[string]any)["secret"] != "array-second-secret" {
		t.Fatal("敏感数组被掩码替换")
	}

	storedTargets, ok := storedParams["targets"].([]any)
	if !ok || len(storedTargets) != 2 || storedTargets[0].(map[string]any)["name"] != "primary" || storedTargets[0].(map[string]any)["token"] != "target-primary-secret" || storedTargets[1].(map[string]any)["name"] != "secondary" || storedTargets[1].(map[string]any)["token"] != "target-secondary-secret" {
		t.Fatal("含掩码数组回传按索引污染原目标与秘密")
	}
	got := h.ok("POST", "/search_tools/test_connection", admin, map[string]any{"search_tool_id": "search-roundtrip"})
	if got.json()["status"] != "success" || got.json()["results_count"] != float64(1) || calls.Load() != 1 {
		t.Fatalf("回传后真实搜索探测失败: calls=%d %s", calls.Load(), got.describe())
	}
	h.ok("DELETE", path, admin, nil)
	if missing := h.do("GET", path, admin, nil); missing.status != 404 {
		t.Fatalf("搜索回传夹具清理失败: %s", missing.describe())
	}
}
