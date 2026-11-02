package iam

import (
	"context"
	"errors"
	"strings"

	"xorm.io/builder"
)

// LogOptionDeployment 表示当前允许使用的公开模型与供应商连接组合，不含上游凭证或地址之外的秘密。
type LogOptionDeployment struct{ Model, Supplier string }

// ListLogFilterOptions 在日志授权和时间范围内读取单维候选；q 为已限制的查询，field 为白名单字段，
// search 为字面子串，pairs 为已启用且授权的模型连接；page/size 为调用方已校验的正整数。
// 返回稳定排序的候选、是否有下一页和数据库错误。模型或供应商查询额外按 pairs 收窄，空列表返回空集；
// 仅日志筛选候选接口调用，不读取正文、不返回明文密钥，也不写入任何数据。
func (db *DB) ListLogFilterOptions(ctx context.Context, q UsageQuery, field, search string, page, size int, pairs []LogOptionDeployment) ([]string, bool, error) {
	columns := map[string]string{"model": "model", "key_alias": "key_alias", "key_hash": "key_hash", "supplier": "supplier", "inbound_endpoint": "inbound_endpoint"}
	column, ok := columns[field]
	if !ok || q.Cond == nil || q.From.IsZero() || q.To.IsZero() || page < 1 || size < 1 || size > 50 {
		return nil, false, errors.New("invalid log filter options query")
	}
	if (field == "model" || field == "supplier") && len(pairs) == 0 {
		return []string{}, false, nil
	}
	s := q.scopedSession(db, ctx, "usage_events")
	defer s.Close()
	s = s.And(column+" <> ?", "")
	if search != "" {
		s = s.And("position(lower(?) in lower("+column+")) > 0", search)
	}
	if field == "model" || field == "supplier" {
		terms := make([]builder.Cond, 0, len(pairs))
		for _, pair := range pairs {
			if strings.TrimSpace(pair.Model) == "" || (field == "supplier" && strings.TrimSpace(pair.Supplier) == "") {
				continue
			}
			terms = append(terms, builder.Eq{"model": pair.Model, "supplier": pair.Supplier})
		}
		if len(terms) == 0 {
			return []string{}, false, nil
		}
		s = s.And(builder.Or(terms...))
	}
	var rows []struct {
		Value string `xorm:"'value'"`
	}
	err := s.Select("DISTINCT "+column+" AS value").OrderBy(column+" ASC").Limit(size+1, (page-1)*size).Find(&rows)
	if err != nil {
		return nil, false, mapErr(err)
	}
	hasMore := len(rows) > size
	if hasMore {
		rows = rows[:size]
	}
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		values = append(values, row.Value)
	}
	return values, hasMore, nil
}
