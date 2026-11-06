package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
)

func TestIdempotencyScopesHashesAndExpires(t *testing.T) {
	s, db, user, sessions := idempotencyFixture(t)
	body := []byte(`{"value":1}`)
	lease, hit, conflict := s.beginIdempotency(idemTestRequest(sessions[0]), "same", body)
	if lease == nil || hit != nil || conflict {
		t.Fatalf("first lookup lease=%v hit=%v conflict=%v", lease != nil, hit != nil, conflict)
	}
	lease.complete(&idemRec{Code: http.StatusCreated, Body: []byte("created")})
	_, hit, conflict = s.beginIdempotency(idemTestRequest(sessions[0]), "same", body)
	if hit == nil || hit.Code != http.StatusCreated || conflict {
		t.Fatalf("replay hit=%v conflict=%v", hit != nil, conflict)
	}
	_, _, conflict = s.beginIdempotency(idemTestRequest(sessions[0]), "same", []byte(`{"value":2}`))
	if !conflict {
		t.Fatal("same key with a different payload did not conflict")
	}
	other, hit, _ := s.beginIdempotency(idemTestRequest(sessions[1]), "same", body)
	if other == nil || hit != nil {
		t.Fatal("two UI sessions shared an idempotency namespace")
	}
	other.abort()
	for key := range s.idem {
		if strings.Contains(key, sessions[0]) || strings.Contains(key, sessions[1]) {
			t.Fatalf("stored session credential in idempotency key %q", key)
		}
	}

	teamID := testTeam(t, db, user.ID)
	key, plain, err := db.CreateKey(context.Background(), iam.Actor{ID: user.ID, Kind: "session"}, iam.KeyInput{Name: "idem", UserID: user.ID, TeamID: teamID, OwnerType: iam.OwnerPersonal})
	if err != nil {
		t.Fatal(err)
	}
	keyReq := idemTestRequest(plain)
	keyLease, _, _ := s.beginIdempotency(keyReq, "key-replay", body)
	if keyLease == nil {
		t.Fatal("valid key did not acquire idempotency lease")
	}
	keyLease.complete(&idemRec{Code: http.StatusOK, Body: []byte("ok")})
	for stored := range s.idem {
		if strings.Contains(stored, plain) {
			t.Fatalf("stored raw API key in idempotency key %q", stored)
		}
	}
	if _, err := db.SetKeyStatus(context.Background(), iam.Actor{ID: user.ID, Kind: "session"}, key.ID, iam.StatusRevoked); err != nil {
		t.Fatal(err)
	}
	if replayLease, replay, _ := s.beginIdempotency(idemTestRequest(plain), "key-replay", body); replayLease != nil || replay != nil {
		t.Fatal("revoked key reached its cached response")
	}

	now := time.Now()
	s.now = func() time.Time { return now }
	s.idemTTL = time.Second
	expiring, _, _ := s.beginIdempotency(idemTestRequest(sessions[0]), "expires", body)
	expiring.complete(&idemRec{Code: http.StatusOK})
	now = now.Add(2 * time.Second)
	afterExpiry, replay, _ := s.beginIdempotency(idemTestRequest(sessions[0]), "expires", body)
	if afterExpiry == nil || replay != nil {
		t.Fatal("expired response was replayed")
	}
	afterExpiry.abort()
}

func TestIdempotencyConcurrentRequestWaitsForOwner(t *testing.T) {
	s, _, _, sessions := idempotencyFixture(t)
	body := []byte(`{"value":1}`)
	owner, _, _ := s.beginIdempotency(idemTestRequest(sessions[0]), "concurrent", body)
	result := make(chan *idemRec, 1)
	go func() {
		_, replay, _ := s.beginIdempotency(idemTestRequest(sessions[0]), "concurrent", body)
		result <- replay
	}()
	select {
	case <-result:
		t.Fatal("concurrent request did not wait for the owner")
	case <-time.After(20 * time.Millisecond):
	}
	owner.complete(&idemRec{Code: http.StatusAccepted, Body: []byte("once")})
	select {
	case replay := <-result:
		if replay == nil || replay.Code != http.StatusAccepted || string(replay.Body) != "once" {
			t.Fatalf("concurrent replay %#v", replay)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent replay remained blocked")
	}
}

// TestIdempotencyCrossInstanceAndStorageFailure 验证跨实例重复、不同正文和存储失败的响应。
// 使用独立 PostgreSQL 与隔离会话；故障约束和数据库由测试框架清理，不涉及外部供应商。
func TestIdempotencyCrossInstanceAndStorageFailure(t *testing.T) {
	first, db, _, sessions := idempotencyFixture(t)
	body := []byte(`{"value":1}`)
	owner, _, conflict := first.beginIdempotency(idemTestRequest(sessions[0]), "cross-instance", body)
	if owner == nil || conflict {
		t.Fatal("首次调用未取得资格")
	}
	owner.complete(&idemRec{Code: 200, Body: []byte("private-response")})
	other := &Server{Cfg: first.Cfg, IAM: db, Authz: first.Authz, sessions: first.sessions, idem: map[string]*idemRec{}, idemTTL: defaultIdempotencyTTL, now: time.Now}
	lease, hit, conflict := other.beginIdempotency(idemTestRequest(sessions[0]), "cross-instance", body)
	if lease != nil || conflict || hit == nil || hit.Code != 409 || !hit.Synthetic || !strings.Contains(string(hit.Body), "idempotency_result_unavailable") || strings.Contains(string(hit.Body), "private-response") {
		t.Fatalf("跨实例重放结果错误: %#v", hit)
	}
	if _, _, conflict := other.beginIdempotency(idemTestRequest(sessions[0]), "cross-instance", []byte(`{"value":2}`)); !conflict {
		t.Fatal("不同正文应冲突")
	}
	w := httptest.NewRecorder()
	lw := &statusRecorder{ResponseWriter: w}
	other.writeIdempotentResponse(lw, lw, hit, "/v1/chat/completions")
	if w.Code != 409 || lw.usageRecorded || !strings.Contains(lw.note, "idempotency_result_unavailable") {
		t.Fatal("跨实例拒绝应保留错误日志路径")
	}
	if _, err := db.Engine.Exec("ALTER TABLE inference_idempotency_fences ADD CONSTRAINT test_reject CHECK (false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	lease, hit, conflict = other.beginIdempotency(idemTestRequest(sessions[0]), "storage-unavailable", body)
	if lease != nil || conflict || hit == nil || hit.Code != 503 || !hit.Synthetic || !strings.Contains(string(hit.Body), "idempotency_unavailable") {
		t.Fatalf("存储故障未关闭: %#v", hit)
	}
}

func idempotencyFixture(t *testing.T) (*Server, *iam.DB, *iam.User, []string) {
	t.Helper()
	db := testIdentityStore(t)
	user, err := db.CreateUser(context.Background(), iam.Actor{Kind: "system"}, iam.UserInput{Email: "idem@example.com", Name: "Idem", Password: "password123", Role: iam.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{}, IAM: db, Authz: authz.New(db), sessions: map[string]sessionRec{}, idem: map[string]*idemRec{}, idemTTL: defaultIdempotencyTTL, now: time.Now}
	sessions := []string{"sess-idem-a", "sess-idem-b"}
	for _, session := range sessions {
		s.sessions[session] = sessionRec{UserID: user.ID, Version: user.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}
	}
	return s, db, user, sessions
}

func idemTestRequest(credential string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/probe", nil)
	r.Header.Set("Authorization", "Bearer "+credential)
	return r
}
