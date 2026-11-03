package gateway

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"github.com/crewjam/saml"
	"github.com/sunqirui1987/xhub/cmd/regression/testsupport/samltest"
	"github.com/sunqirui1987/xhub/internal/iam"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSAMLRefreshHTTPRegression 使用真实路由、PostgreSQL 和本地 TLS IdP；覆盖开关、周期执行、租约、失败保留和权限。
// 已发起登录的 revision 保持不变，异常状态可回读；独立 schema、监听与测试 IdP 由 Cleanup 回收，无生产依赖。
func TestSAMLRefreshHTTPRegression(t *testing.T) {
	cfg, st, db := testGatewayStores(t)
	ctx := t.Context()
	cfg.GeneralSettings.MasterKey = strings.Repeat("s", 32)
	admin, err := db.CreateUser(ctx, testActor, iam.UserInput{Email: "refresh-admin@example.com", Password: "password123", Role: iam.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.CreateUser(ctx, testActor, iam.UserInput{Email: "refresh-member@example.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := db.CreateOrg(ctx, testActor, "Refresh test", nil)
	if err != nil {
		t.Fatal(err)
	}
	idp := samltest.NewExpired(t)
	var mode atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			w.WriteHeader(503)
			return
		case 2:
			_, _ = w.Write([]byte("<bad>"))
			return
		case 3:
			_, _ = w.Write([]byte(strings.ReplaceAll(idp.Metadata(), "/sso", "/changed")))
			return
		}
		_, _ = w.Write([]byte(idp.Metadata()))
	}))
	t.Cleanup(upstream.Close)
	s := New(cfg, st, db)
	s.enterpriseClient = upstream.Client()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	session := loginAs(t, srv.URL, admin.Email, "password123")
	user := loginAs(t, srv.URL, member.Email, "password123")
	payload := map[string]any{"organization_id": org.ID, "name": "Refresh", "protocol": "saml", "allow_http": true, "allow_expired_idp_certificate": true, "client_id": "urn:xhub:refresh", "redirect_uri": srv.URL + "/auth/enterprise/saml/acs", "return_uri": srv.URL + "/ui/login", "idp_metadata": idp.Metadata(), "enabled": true}
	raw, _ := json.Marshal(payload)
	status, body := authed(t, srv.URL, session, "POST", "/admin/enterprise/sso", raw)
	if status != 201 {
		t.Fatal(status, string(body))
	}
	var c iam.EnterpriseOIDC
	if err = json.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	path := "/admin/enterprise/sso/" + c.ConnectionID + "/refresh"
	for _, verb := range []string{"GET", "PUT", "POST"} {
		for _, auth := range []struct {
			key  string
			want int
		}{{"", 401}, {user, 403}} {
			status, _ = authed(t, srv.URL, auth.key, verb, path, []byte(`{}`))
			if status != auth.want {
				t.Fatalf("%s 权限: %d", verb, status)
			}
		}
	}
	status, body = authed(t, srv.URL, session, "GET", path, nil)
	if status != 200 || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatal("默认应关闭", status, string(body))
	}
	for _, input := range []string{`{`, `{"interval_hours":0}`, `{"interval_hours":25}`, `{"interval_hours":6,"enabled":true,"source_url":"http://localhost/meta"}`, `{"interval_hours":6,"enabled":true}`} {
		status, _ = authed(t, srv.URL, session, "PUT", path, []byte(input))
		if status != 400 {
			t.Fatal("非法设置被接受", input, status)
		}
	}
	settings, _ := json.Marshal(map[string]any{"enabled": true, "source_url": upstream.URL, "interval_hours": 6})
	status, body = authed(t, srv.URL, session, "PUT", path, settings)
	if status != 200 {
		t.Fatal(status, string(body))
	}
	// 在真实数据库保存已到期 XML，模拟旧配置 48 小时后失效；仅测试夹具操作私有 schema。
	var expired saml.EntityDescriptor
	_ = xml.Unmarshal([]byte(c.IDPMetadata), &expired)
	expired.ValidUntil = time.Now().Add(-time.Hour)
	old, _ := xml.Marshal(expired)
	if _, err = db.Engine.Exec("UPDATE enterprise_oidc SET idp_metadata=? WHERE connection_id=?", string(old), c.ConnectionID); err != nil {
		t.Fatal(err)
	}
	c0, err := db.EnterpriseOIDCByID(ctx, c.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	// 服务启动触发后台到期扫描，不通过管理 POST 来假冒定时执行。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.serveListener(ln) }()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("刷新后台未停止")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, e := db.SAMLRefreshByID(ctx, c.ConnectionID)
		if e != nil {
			t.Fatal(e)
		}
		if r.LastSuccess > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("后台未刷新: %+v", r)
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, err := db.EnterpriseOIDCByID(ctx, c.ConnectionID)
	if err != nil || current.Revision != c0.Revision || !samlMetadataExpiry(current.IDPMetadata).After(time.Now()) {
		t.Fatal("自动刷新未恢复或改变登录版本", err)
	}
	// 后续立即刷新失败必须可见，并保留现有可用 XML；使用真实网络状态和错误正文。
	for _, tc := range []struct {
		mode int32
		code string
	}{{1, "fetch_failed"}, {2, "invalid_metadata"}, {3, "trust_changed"}} {
		mode.Store(tc.mode)
		status, body = authed(t, srv.URL, session, "POST", path, []byte(`{}`))
		if status != 502 {
			t.Fatal("失败被报告成功", status, string(body))
		}
		status, body = authed(t, srv.URL, session, "GET", path, nil)
		if status != 200 || !strings.Contains(string(body), tc.code) {
			t.Fatal("失败不可见", string(body))
		}
		got, _ := db.EnterpriseOIDCByID(ctx, c.ConnectionID)
		if got.IDPMetadata != current.IDPMetadata {
			t.Fatal("失败覆盖 XML")
		}
	}
	mode.Store(0)
	claim, err := db.ClaimSAMLRefresh(ctx, c.ConnectionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimSAMLRefresh(ctx, c.ConnectionID, true); !errors.Is(err, iam.ErrConflict) {
		t.Fatal("重复实例领取同一任务", err)
	}
	// 保存关闭使在途租约失效；迟到提交必须拒绝。
	if err = db.SaveSAMLRefresh(ctx, testActor, current, iam.SAMLRefresh{SourceURL: upstream.URL, IntervalHours: 6}); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishSAMLRefresh(ctx, testActor, current, claim, current.IDPMetadata, "", time.Now().Unix()); !errors.Is(err, iam.ErrConflict) {
		t.Fatal("旧租约覆盖新设置", err)
	}
	status, body = authed(t, srv.URL, session, "POST", path, []byte(`{}`))
	if status != 200 {
		t.Fatal("关闭自动刷新后手动刷新失败", status, string(body))
	}
	status, _ = authed(t, srv.URL, session, "DELETE", "/admin/enterprise/sso/"+c.ConnectionID, nil)
	if status != 204 {
		t.Fatal("清理失败", status)
	}
	n, err := db.Engine.Where("connection_id=?", c.ConnectionID).Count(new(iam.SAMLRefresh))
	if err != nil || n != 0 {
		t.Fatal("调度未随连接清理", n, err)
	}
}
