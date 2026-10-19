package usage

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
)

// monitorAccessFixture 记录授权查询的实际来源；仅供单元测试，不替代后台真实 IAM 回归。
type monitorAccessFixture struct {
	called string
	models []string
	err    error
}

// AllowedModelsForUser 接收会话账号并返回预置模型及错误；记录账号来源，无数据库副作用需清理。
func (f *monitorAccessFixture) AllowedModelsForUser(_ context.Context, userID string) ([]string, error) {
	f.called = "user:" + userID
	return f.models, f.err
}

// AllowedModelsForTeam 接收已授权团队并返回预置模型及错误；记录团队来源，无持久化副作用。
func (f *monitorAccessFixture) AllowedModelsForTeam(_ context.Context, teamID string) ([]string, error) {
	f.called = "team:" + teamID
	return f.models, f.err
}

// AllowedModelsForKey 接收当前密钥并返回预置交集及错误；记录 Key ID，测试结束无需外部清理。
func (f *monitorAccessFixture) AllowedModelsForKey(_ context.Context, key *iam.Key) ([]string, error) {
	f.called = "key:" + key.ID
	return f.models, f.err
}

// TestModelMonitorAllowedModels 验证默认会话、显式团队、密钥、管理员及授权失败的来源选择。
// 前置内存 IAM 查询夹具；断言 nil/空集合不混淆、读取错误不吞掉，无外部依赖或清理需求。
func TestModelMonitorAllowedModels(t *testing.T) {
	member := &auth.Principal{Kind: authz.KindSession, Role: iam.RoleUser, UserID: "member"}
	key := &auth.Principal{Kind: authz.KindKey, Key: &iam.Key{ID: "restricted"}}
	storeError := errors.New("authorization storage unavailable")
	for _, tc := range []struct {
		name, teamID, called string
		principal            *auth.Principal
		models               []string
		err                  error
	}{
		{"default session uses current user", "", "user:member", member, []string{"a"}, nil},
		{"explicit authorized team", "watched", "team:watched", member, []string{"b"}, nil},
		{"key ignores team hint", "other", "key:restricted", key, []string{"a"}, nil},
		{"empty intersection denies", "", "user:member", member, []string{}, nil},
		{"inherited unrestricted scope", "", "user:member", member, nil, nil},
		{"user lookup failure", "", "user:member", member, nil, storeError},
		{"team lookup failure", "watched", "team:watched", member, nil, storeError},
		{"key lookup failure", "", "key:restricted", key, nil, storeError},
		{"platform admin keeps catalog", "", "", &auth.Principal{Kind: authz.KindSession, Role: iam.RoleAdmin}, nil, nil},
		{"master remains unrestricted", "", "", &auth.Principal{Kind: authz.KindMaster}, nil, nil},
		{"missing principal rejects", "", "", nil, nil, iam.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &monitorAccessFixture{models: tc.models, err: tc.err}
			got, err := modelMonitorAllowedModels(context.Background(), fixture, tc.principal, tc.teamID)
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.models) || fixture.called != tc.called {
				t.Fatalf("监控模型授权来源或结果错误: got=%v err=%v source=%q want=%v err=%v source=%q", got, err, fixture.called, tc.models, tc.err, tc.called)
			}
		})
	}
	if _, err := modelMonitorAllowedModels(context.Background(), nil, member, ""); !errors.Is(err, iam.ErrInvalid) {
		t.Fatal("缺少授权存储时必须拒绝目录读取", err)
	}
}

// TestModelMonitorOptions 使用无账单配置验证目录去重、连接隔离、许可别名与供应商壳；无持久化及清理需求。
func TestModelMonitorOptions(t *testing.T) {
	list := []config.ModelEntry{
		{ModelName: "a", LiteLLMParams: map[string]any{"model": "actual", "litellm_credential_name": "supplier-a"}},
		{ModelName: "alias-a", LiteLLMParams: map[string]any{"model": "actual", "litellm_credential_name": "supplier-a"}},
		{ModelName: "b", LiteLLMParams: map[string]any{"model": "actual", "litellm_credential_name": "supplier-b"}},
		{ModelName: "shell", LiteLLMParams: map[string]any{"litellm_credential_name": "supplier-c"}, ModelInfo: map[string]any{"role": "provider"}},
		{ModelName: " "},
	}
	for _, tc := range []struct {
		name    string
		list    []config.ModelEntry
		allowed []string
		want    []modelMonitorOption
	}{
		{"unrestricted", list, nil, []modelMonitorOption{{"supplier-a", "actual"}, {"supplier-b", "actual"}, {"supplier-c", ""}}},
		{"restricted alias", list, []string{"b"}, []modelMonitorOption{{"supplier-b", "actual"}}},
		{"denied", list, []string{}, []modelMonitorOption{}},
		{"unknown grant", list, []string{"missing"}, []modelMonitorOption{}},
		{"empty config", nil, nil, []modelMonitorOption{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelMonitorOptions(tc.list, tc.allowed); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("配置筛选目录错误: %+v，期望 %+v", got, tc.want)
			}
		})
	}
}

// TestModelMonitorQuery 验证默认七日、四分之一小时时区、筛选和错误日期；无写入及清理需求。
func TestModelMonitorQuery(t *testing.T) {
	now := time.Date(2026, 1, 2, 20, 0, 0, 0, time.UTC)
	q, tz, err := modelMonitorQuery(httptest.NewRequest("GET", "/?timezone=-345&supplier=a&model=m", nil), now)
	if err != nil || tz != 345 || q.Supplier != "a" || q.Model != "m" || q.To.Sub(q.From) != 7*24*time.Hour || q.To.UTC().Format(time.RFC3339) != "2026-01-03T18:15:00Z" {
		t.Fatalf("默认日期/筛选错误: %+v %d %v", q, tz, err)
	}
	for _, raw := range []string{"timezone=x", "timezone=1", "timezone=900", "start_date=no", "end_date=no", "start_date=2026-02-01&end_date=2026-01-01", "start_date=2026-01-01&end_date=2026-04-01"} {
		if _, _, err := modelMonitorQuery(httptest.NewRequest("GET", "/?"+raw, nil), now); err == nil {
			t.Fatal("非法查询被接受", raw)
		}
	}
}
