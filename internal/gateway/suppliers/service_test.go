package suppliers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// memoryStore 模拟窄存储及故障，特意返回共享对象以发现服务补丁污染；只供单测使用。
type memoryStore struct {
	rows  map[string]map[string]any
	fail  string
	kinds []string
}

// ListKV 返回测试共享记录或指定读错误；kind 记录用于验证旧命名空间，无外部副作用。
func (m *memoryStore) ListKV(kind string) ([]map[string]any, error) {
	m.kinds = append(m.kinds, kind)
	if m.fail == "list" {
		return nil, errors.New("private storage error")
	}
	var rows []map[string]any
	for _, r := range m.rows {
		rows = append(rows, r)
	}
	return rows, nil
}

// GetKV 返回 id 的共享对象或缺失/故障错误，服务必须复制后才能修改。
func (m *memoryStore) GetKV(kind, id string) (map[string]any, error) {
	m.kinds = append(m.kinds, kind)
	if m.fail == "get" {
		return nil, errors.New("private storage error")
	}
	if r, ok := m.rows[id]; ok {
		return r, nil
	}
	return nil, sql.ErrNoRows
}

// PutKV 解码成功才替换记录，模拟真实保存边界；故障不写入，由测试回读验证。
func (m *memoryStore) PutKV(kind, id, raw string) error {
	m.kinds = append(m.kinds, kind)
	if m.fail == "put" {
		return errors.New("private storage error")
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return err
	}
	m.rows[id] = r
	return nil
}

// DeleteKV 模拟幂等缺失与特定项失败；批量中此前成功项保持删除，无外部数据。
func (m *memoryStore) DeleteKV(kind, id string) error {
	m.kinds = append(m.kinds, kind)
	if m.fail == "delete" || id == "reject" {
		return errors.New("private storage error")
	}
	if _, ok := m.rows[id]; !ok {
		return sql.ErrNoRows
	}
	delete(m.rows, id)
	return nil
}

