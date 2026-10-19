package identity

import (
	"github.com/sunqirui1987/xhub/internal/iam"
	"net/http/httptest"
	"testing"
)

// TestOrganizationBudgetFields 前置内存请求对象；验证缺省、取消、周期及错误类型，无持久化资源。
func TestOrganizationBudgetFields(t *testing.T) {
	for _, tc := range []struct {
		body        map[string]any
		want        string
		absent, bad bool
	}{
		{map[string]any{}, "", true, false}, {map[string]any{"budget_duration": nil}, "", false, false}, {map[string]any{"budget_duration": "30d"}, "30d", false, false},
		{map[string]any{"budget_duration": 30}, "", false, true}, {map[string]any{"budget_duration": "month"}, "", false, true},
	} {
		got, err := organizationBudgetField(tc.body)
		if tc.bad {
			if err == nil {
				t.Fatal("非法周期未拒绝")
			}
			continue
		}
		if err != nil || (got == nil) != tc.absent || got != nil && *got != tc.want {
			t.Fatalf("周期解析错误: %v %v", got, err)
		}
	}
}

// TestOrganizationFilter 前置已授权组织；验证名称包含、精确 ID、组合及空条件；筛选不改变授权集，无副作用。
func TestOrganizationFilter(t *testing.T) {
	o := iam.Organization{ID: "org-123", Name: "FDE Alpha"}
	for _, tc := range []struct {
		name, id string
		want     bool
	}{{"", "", true}, {"fde", "", true}, {"ALPHA", "org-123", true}, {"alpha", "foreign", false}, {"beta", "org-123", false}, {"", "org", false}} {
		if got := organizationMatches(o, tc.name, tc.id); got != tc.want {
			t.Fatalf("组合筛选 %q/%q=%v", tc.name, tc.id, got)
		}
	}
	for _, url := range []string{"/?org_id=org-123", "/?organization_id=org-123"} {
		if got := firstQueryValue(httptest.NewRequest("GET", url, nil), "org_id", "organization_id"); got != o.ID {
			t.Fatal("筛选别名错误", got)
		}
	}
	if got := firstQueryValue(httptest.NewRequest("GET", "/", nil), "org_id"); got != "" {
		t.Fatal("空条件不应新增过滤")
	}
}
