package store

import (
	"context"
	"fmt"
	"time"
)

// CreateKVIfAbsent 按复合主键原子创建目录记录；ctx用于请求取消，kind/id为已验证标识，body为编码后的JSON。
// 插件注册调用；返回true仅代表插入成功，false/nil表示重名，其他错误必须上报。冲突不更新旧值，成功后失效ORM缓存。
func (s *Store) CreateKVIfAbsent(ctx context.Context, kind, id, body string) (bool, error) {
	if s == nil || s.DB == nil {
		return false, fmt.Errorf("catalog storage unavailable")
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO kv(kind,id,body,created_at) VALUES($1,$2,$3,$4) ON CONFLICT(kind,id) DO NOTHING`, kind, id, body, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		s.bust(new(kvRow))
	}
	return n > 0, nil
}