// TestServiceLifecycle 验证两个旧存储种类的创建、合并、脱敏、替换、upsert 和删除；内存隔离无需清理。
// 特别验证 null/零/未知字段和非法秘密补丁保留旧值，返回掩码不污染存储。
func TestServiceLifecycle(t *testing.T) {
	for _, kind := range []string{"credentials", "credential"} {
		t.Run(kind, func(t *testing.T) {
			st := &memoryStore{rows: map[string]map[string]any{}}
			s := Service{Store: st, Kind: kind}
			rows, err := s.List()
			if err != nil || rows == nil || len(rows) != 0 {
				t.Fatal("空列表必须是非nil成功", rows, err)
			}
			body := map[string]any{"credential_values": map[string]any{"api_key": "original-secret", "api_base": "old"}, "password": "ignored", "note": nil, "enabled": false, "zero": 0, "name": "display", "prompt": map[string]any{"extra": "legacy"}}
			created, err := s.Create("one", body)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := created["credential_values"]; ok {
				t.Fatal("创建返回秘密对象")
			}
			if _, ok := created["password"]; ok {
				t.Fatal("保留了password")
			}
			if created["extra"] != "legacy" || created["id"] != "one" {
				t.Fatal("旧展开或ID丢失")
			}
			if kind == "credential" && created["credential_alias"] != "display" {
				t.Fatal("单数旧别名丢失")
			}
			for _, secret := range []any{nil, "", "   ", "********", 42} {
				got, err := s.Update("one", map[string]any{"credential_values": map[string]any{"api_key": secret, "api_base": "new"}})
				if err != nil {
					t.Fatal(err)
				}
				v := got["credential_values"].(map[string]any)
				if v["api_key"] == "original-secret" || v["api_base"] != "new" {
					t.Fatal("合并或脱敏错误", v)
				}
				if st.rows["one"]["credential_values"].(map[string]any)["api_key"] != "original-secret" {
					t.Fatal("无效补丁破坏秘密")
				}
			}
			for _, values := range []any{nil, "invalid", []any{}} {
				if _, err = s.Update("one", map[string]any{"credential_values": values}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Update("one", map[string]any{"credential_values": map[string]any{"api_key": "replacement", "timeout": 0, "api_base": nil}, "note": nil}); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get("one")
			if err != nil {
				t.Fatal(err)
			}
			if got["enabled"] != false || got["zero"] != float64(0) || got["note"] != nil {
				t.Fatal("零值或null改变", got)
			}
			vals := st.rows["one"]["credential_values"].(map[string]any)
			if vals["api_key"] != "replacement" || vals["api_base"] != nil {
				t.Fatal("真实替换未保存")
			}
			rows, err = s.List()
			if err != nil || len(rows) != 1 || strings.Contains(string(mustJSON(t, rows)), "replacement") {
				t.Fatal("列表泄露秘密或缺失", err)
			}
			if _, err = s.Create("one", map[string]any{"note": "replace"}); err != nil {
				t.Fatal(err)
			}
			if _, ok := st.rows["one"]["zero"]; ok {
				t.Fatal("创建未替换")
			}
			n, err := s.Delete([]string{"one", "one", "missing"})
			if err != nil || n != 1 {
				t.Fatal("删除计数错误", n, err)
			}
			if _, err = s.Get("one"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("缺失详情应失败", err)
			}
			if _, err = s.Update("one", nil); err != nil {
				t.Fatal("upsert失败", err)
			}
			generated, err := s.Update("", nil)
			if err != nil || !strings.HasPrefix(generated["id"].(string), "credential_") {
				t.Fatal("空ID upsert失败", err)
			}
			generated, err = s.Create("", nil)
			if err != nil || !strings.HasPrefix(generated["id"].(string), "credential_") {
				t.Fatal("空对象创建失败", err)
			}
			for _, gotKind := range st.kinds {
				if gotKind != kind {
					t.Fatal("存储命名空间改变", gotKind)
				}
			}
		})
	}
}

// mustJSON 为断言编码可序列化的内存数据；t 报告错误，返回字节，不读取外部资源。
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestServiceFailures 验证读写/编码失败和部分删除；故障不得返回成功或污染共享旧对象，内存自动回收。
func TestServiceFailures(t *testing.T) {
	st := &memoryStore{rows: map[string]map[string]any{}}
	s := Service{Store: st, Kind: "credentials"}
	if _, err := s.Create("one", map[string]any{"credential_values": map[string]any{"api_key": "old-key"}}); err != nil {
		t.Fatal(err)
	}
	before := string(mustJSON(t, st.rows))
	for _, fail := range []string{"list", "get", "put", "delete"} {
		st.fail = fail
		var err error
		switch fail {
		case "list":
			_, err = s.List()
		case "get":
			_, err = s.Get("one")
			if _, e := s.Update("one", nil); e == nil {
				t.Fatal("读错误被当成upsert")
			}
		case "put":
			_, err = s.Update("one", map[string]any{"credential_values": map[string]any{"api_key": "new-key"}})
			if _, e := s.Create("new", nil); e == nil {
				t.Fatal("创建失败伪成功")
			}
		case "delete":
			_, err = s.Delete([]string{"one"})
		}
		if err == nil {
			t.Fatal("故障伪成功", fail)
		}
		if string(mustJSON(t, st.rows)) != before {
			t.Fatal("失败污染数据", fail)
		}
	}
	st.fail = ""
	for _, update := range []bool{false, true} {
		var err error
		body := map[string]any{"bad": make(chan int)}
		if update {
			_, err = s.Update("one", body)
		} else {
			_, err = s.Create("one", body)
		}
		if err == nil {
			t.Fatal("编码失败伪成功")
		}
		if string(mustJSON(t, st.rows)) != before {
			t.Fatal("编码失败污染数据")
		}
	}
	st.rows["bad"] = map[string]any{"unsupported": make(chan int)}
	if _, err := s.Get("bad"); err == nil {
		t.Fatal("非法存储对象未拒绝")
	}
	if _, err := s.List(); err == nil {
		t.Fatal("非法列表未拒绝")
	}
	if _, err := s.Update("bad", nil); err == nil {
		t.Fatal("非法存储更新未拒绝")
	}
	delete(st.rows, "bad")
	n, err := s.Delete([]string{"one", "reject"})
	if n != 1 || err == nil || len(st.rows) != 0 {
		t.Fatal("部分删除语义改变", n, err)
	}
}

// TestObjectCompatibility 验证规范化和脱敏边界，保留旧非字符串秘密语义；纯内存，无外部清理。
func TestObjectCompatibility(t *testing.T) {
	obj := map[string]any{"password": "ignored", "id": nil, "credential_name": nil, "credential_info": nil}
	Normalize(obj)
	if !strings.HasPrefix(obj["id"].(string), "credential_") || obj["created_at"] == nil || obj["credential_info"] == nil {
		t.Fatal("缺省未补齐", obj)
	}
	before := string(mustJSON(t, obj))
	Normalize(obj)
	if string(mustJSON(t, obj)) != before {
		t.Fatal("重复规范化不稳定")
	}
	for _, value := range []any{nil, "legacy", map[string]any{"api_key": "", "token": "1234", "secret": "12345", "api_base": "url", "password": 42}} {
		input := map[string]any{"credential_values": value}
		before := string(mustJSON(t, input))
		out := Redact(input, true)
		if vals, ok := value.(map[string]any); ok {
			got := out["credential_values"].(map[string]any)
			if got["api_key"] != "*****" || got["token"] != "*****" || got["secret"] != "12*45" || got["password"] != 42 || got["api_base"] != vals["api_base"] {
				t.Fatal("掩码边界改变", got)
			}
			if _, ok = Redact(input, false)["credential_values"]; ok {
				t.Fatal("创建秘密未省略")
			}
		} else if !reflect.DeepEqual(out, input) {
			t.Fatal("历史非对象值改变")
		}
		if string(mustJSON(t, input)) != before {
			t.Fatal("脱敏污染输入")
		}
	}
}
