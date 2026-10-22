package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestTeamlessIdentityAdmission 验证无团队主体的预算准入；前置独立数据库和真实个人密钥。
// 参数 t 为测试上下文，返回无；会话及密钥的空预算放行，有限预算放行，零预算拒绝，清空恢复。
// 数据库故障必须拒绝；fixture 清理全部数据，不调用外部供应商。
func TestTeamlessIdentityAdmission(t *testing.T) {
	f := newPermFixture(t)
	ctx := context.Background()
	by := iam.Actor{ID: f.admin.user.ID, Kind: "session"}
	k, _, err := f.db.CreateKey(ctx, by, iam.KeyInput{OwnerType: iam.OwnerPersonal, UserID: f.member.user.ID})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{IAM: f.db}
	zero, finite := 0.0, 10.0
	for _, tc := range []struct {
		name    string
		budget  *float64
		allowed bool
		status  int
	}{
		{"blank", nil, true, 200}, {"finite", &finite, true, 200},
		{"zero", &zero, false, 403}, {"cleared", nil, true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.db.AdminUpdateUser(ctx, by, f.member.user.ID, iam.UserUpdate{MaxBudget: &tc.budget}); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []authz.Kind{authz.KindSession, authz.KindKey} {
				p := &auth.Principal{Kind: kind, UserID: f.member.user.ID}
				if kind == authz.KindKey {
					p.Key, p.KeyID, p.Hash = k, k.ID, k.TokenHash
				}
				w := httptest.NewRecorder()
				if allowed := s.enforceIdentityLimits(w, "/chat/completions", p, "", 32); allowed != tc.allowed || w.Code != tc.status {
					t.Fatalf("无团队 %s 预算准入错误: allowed=%v status=%d body=%s", kind, allowed, w.Code, w.Body.String())
				}
			}
		})
	}
	w := httptest.NewRecorder()
	if (&Server{}).enforceIdentityLimits(w, "/chat/completions", &auth.Principal{}, "", 0) || w.Code != 503 {
		t.Fatalf("身份库缺失应停止推理: status=%d", w.Code)
	}
}

// TestTeamlessKeyBudget 验证独立密钥额度边界；前置隔离数据库、无团队账号和个人密钥。
// 参数 t 为测试上下文，返回无；验证不限额、零额度、恢复、停用、删除和密钥路由模板；fixture 清理 schema。
func TestTeamlessKeyBudget(t *testing.T) {
	f := newPermFixture(t)
	ctx := context.Background()
	by := iam.Actor{ID: f.admin.user.ID, Kind: "session"}
	route, err := f.db.CreateRouteTemplate(ctx, by, iam.TemplateOwner{}, "personal-template", "{}")
	if err != nil {
		t.Fatal(err)
	}
	template := route.ID
	selection := &template
	k, _, err := f.db.CreateKey(ctx, by, iam.KeyInput{OwnerType: iam.OwnerPersonal, UserID: f.member.user.ID, RouteTemplateID: &selection})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{IAM: f.db}
	p := &auth.Principal{KeyID: k.ID, Hash: k.TokenHash}
	if err := s.keyBudgetOK(ctx, p); err != nil {
		t.Fatalf("不限额个人密钥被拒绝: %v", err)
	}
	if p.RouteTemplateID != template || p.RouteTemplateSource != "key" || p.OrgID != "" {
		t.Fatalf("独立密钥路由归属错误: %+v", p)
	}
	zero := 0.0
	ceiling := &zero
	if _, err := f.db.AdminUpdateUser(ctx, by, f.member.user.ID, iam.UserUpdate{MaxBudget: &ceiling}); err != nil {
		t.Fatal(err)
	}
	if err := s.keyBudgetOK(ctx, p); err != (errBudget{scope: "User"}) {
		t.Fatalf("用户零额度未拦截: %v", err)
	}
	ceiling = nil
	if _, err := f.db.AdminUpdateUser(ctx, by, f.member.user.ID, iam.UserUpdate{MaxBudget: &ceiling}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.UpdateKey(ctx, by, k.ID, iam.KeyInput{MaxBudget: &zero}); err != nil {
		t.Fatal(err)
	}
	if err := s.keyBudgetOK(ctx, p); err != (errBudget{scope: "Key"}) {
		t.Fatalf("密钥零额度未拦截: %v", err)
	}
	if _, err := f.db.UpdateKey(ctx, by, k.ID, iam.KeyInput{}); err != nil {
		t.Fatal(err)
	}
	if err := s.keyBudgetOK(ctx, p); err != nil {
		t.Fatalf("额度恢复后仍拒绝: %v", err)
	}
	status := iam.StatusDisabled
	if _, err := f.db.AdminUpdateUser(ctx, by, f.member.user.ID, iam.UserUpdate{Status: &status}); err != nil {
		t.Fatal(err)
	}
	if err := s.keyBudgetOK(ctx, p); !errors.Is(err, errKeyUnusable) {
		t.Fatalf("停用用户密钥未拒绝: %v", err)
	}
	if err := f.db.DeleteKey(ctx, by, k.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.keyBudgetOK(ctx, p); !errors.Is(err, errKeyGone) {
		t.Fatalf("已删除密钥未拒绝: %v", err)
	}
}

// TestBudgetRefusalStatus 验证额度链耗尽返回403且保留错误类型，存储和身份错误不被误分类。
// 参数t为测试上下文；覆盖五层预算、身份错误和存储故障输入；纯内存响应无需清理。
func TestBudgetRefusalStatus(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/bypass/anthropic/v1/messages"} {
		for _, scope := range []string{"User", "Key", "Project", "Team", "Organization"} {
			w := httptest.NewRecorder()
			budgetRefusal(w, path, errBudget{scope: scope})
			if w.Code != 403 || !strings.Contains(w.Body.String(), "budget_exceeded") || !strings.Contains(w.Body.String(), scope) {
				t.Fatalf("预算应返回403及原始层级: %d %s", w.Code, w.Body.String())
			}
		}
		for _, tc := range []struct {
			err  error
			code int
		}{{iam.ErrQuotaUnavailable, 503}, {errKeyGone, 401}, {errKeyUnusable, 401}} {
			w := httptest.NewRecorder()
			budgetRefusal(w, path, tc.err)
			if w.Code != tc.code {
				t.Fatalf("非预算错误被改写: %v %d", tc.err, w.Code)
			}
		}
	}
}
