// Package promptconfig 提供提示词配置服务；不依赖 HTTP、鉴权、模型执行器或预算规则。
package promptconfig

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

// Store 是提示词服务依赖的持久化边界；生产 Store 实现这些方法。
// Get/Delete 以 sql.ErrNoRows 表示缺失，其余错误原样交给 HTTP 适配层脱敏。
type Store interface {
	ListKV(kind string) ([]map[string]any, error)
	GetKV(kind, id string) (map[string]any, error)
	PutKV(kind, id, body string) error
	DeleteKV(kind, id string) error
}

// Service 由兼容层为已授权请求构造；存储固定沿用 prompts 命名空间。
// 不持有 HTTP 上下文和缓存；Store 必须非空，调用方负责鉴权和严格 JSON 校验。
type Service struct {
	Store Store
}

// List 返回规范化的提示词列表；空列表非 nil，存储失败返回错误而非空成功。
// 管理列表调用；复制存储结果，不能将补齐字段写回数据库。
func (s Service) List() ([]map[string]any, error) {
	rows, err := s.Store.ListKV("prompts")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		obj, err := copyObject(row)
		if err != nil {
			return nil, err
		}
		Normalize(obj)
		out = append(out, obj)
	}
	return out, nil
}

// Get 按 id 返回单个规范化提示词；详情适配器将缺失错误映射为 404。
// 存储或编码错误直接返回，不合成默认资源，也不修改原始对象。
func (s Service) Get(id string) (map[string]any, error) {
	row, err := s.Store.GetKV("prompts", id)
	if err != nil {
		return nil, err
	}
	obj, err := copyObject(row)
	if err != nil {
		return nil, err
	}
	Normalize(obj)
	return obj, nil
}

// Create 以 id 和 body 替换保存提示词；空 id 自动生成，保留旧同名覆盖和扩展字段语义。
// 返回规范化的对象；编码或保存失败返回错误，不产生成功响应。
func (s Service) Create(id string, body map[string]any) (map[string]any, error) {
	obj, err := copyObject(body)
	if err != nil {
		return nil, err
	}
	delete(obj, "password")
	// 旧通用目录创建会展开这些历史嵌套字段；迁移只搬职责，不能删除已有兼容行为。
	for _, nest := range []string{"search_tool", "prompt", "guardrail", "policy", "skill", "vector_store"} {
		if inner, ok := obj[nest].(map[string]any); ok {
			for k, v := range inner {
				if obj[k] == nil {
					obj[k] = v
				}
			}
		}
	}
	if id == "" {
		id = "prompt_" + util.CallID()[:12]
	}
	obj["id"], obj["prompt_id"] = id, id
	Normalize(obj)
	return s.save(id, obj)
}

// Update 合并 body 到 id 对应配置；缺失记录沿用 upsert，空 id 生成新名称。
// 管理编辑调用；读取失败不能误当缺失，保存失败不污染读取对象。
func (s Service) Update(id string, body map[string]any) (map[string]any, error) {
	if id == "" {
		id = "prompt_" + util.CallID()[:12]
	}
	row, err := s.Store.GetKV("prompts", id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		row = map[string]any{"id": id, "prompt_id": id, "created_at": time.Now().UTC().Format(time.RFC3339)}
	}
	obj, err := copyObject(row)
	if err != nil {
		return nil, err
	}
	// 提示词保持旧浅层补丁：嵌套参数整体替换，null/零值照常写入，不套用秘密保留规则。
	for key, value := range body {
		if key != "password" {
			obj[key] = value
		}
	}
	obj["id"], obj["prompt_id"] = id, id
	Normalize(obj)
	return s.save(id, obj)
}

// Delete 依次删除 ids，返回实际删除数；缺失项忽略，其他错误立即返回。
// 批量适配器调用；保持非事务语义和重复 ID 的计数方式，不回滚已删除项或伪报整批成功。
func (s Service) Delete(ids []string) (int, error) {
	n := 0
	for _, id := range ids {
		err := s.Store.DeleteKV("prompts", id)
		if err == nil {
			n++
		} else if !errors.Is(err, sql.ErrNoRows) {
			return n, err
		}
	}
	return n, nil
}

// save 编码 obj 并按 id 保存，成功后才返回已保存对象。
// 创建/更新共用；错误交由适配层处理，不把数据库错误或模板正文放入错误响应。
func (s Service) save(id string, obj map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	if err = s.Store.PutKV("prompts", id, string(raw)); err != nil {
		return nil, err
	}
	return obj, nil
}

// copyObject 复制 JSON 对象，使规范化和失败补丁不修改输入或存储共享引用。
// 服务读写调用；nil 变为空对象，非 JSON 值返回编码错误，保留 null/零/未知字段。
func copyObject(obj map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
