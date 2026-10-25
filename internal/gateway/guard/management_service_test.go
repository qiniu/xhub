package guard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// managementMemory 提供只保存 JSON 的独立单测存储；故障按操作注入，不访问数据库。
type managementMemory struct {
	rows    map[string]string
	failure string
	calls   int
}

var managementFault = errors.New("private storage failure")

// ListKV 返回反序列化副本；仅允许当前命名空间，故障不返回部分成功。
func (m *managementMemory) ListKV(kind string) ([]map[string]any, error) {
	if kind != "guardrails" {
		panic(kind)
	}
	if m.failure == "list" {
		return nil, managementFault
	}
	out := []map[string]any{}
	for _, raw := range m.rows {
		var row map[string]any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// GetKV 按 id 返回副本，缺失返回标准缺失错误；便于验证失败不会污染持久化值。
func (m *managementMemory) GetKV(kind, id string) (map[string]any, error) {
	if kind != "guardrails" {
		panic(kind)
	}
	if m.failure == "get" {
		return nil, managementFault
	}
	raw, ok := m.rows[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		return nil, err
	}
	return row, nil
}

// PutKV 记录写入次数和 JSON；注入故障时禁止持久化。
func (m *managementMemory) PutKV(kind, id, raw string) error {
	if kind != "guardrails" {
		panic(kind)
	}
	m.calls++
	if m.failure == "put" {
		return managementFault
	}
	m.rows[id] = raw
	return nil
}

// DeleteKV 删除指定记录；故障保留原值，缺失返回标准错误。
func (m *managementMemory) DeleteKV(kind, id string) error {
	if kind != "guardrails" {
		panic(kind)
	}
	if m.failure == "delete" {
		return managementFault
	}
	if _, ok := m.rows[id]; !ok {
		return sql.ErrNoRows
	}
	delete(m.rows, id)
	return nil
}

// managementFixture 为各单测提供本地规则和隔离存储；无需外部依赖或数据清理。
func managementFixture() (managementService, *managementMemory, map[string]any) {
	m := &managementMemory{rows: map[string]string{}}
	return managementService{Store: m, Configured: func() []map[string]any { return []map[string]any{} }}, m, map[string]any{"guardrail_name": "test-rule", "litellm_params": map[string]any{"guardrail": "local", "blocked_words": []string{"secret"}, "default_on": false, "priority": 0}}
}

// TestManagementServiceLifecycle 验证创建、保留扩展字段、浅层补丁、稳定标识、列表与删除；全部为隔离内存数据。
func TestManagementServiceLifecycle(t *testing.T) {
	s, m, body := managementFixture()
	body["id"] = "ignored"
	body["created_at"] = "ignored"
	body["note"] = nil
	row, err := s.Save("", nil, map[string]any{"guardrail": body})
	if err != nil {
		t.Fatal(err)
	}
	id := str(row["guardrail_id"])
	if !strings.HasPrefix(id, "guardrail_") || row["created_at"] == "ignored" || row["guardrail_definition_location"] != "db" {
		t.Fatal(row)
	}
	original, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	created := original["created_at"]
	result, err := s.Save(id, original, map[string]any{"guardrail_id": "ignored", "litellm_params": map[string]any{"replacement": "[hidden]", "priority": nil}})
	if err != nil {
		t.Fatal(err)
	}
	p := result["litellm_params"].(map[string]any)
	if p["priority"] != nil || p["default_on"] != false || p["replacement"] != "[hidden]" || result["created_at"] != created || result["id"] != id {
		t.Fatal(result)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if err = s.Delete(id); err != nil {
		t.Fatal(err)
	}
	if len(m.rows) != 0 {
		t.Fatal("删除后残留")
	}
	if _, err = s.Get(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err = s.Delete(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

// TestManagementServiceSecrets 验证掩码与空字符串保留凭据、null 删除可选凭据，失败不覆盖原记录；不调用外部提供商。
func TestManagementServiceSecrets(t *testing.T) {
	s, m, body := managementFixture()
	body["litellm_params"] = map[string]any{"guardrail": "openai_moderation", "api_key": "test-private", "aws_session_token": "optional"}
	result, err := s.Save("fixed", nil, body)
	if err != nil {
		t.Fatal(err)
	}
	if result["litellm_params"].(map[string]any)["api_key"] != credentialMask {
		t.Fatal(result)
	}
	for _, value := range []any{credentialMask, ""} {
		row, _ := s.Get("fixed")
		_, err = s.Save("fixed", row, map[string]any{"litellm_params": map[string]any{"api_key": value, "aws_session_token": nil}})
		if err != nil {
			t.Fatal(err)
		}
		saved, _ := s.Get("fixed")
		p := saved["litellm_params"].(map[string]any)
		if p["api_key"] != "test-private" || p["aws_session_token"] != nil {
			t.Fatal("凭据更新语义改变")
		}
	}
	before := m.rows["fixed"]
	row, _ := s.Get("fixed")
	_, err = s.Save("fixed", row, map[string]any{"litellm_params": map[string]any{"api_key": nil}})
	if err == nil || m.rows["fixed"] != before {
		t.Fatal("删除必填凭据应校验失败且不写库", err)
	}
}

// TestManagementServiceFailures 覆盖类型、规则、序列化及各存储操作故障；验证无部分成功，局部夹具随测试回收。
func TestManagementServiceFailures(t *testing.T) {
	for _, which := range []string{"params", "validate", "marshal", "list", "put", "get", "delete"} {
		t.Run(which, func(t *testing.T) {
			s, m, body := managementFixture()
			var err error
			switch which {
			case "params":
				body["litellm_params"] = nil
			case "validate":
				body["guardrail_name"] = ""
			case "marshal":
				body["extension"] = make(chan int)
			default:
				m.failure = which
			}
			if which == "get" {
				_, err = s.Get("id")
			} else if which == "delete" {
				err = s.Delete("id")
			} else {
				_, err = s.Save("", nil, body)
			}
			if err == nil || len(m.rows) != 0 {
				t.Fatal("故障被吞掉或仍写库", err, m.rows)
			}
			if m.failure != "" && !errors.Is(err, managementFault) {
				t.Fatal("存储错误丢失", err)
			}
			if m.failure == "" {
				var e *managementError
				if !errors.As(err, &e) || e.Status != 400 || e.Code != "invalid_request" || e.Error() == "" {
					t.Fatal(err)
				}
			}
			if which == "list" {
				list, err := s.List()
				if list != nil || !errors.Is(err, managementFault) {
					t.Fatal("读取失败返回部分成功", list, err)
				}
			}
		})
	}
}

// TestManagementServiceConflicts 验证数据库名称/ID 与 YAML 名称冲突都拒绝，当前记录自更新允许；内存夹具无需清理。
func TestManagementServiceConflicts(t *testing.T) {
	for _, source := range []string{"name", "id", "yaml"} {
		t.Run(source, func(t *testing.T) {
			s, m, body := managementFixture()
			existing := map[string]any{"guardrail_id": "existing-id", "guardrail_name": "existing-name", "litellm_params": map[string]any{"guardrail": "local"}}
			if source == "yaml" {
				s.Configured = func() []map[string]any { return []map[string]any{existing} }
			} else {
				raw, _ := json.Marshal(existing)
				m.rows["existing-id"] = string(raw)
			}
			body["guardrail_name"] = "existing-name"
			if source == "id" {
				body["guardrail_name"] = "existing-id"
			}
			_, err := s.Save("", nil, body)
			var e *managementError
			if !errors.As(err, &e) || e.Status != 409 || m.calls != 0 {
				t.Fatal("重名未拒绝", err)
			}
		})
	}
}

// TestManagementMemoryRejectsCorruptFixture 验证单测存储不把损坏 JSON 当空记录；夹具仅存在内存，无外部清理。
func TestManagementMemoryRejectsCorruptFixture(t *testing.T) {
	s, m, _ := managementFixture()
	m.rows["bad"] = "{"
	if _, err := s.Get("bad"); err == nil {
		t.Fatal("损坏夹具读取不能成功")
	}
	if _, err := s.List(); err == nil {
		t.Fatal("损坏夹具列表不能成功")
	}
}
