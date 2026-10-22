package identity

import (
	"errors"
	"github.com/sunqirui1987/xhub/internal/iam"
	"net/url"
	"testing"
)

// TestTeamSearch 覆盖空白、中文/大小写、ID 两种模式与非法参数；纯内存条件无持久化和清理。
func TestTeamSearch(t *testing.T) {
	for _, tc := range []struct {
		name, query, mode, id, alias string
		want, invalid                bool
	}{
		{name: "empty", query: "  ", want: true},
		{name: "chinese", query: "研发", alias: "研发团队", want: true},
		{name: "case and whitespace", query: " ALPHA ", alias: "Alpha team", want: true},
		{name: "name miss", query: "研发", alias: "运营团队"},
		{name: "exact default", query: "ABC", id: "abc", want: true},
		{name: "exact not prefix", query: "abc", id: "abcdef"},
		{name: "prefix", query: "ABC", mode: "prefix", id: "abcdef", want: true},
		{name: "not suffix", query: "def", mode: "prefix", id: "abcdef"},
		{name: "invalid", query: "abc", mode: "contains", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parseTeamSearch(url.Values{"search": {tc.query}, "search_team_id_match": {tc.mode}})
			if tc.invalid {
				if !errors.Is(err, iam.ErrInvalid) {
					t.Fatalf("非法模式应拒绝: %v", err)
				}
				return
			}
			if err != nil || s.matches(tc.id, tc.alias) != tc.want {
				t.Fatalf("搜索匹配不符: %+v err=%v", tc, err)
			}
		})
	}
}
