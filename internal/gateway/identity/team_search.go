package identity

import (
	"net/url"
	"strings"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// teamSearch 保存目录搜索条件；只用于已通过权限筛选的团队，不扩大可见范围。
type teamSearch struct{ text, idMatch string }

// parseTeamSearch 解析列表查询，缺省 ID 精确匹配，支持显式前缀匹配；非法模式返回 ErrInvalid。
// 参数为 URL 查询，返回规范化条件，供两种团队列表接口使用；不读取数据库或修改请求。
func parseTeamSearch(q url.Values) (teamSearch, error) {
	s := teamSearch{text: strings.ToLower(strings.TrimSpace(q.Get("search"))), idMatch: q.Get("search_team_id_match")}
	switch s.idMatch {
	case "", "exact":
		s.idMatch = "exact"
	case "prefix":
	default:
		return s, iam.ErrInvalid
	}
	return s, nil
}

// matches 对已授权团队进行名称子串或 ID 精确/前缀匹配；空搜索匹配全部，Unicode 名称大小写忽略。
// 参数为团队 ID 和名称，返回匹配结果；调用方先应用权限及其他过滤，再分页，无副作用。
func (s teamSearch) matches(id, name string) bool {
	if s.text == "" || strings.Contains(strings.ToLower(name), s.text) {
		return true
	}
	id = strings.ToLower(id)
	if s.idMatch == "prefix" {
		return strings.HasPrefix(id, s.text)
	}
	return id == s.text
}
