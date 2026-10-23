package store

import (
	"encoding/json"
	"fmt"
	"xorm.io/builder"
)

// FreshConfigValue 不经过ORM缓存读取单个配置，供必须即时生效的灰度总开关使用。
// 参数 namespace/key 为服务端固定配置键；返回值、是否存在及错误，缺失可继承YAML，读取/JSON错误不能当作缺失。
// 无写入副作用；避免多实例或缓存命中掩盖停用和数据库故障。
func (s *Store) FreshConfigValue(namespace, key string) (any, bool, error) {
	if s == nil || s.Engine == nil {
		return nil, false, fmt.Errorf("configuration store unavailable")
	}
	var row configRow
	found, err := s.Engine.NoCache().Where(builder.Eq{"namespace": namespace, "key": key}).Get(&row)
	if err != nil || !found {
		return nil, found, err
	}
	var value any
	if err := json.Unmarshal([]byte(row.ValueJSON), &value); err != nil {
		return nil, true, err
	}
	return value, true, nil
}
