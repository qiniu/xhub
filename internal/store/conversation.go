package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// conversationKind 隔离持久会话状态与控制台登录及供应商配置；记录随数据库备份保存，不受缓存TTL影响。
const conversationKind = "conversation_state"

// ReadConversation 读取持久续聊状态；参数为调用超时上下文和已做身份隔离的键。
// 供affinity读取原连接证明和历史；未命中返回空串，数据库失败原样返回，不降级猜测归属。
func (s *Store) ReadConversation(ctx context.Context, key string) (string, error) {
	if s == nil || s.DB == nil {
		return "", fmt.Errorf("conversation storage unavailable")
	}
	var value string
	err := s.DB.QueryRowContext(ctx, `SELECT body FROM kv WHERE kind=$1 AND id=$2`, conversationKind, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// CompareConversation 原子比较并保存会话归属；参数old为空表示仅首次创建，value为完整序列化值。
// 供affinity阻止并发请求重新绑定签名历史；返回实际赢家，数据库错误不当作未命中，不使用ORM缓存。
func (s *Store) CompareConversation(ctx context.Context, key, old, value string) (string, error) {
	if s == nil || s.DB == nil {
		return "", fmt.Errorf("conversation storage unavailable")
	}
	if old == "" {
		var actual string
		err := s.DB.QueryRowContext(ctx, `INSERT INTO kv(kind,id,body,created_at) VALUES($1,$2,$3,$4)
   ON CONFLICT(kind,id) DO UPDATE SET body=kv.body RETURNING body`, conversationKind, key, value, time.Now().UTC().Format(time.RFC3339)).Scan(&actual)
		return actual, err
	}
	var actual string
	err := s.DB.QueryRowContext(ctx, `UPDATE kv SET body=$1 WHERE kind=$2 AND id=$3 AND body=$4 RETURNING body`, value, conversationKind, key, old).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		return s.ReadConversation(ctx, key)
	}
	return actual, err
}

// WriteConversation 保存成功响应的历史或归属；参数为超时上下文、隔离键及JSON载荷。
// 供affinity在发布引用前调用；失败返回错误，不能向客户端发布无法恢复的成功引用。
func (s *Store) WriteConversation(ctx context.Context, key, value string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("conversation storage unavailable")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO kv(kind,id,body,created_at) VALUES($1,$2,$3,$4)
 ON CONFLICT(kind,id) DO UPDATE SET body=EXCLUDED.body`, conversationKind, key, value, time.Now().UTC().Format(time.RFC3339))
	return err
}
