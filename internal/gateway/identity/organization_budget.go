package identity

import (
	"github.com/sunqirui1987/xhub/internal/iam"
	"net/http"
	"strings"
)

// organizationBudgetField 解析周期部分更新；body 为请求对象，返回 nil 表示未提供、空串指针表示取消及输入错误。
// 创建/更新接口调用；仅接受约定周期或 null，不把非法值静默当作取消，无持久化副作用。
func organizationBudgetField(body map[string]any) (*string, error) {
	raw, ok := body["budget_duration"]
	if !ok {
		return nil, nil
	}
	value := ""
	if raw != nil {
		var valid bool
		value, valid = raw.(string)
		if !valid {
			return nil, iam.ErrInvalid
		}
	}
	if _, err := iam.OrganizationBudgetInterval(value); err != nil {
		return nil, err
	}
	return &value, nil
}

// organizationMatches 对已授权组织应用名称/ID组合筛选；参数为组织和查询条件，返回是否命中。
// OrgList 调用；名称忽略大小写包含匹配、ID 精确匹配，空条件不限制；不能扩大上游已确定的可见范围。
func organizationMatches(org iam.Organization, name, id string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	id = strings.TrimSpace(id)
	return (name == "" || strings.Contains(strings.ToLower(org.Name), name)) && (id == "" || org.ID == id)
}

// firstQueryValue 读取首个非空查询别名；r 为入站请求，keys 为兼容名称，返回原值或空串。
// 组织列表筛选调用；仅读取查询，不允许参数改变授权范围。
func firstQueryValue(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if v := r.URL.Query().Get(key); v != "" {
			return v
		}
	}
	return ""
}
