package guard

import (
	"encoding/json"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
)

// managementStore 是配置服务的窄存储边界；生产 Store 实现它，测试可注入确定性故障。
// 只使用 guardrails 命名空间；缺失和底层错误原样返回 HTTP 适配层处理。
type managementStore interface {
	ListKV(string) ([]map[string]any, error)
	GetKV(string, string) (map[string]any, error)
	PutKV(string, string, string) error
	DeleteKV(string, string) error
}

// managementService 管理已授权护栏配置，不依赖 HTTP、预算或上游执行器。
// Store 必须非 nil；Configured 每次提供 YAML 规则副本。HTTP 适配器必须持有 manageMu
// 覆盖 Get、请求解析及 Save/Delete，保持既有错误优先级和并发重名校验的原子性。
type managementService struct {
	Store      managementStore
	Configured func() []map[string]any
}

// managementError 表示可公开的校验或冲突错误；存储错误不得包装成此类型泄露给客户端。
type managementError struct {
	Status        int
	Code, Message string
}

// Error 返回业务错误正文，供标准 error 接口使用；无副作用。
func (e *managementError) Error() string { return e.Message }

// Get 读取 id 对应的原始配置，供 HTTP 层在解析正文前确定资源存在性。
// 不补齐字段、不吞掉缺失或存储故障；返回对象仅用于当前请求。
func (s managementService) Get(id string) (map[string]any, error) {
	return s.Store.GetKV("guardrails", id)
}

// Delete 删除已读取确认的 id；调用方持有管理写锁，错误由 HTTP 层统一映射。
func (s managementService) Delete(id string) error { return s.Store.DeleteKV("guardrails", id) }

// List 合并 YAML 副本与数据库规则并排序，仅供管理接口及 storedRules 使用。
// 执行层 listGuardrails 独立加载，并做名称歧义拒绝和全局默认继承；不能把其结果用于可编辑配置。
// 数据库失败返回 nil 和原错误，禁止把部分 YAML 结果当完整列表。
func (s managementService) List() ([]map[string]any, error) {
	out := s.Configured()
	rows, err := s.Store.ListKV("guardrails")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, normalizeRule(row))
	}
	sortRules(out)
	return out, nil
}

// Save 合并 body 到已读取的 row，校验规则与名称后持久化，返回脱敏的公开对象。
// id 为空创建新记录；row/body 来自当前 HTTP 请求，允许原地合并。调用方持有 manageMu，
// 保持读改写与重名检查原子；遮蔽或空凭据保留旧值，显式 null 删除，失败不返回成功结果。
func (s managementService) Save(id string, row, body map[string]any) (map[string]any, error) {
	if inner, ok := body["guardrail"].(map[string]any); ok {
		body = inner
	}
	if row == nil {
		row = map[string]any{"created_at": time.Now().UTC().Format(time.RFC3339)}
	}
	for key, value := range body {
		if key == "id" || key == "guardrail_id" || key == "created_at" || key == "guardrail_definition_location" {
			continue
		}
		if key == "litellm_params" {
			patch, ok := value.(map[string]any)
			if !ok {
				return nil, &managementError{400, "invalid_request", "litellm_params must be an object"}
			}
			params, _ := row[key].(map[string]any)
			if params == nil {
				params = map[string]any{}
			}
			for name, v := range patch {
				// 浏览器拿到的掩码或留空表示保留原凭据；显式 null 才删除。
				if secretFields[name] && (v == credentialMask || v == "") && params[name] != nil {
					continue
				}
				if v == nil {
					delete(params, name)
				} else {
					params[name] = v
				}
			}
			row[key] = params
		} else {
			row[key] = value
		}
	}
	if err := Validate(row); err != nil {
		return nil, &managementError{400, "invalid_request", err.Error()}
	}
	rows, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, existing := range rows {
		if ruleIdentifies(existing, findingName(row)) && str(existing["guardrail_id"]) != id {
			return nil, &managementError{409, "conflict", "guardrail name conflicts with an existing name or id"}
		}
	}
	if id == "" {
		id = "guardrail_" + util.CallID()[:12]
	}
	row["id"], row["guardrail_id"] = id, id
	row["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	row["guardrail_definition_location"] = "db"
	raw, err := json.Marshal(row)
	if err != nil {
		return nil, &managementError{400, "invalid_request", err.Error()}
	}
	if err := s.Store.PutKV("guardrails", id, string(raw)); err != nil {
		return nil, err
	}
	return publicRule(normalizeRule(row)), nil
}
