package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/qiniu/go-sdk/v7/auth"
	qclient "github.com/qiniu/go-sdk/v7/client"
	"github.com/qiniu/go-sdk/v7/storage"
	"github.com/qiniu/go-sdk/v7/storagev2/uplog"
	"github.com/sunqirui1987/xhub/cmd/regression/testsupport/samltest"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/gateway/affinity"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	"github.com/sunqirui1987/xhub/internal/store"
	"github.com/sunqirui1987/xhub/internal/util"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestBrowserEnterpriseFixture 启动仅编入测试二进制的 HTTPS 身份/对象服务和控制台代理。
// 参数 t 为测试运行器；仅显式开启时启动，要求私有 e2e schema，拒绝调用方提供的共享 Redis。
// E2E_REDIS=1 时夹具自行启动专属 Redis，并刷写真实队列；退出关闭连接和进程。
// 浏览器操作真实页面、网关和数据库，归档通过官方 SDK；控制端仅调度 worker/故障。
// 退出关闭服务和数据库，外层脚本删除私有 schema，不使用外部凭据，不改变生产 SSRF 限制。
func TestBrowserEnterpriseFixture(t *testing.T) {
	if os.Getenv("E2E_ENTERPRISE_FIXTURE") != "1" {
		t.Skip("仅供隔离浏览器运行")
	}
	dir := os.Getenv("E2E_RUN_DIR")
	cfg, err := config.Load(filepath.Join(dir, "c.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := url.Parse(cfg.GeneralSettings.DatabaseURL)
	if err != nil || !strings.HasPrefix(dsn.Query().Get("search_path"), "e2e_") || cfg.GeneralSettings.RedisURL != "" {
		t.Fatal("夹具要求私有 e2e schema，Redis 只能由夹具自行创建")
	}
	if os.Getenv("E2E_REDIS") == "1" {
		cfg.GeneralSettings.RedisURL = browserPrivateRedis(t)
	}
	ctx := context.Background()
	db, err := iam.Open(ctx, cfg.GeneralSettings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.GeneralSettings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Engine.Close()
	s := New(cfg, st, db)
	if cfg.GeneralSettings.RedisURL != "" {
		if s.Live == nil {
			t.Fatal("专属 Redis 未连接，浏览器验收不能降级为直接落库")
		}
		defer s.Live.Close()
		// 使用真实刷写方法，缩短离线夹具的等待；停止后再关闭连接，避免迟到刷写访问已关闭的数据库。
		stopFlush := browserSpendWorker(s)
		defer stopFlush()
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	mode := "valid"
	issuer := ""
	objects := map[string][]byte{}
	codes := map[string]url.Values{}
	// 一次性授权码保存 nonce、PKCE 和用户选择；表单由浏览器提交，服务端不替浏览器触发回调。
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/authorize":
			w.Header().Set("Content-Type", "text/html")
			if r.Method == "GET" {
				_, _ = fmt.Fprint(w, `<form method="post"><label>Employee<input name="employee" value="employee-sub"></label><button>Sign in to identity provider</button></form>`)
				return
			}
			_ = r.ParseForm()
			code, _ := enterpriseRandom()
			q := r.URL.Query()
			q.Set("sub", r.Form.Get("employee"))
			codes[code] = q
			http.Redirect(w, r, q.Get("redirect_uri")+"?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(q.Get("state")), 302)
		case "/token":
			_ = r.ParseForm()
			// 本地 IdP 拒绝授权码兑换；浏览器必须看到失败，不能建立 XHub 会话。
			if mode == "rejected" {
				http.Error(w, "invalid grant", http.StatusBadRequest)
				return
			}
			q, ok := codes[r.Form.Get("code")]
			delete(codes, r.Form.Get("code"))
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || q.Get("code_challenge_method") != "S256" || base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") || r.Form.Get("redirect_uri") != q.Get("redirect_uri") {
				http.Error(w, "invalid grant", 400)
				return
			}
			claims := map[string]any{"iss": issuer, "sub": q.Get("sub"), "aud": "xhub-browser", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": q.Get("nonce")}
			if mode == "nonce" {
				claims["nonce"] = "invalid"
			}
			if mode == "unknown" {
				claims["sub"] = "unprovisioned"
			}
			// 签名和身份正确但令牌已过期，验证回调实际校验时间声明。
			if mode == "expired" {
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "local-access", "token_type": "Bearer", "id_token": signedEnterpriseToken(t, key, claims)})
		default:
			w.WriteHeader(404)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	s.enterpriseClient = idp.Client()
	archiveSDKOnce.Do(uplog.DisableUplog)
	// 对象服务实现七牛 multipart、限定 token、stat 和不可覆盖语义；不以 mock Put 代替 SDK。
	objectServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Reqid", "browser-kodo")
		if mode == "archive-fail" {
			http.Error(w, `{"error":"fixture unavailable"}`, 503)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/":
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				http.Error(w, "multipart required", 400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			objectKey := r.FormValue("key")
			parts := strings.Split(r.FormValue("token"), ":")
			var policy storage.PutPolicy
			if len(parts) != 3 || parts[0] != "fake-ak" {
				w.WriteHeader(401)
				return
			}
			raw, _ := base64.URLEncoding.DecodeString(parts[2])
			if auth.New("fake-ak", "fake-sk").Sign([]byte(parts[2])) != parts[0]+":"+parts[1] || json.Unmarshal(raw, &policy) != nil || policy.Scope != "xhub-test:"+objectKey || policy.InsertOnly != 1 {
				w.WriteHeader(401)
				return
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(400)
				return
			}
			defer f.Close()
			body, err := io.ReadAll(f)
			if err != nil {
				w.WriteHeader(400)
				return
			}
			if _, found := objects[objectKey]; found {
				w.WriteHeader(614)
				_, _ = w.Write([]byte(`{"error":"file exists"}`))
				return
			}
			objects[objectKey] = body
			_ = json.NewEncoder(w).Encode(map[string]any{"key": objectKey, "hash": kodoContentHash(body)})
		case strings.HasPrefix(r.URL.Path, "/stat/") || strings.HasPrefix(r.URL.Path, "/delete/"):
			valid, err := auth.New("fake-ak", "fake-sk").VerifyCallback(r)
			if !valid || err != nil {
				w.WriteHeader(401)
				return
			}
			raw, _ := base64.URLEncoding.DecodeString(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
			objectKey := strings.TrimPrefix(string(raw), "xhub-test:")
			body, found := objects[objectKey]
			if !found {
				w.WriteHeader(612)
				_, _ = w.Write([]byte(`{"error":"missing"}`))
				return
			}
			if strings.HasPrefix(r.URL.Path, "/delete/") {
				delete(objects, objectKey)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hash": kodoContentHash(body), "fsize": len(body)})
		default:
			w.WriteHeader(404)
		}
	}))
	defer objectServer.Close()
	host := strings.TrimPrefix(objectServer.URL, "https://")
	// 工厂仅在本测试作用域注入 TLS 端点，秘密仍从真实版本配置解密，生产构造保持原样。
	factory := func(c *iam.ArchiveConfig) (archiveObjectStore, error) {
		actual, err := newKodoArchive(c, cfg.GeneralSettings.MasterKey)
		if err != nil {
			return nil, err
		}
		k := actual.(*kodoArchive)
		if k.bucket != "xhub-test" {
			return nil, fmt.Errorf("fixture bucket required")
		}
		k.cfg = &storage.Config{UseHTTPS: true, Region: &storage.Region{SrcUpHosts: []string{host}, RsHost: host}}
		k.client = &qclient.Client{Client: objectServer.Client()}
		return k, nil
	}
	// 故障只注入隔离测试 HTTP 边界；分页主体和成功结果仍来自真实 IAM 查询。
	var usageFault string
	var usageFaultMu sync.Mutex
	gatewayHandler := s.Handler()
	fixtureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usageFaultMu.Lock()
		fault := usageFault
		usageFaultMu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/gw")
		failed := strings.Contains(path, "/daily/activity") && ((fault == "aggregated" && strings.HasSuffix(path, "/aggregated")) || (fault == "page2" && (strings.HasSuffix(path, "/aggregated") || r.URL.Query().Get("page") == "2")) || (fault == "gateway" && path == "/gateway/daily/activity"))
		if failed {
			util.WriteError(w, 503, "unavailable", "isolated usage failure")
			return
		}
		gatewayHandler.ServeHTTP(w, r)
	})
	ui, _ := url.Parse("http://127.0.0.1:" + os.Getenv("E2E_UI_PORT"))
	proxy := httputil.NewSingleHostReverseProxy(ui)
	consoleServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/gw/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/gw")
			fixtureHandler.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			fixtureHandler.ServeHTTP(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer consoleServer.Close()
	// SAML 独立 TLS 入口全部经过真实 Next 代理，包括 /auth 路由；HTTP 场景直接使用 Next 端口。
	// 不复用上方历史 OIDC 的直接路由，以免夹具掩盖生产 /auth rewrite 缺失。
	samlConsole := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(ui))
	defer samlConsole.Close()
	samlIDP := samltest.New(t)
	httpSAMLIDP := samltest.NewHTTP(t)
	expiredSAMLIDP := samltest.NewExpired(t)
	// 刷新下载走独立真实 TLS 服务；只返回本地 IdP 的公开元数据，故障端点用于观察保留旧配置。
	refreshSource := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/fail") {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		if r.URL.Path == "/expired" {
			_, _ = io.WriteString(w, expiredSAMLIDP.Metadata())
		} else {
			_, _ = io.WriteString(w, httpSAMLIDP.Metadata())
		}
	}))
	defer refreshSource.Close()
	identities := map[string]string{}
	spoolControl, closeSpoolControl := newBrowserUsageSpoolControl(s, dir)
	defer closeSpoolControl()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if browserBillingPendingControl(w, r, s) || spoolControl(w, r) {
			return
		}
		switch r.URL.Path {
		case "/saml/register":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
			if err != nil || samlIDP.Register(string(raw)) != nil || httpSAMLIDP.Register(string(raw)) != nil || expiredSAMLIDP.Register(string(raw)) != nil {
				http.Error(w, "invalid SP metadata", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"registered": true})
		case "/saml/mode":
			samlIDP.SetMode(r.URL.Query().Get("value"))
			httpSAMLIDP.SetMode(r.URL.Query().Get("value"))
			expiredSAMLIDP.SetMode(r.URL.Query().Get("value"))
			_ = json.NewEncoder(w).Encode(map[string]bool{"configured": true})
		case "/saml/last":
			if r.URL.Query().Get("http") == "true" {
				_ = json.NewEncoder(w).Encode(httpSAMLIDP.Last())
			} else {
				_ = json.NewEncoder(w).Encode(samlIDP.Last())
			}
		case "/monitor/tps/legacy":
			// 只在此测试程序的私有 schema 写入旧 TPS 并移除完成标记；浏览器随后通过生产 Migrate 验证修复。
			// 不改写账单、费用、Token 或其他统计；POST 以外拒绝，运行器最终删除专属 schema。
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if _, err := db.Engine.Context(r.Context()).Exec("UPDATE model_monitor_buckets SET output_tps_sum=999999,output_tps_samples=2; DELETE FROM model_monitor_migrations"); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})

		case "/organization/budget/due":
			// 私有测试 schema 中推进本场景组织的到期时刻，可保留旧周期积压；实际重置仍走生产事务、审计与准入。
			var body struct {
				ID      string `json:"organization_id"`
				Pending bool   `json:"pending_previous"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "POST organization_id required", 400)
				return
			}
			result, err := db.Engine.Exec("UPDATE organizations SET budget_reset_at=? WHERE id=? AND name LIKE 'budget-%' AND budget_duration<>''", time.Now().Add(-time.Second), body.ID)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				http.Error(w, "fixture organization required", 400)
				return
			}
			if body.Pending && s.Live != nil {
				var org iam.Organization
				if _, err := db.Engine.ID(body.ID).Get(&org); err != nil || org.MaxBudget == nil {
					http.Error(w, "fixture budget required", 400)
					return
				}
				old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
				if err := s.Live.EnqueueSpend(live.SpendLog{RequestID: "budget-pending-" + body.ID, OrgID: body.ID, Model: "budget-period-fixture", Spend: *org.MaxBudget, SpendValid: true, Start: old, End: old}); err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "/usage/failure":
			// 只切换本地 HTTP 故障模式，不替换任何成功响应或生产页面数据。
			var body struct {
				Mode string `json:"mode"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "POST mode required", 400)
				return
			}
			if body.Mode != "" && body.Mode != "aggregated" && body.Mode != "page2" && body.Mode != "gateway" {
				http.Error(w, "unsupported mode", 400)
				return
			}
			usageFaultMu.Lock()
			usageFault = body.Mode
			usageFaultMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "/usage/seed-pages":
			// 私有 schema 写入确定的 52 日历史账单，浏览器通过真实分页路由读取；只能用于本场景测试账号。
			var body struct {
				UserID string `json:"user_id"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "POST user_id required", 400)
				return
			}
			user, err := db.GetUser(r.Context(), body.UserID)
			if err != nil || user == nil || !strings.HasPrefix(user.Email, "usage-state-") {
				http.Error(w, "fixture user required", 400)
				return
			}
			var records []iam.UsageRecord
			for i := 0; i < 52; i++ {
				records = append(records, iam.UsageRecord{RequestID: body.UserID + fmt.Sprint("-page-", i), UserID: body.UserID, OwnerType: iam.OwnerPersonal, Model: "usage-state-history", TS: time.Now().UTC().Add(-time.Duration(i) * 24 * time.Hour), Cost: 0.01, PromptTokens: 10, CompletionTokens: 1, HTTPStatus: 200, Status: "success"})
			}
			if err := db.RecordUsage(r.Context(), records); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]int{"records": len(records)})
		case "/usage/legacy", "/usage/migrate":
			// 仅在已校验的 e2e_ 私有 schema 中复现旧表缺列；恢复必须走生产迁移入口。
			// 控制端只编入测试程序，重复迁移不得重算历史费用；测试结束由运行器删库清理。
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var err error
			if r.URL.Path == "/usage/legacy" {
				_, err = db.Engine.Context(r.Context()).Exec("ALTER TABLE usage_events DROP COLUMN session_source, DROP COLUMN reasoning_effort")
			} else {
				err = db.Migrate(r.Context())
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "/conversation/restart":
			// 仅测试控制端：两轮请求之间丢弃所有内存/Redis会话状态，以真实PG恢复续聊。
			// 不编入生产程序；调用者必须等待前轮请求结束，私有schema由运行器回收。
			if r.Method != http.MethodPost {
				w.WriteHeader(405)
				return
			}
			s.State = *affinity.NewDurable(nil, st)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case "/session/ttl":
			// 仅缩短当前测试会话的存储 TTL；不删除或撤销凭据，让真实鉴权链等待到期后拒绝。
			var body struct {
				Session string `json:"session"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
				http.Error(w, "session required", 400)
				return
			}
			record, err := st.GetKV("ui_session", body.Session)
			if err != nil || record == nil {
				http.Error(w, "fixture session not found", 404)
				return
			}
			expires := time.Now().Add(2 * time.Second)
			record["expires_at"] = expires.UTC().Format(time.RFC3339Nano)
			encoded, err := json.Marshal(record)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err = st.PutKV("ui_session", body.Session, string(encoded)); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"expires_at": expires})
		case "/identity":
			org, err := db.CreateOrg(ctx, testActor, fmt.Sprintf("Browser identity %d", time.Now().UnixNano()), nil)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			c, _, err := db.CreateDirectoryConnection(ctx, testActor, org.ID, "Browser identity")
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err = db.ConfigureDirectory(ctx, testActor, c.ID, true, ""); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			_, user, err := db.ProvisionDirectoryUser(ctx, c.ID, "employee-sub", "browser-"+c.ID+"@example.com", "Browser Employee", true)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			identities[org.ID] = c.ID
			_ = json.NewEncoder(w).Encode(map[string]any{"organization_id": org.ID, "connection_id": c.ID, "user": user})
		case "/identity/cleanup":
			// 仅回收控制端创建的租户；保留锚点是产品语义，测试清理由私有 schema 内事务执行。
			orgID := r.URL.Query().Get("organization_id")
			connectionID, found := identities[orgID]
			if r.Method != "POST" || !found {
				http.Error(w, "fixture identity required", 400)
				return
			}
			tx := db.Engine.NewSession()
			defer tx.Close()
			if err := tx.Begin(); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			statements := []string{
				"DELETE FROM enterprise_login_transactions WHERE connection_id=?",
				"DELETE FROM enterprise_login_identities WHERE connection_id=?",
				"DELETE FROM enterprise_oidc WHERE connection_id=?",
				"DELETE FROM directory_team_mappings WHERE connection_id=?",
				"DELETE FROM directory_group_members WHERE connection_id=?",
				"DELETE FROM directory_groups WHERE connection_id=?",
				"DELETE FROM users WHERE id IN (SELECT user_id FROM directory_users WHERE connection_id=?)",
			}
			// 用户外键为 RESTRICT，先读 ID 再清除锚点，最后删除用户和组织。
			var rows []struct{ UserID string }
			if err := tx.SQL("SELECT user_id FROM directory_users WHERE connection_id=?", connectionID).Find(&rows); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			statements = append(statements[:6], "DELETE FROM directory_users WHERE connection_id=?", "DELETE FROM directory_connections WHERE id=?")
			for _, statement := range statements {
				if _, err := tx.Exec(statement, connectionID); err != nil {
					_ = tx.Rollback()
					http.Error(w, err.Error(), 500)
					return
				}
			}
			for _, row := range rows {
				if _, err := tx.Exec("DELETE FROM users WHERE id=?", row.UserID); err != nil {
					_ = tx.Rollback()
					http.Error(w, err.Error(), 500)
					return
				}
			}
			if _, err := tx.Exec("DELETE FROM organizations WHERE id=?", orgID); err != nil {
				_ = tx.Rollback()
				http.Error(w, err.Error(), 500)
				return
			}
			if err := tx.Commit(); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			delete(identities, orgID)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/mode":
			mu.Lock()
			mode = r.URL.Query().Get("value")
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/archive/reset":
			// 仅测试二进制且启动时已验证独占 e2e schema；单 worker 场景回收前条测试的队列与内存对象。
			// 不修改产品处理链或业务状态，也不能从生产网关访问，避免伪凭据积压遮蔽当前请求。
			if _, err := db.Engine.Context(ctx).Exec("DELETE FROM archive_events"); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			mu.Lock()
			clear(objects)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/archive/process":
			count, err := s.runArchiveBatch(ctx, 200, factory)
			message := ""
			if err != nil {
				message = err.Error()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"processed": count, "error": message})
		case "/objects":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(objects)
		default:
			w.WriteHeader(404)
		}
	}))
	defer control.Close()
	metadata, _ := json.Marshal(map[string]string{"issuer": issuer, "console": consoleServer.URL, "control": control.URL, "saml_console": samlConsole.URL, "saml_refresh_source": refreshSource.URL, "saml_metadata": samlIDP.Metadata(), "saml_metadata_http": httpSAMLIDP.Metadata(), "saml_metadata_expired": expiredSAMLIDP.Metadata()})
	if err = os.WriteFile(filepath.Join(dir, "enterprise-fixture.json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:" + os.Getenv("E2E_GW_PORT"), Handler: fixtureHandler}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	<-stop
	shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
