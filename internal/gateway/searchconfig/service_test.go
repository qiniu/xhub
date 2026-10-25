package searchconfig

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	s := Service{Store: st, Kind: "search_tools"}
	if _, err := s.Create("one", map[string]any{"litellm_params": map[string]any{"api_key": "old-key"}}); err != nil {
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
			_, err = s.Update("one", map[string]any{"litellm_params": map[string]any{"api_key": "new-key"}})
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

// TestServiceLifecycle 覆盖三种历史命名空间的完整 CRUD、缺省、覆盖、掩码及 upsert；内存隔离无需外部清理。
// 同时验证旧对象回读与返回脱敏不修改存储共享引用，非秘密 null/零值和扩展字段保持。
func TestServiceLifecycle(t *testing.T) {
	for _, kind := range []string{"search_tools", "search_tool", "search-tools"} {
		t.Run(kind, func(t *testing.T) {
			st := &memoryStore{rows: map[string]map[string]any{}}
			s := Service{Store: st, Kind: kind}
			rows, err := s.List()
			if err != nil || rows == nil || len(rows) != 0 {
				t.Fatal("空列表", rows, err)
			}
			body := map[string]any{"password": "ignored", "name": "display", "note": nil, "zero": 0, "enabled": false, "search_tool": map[string]any{"extra": "nested", "note": "fallback"}, "litellm_params": map[string]any{"api_key": "original", "api_base": "old"}}
			out, err := s.Create("one", body)
			if err != nil {
				t.Fatal(err)
			}
			if out["password"] != nil || out["id"] != "one" || out["extra"] != "nested" || out["note"] != "fallback" || out["search_tool_name"] != "" || out[strings.TrimSuffix(kind, "s")+"_alias"] != "display" {
				t.Fatal("创建兼容字段", out)
			}
			if out["litellm_params"].(map[string]any)["api_key"] != "********" {
				t.Fatal("创建未脱敏")
			}
			for _, secret := range []any{nil, "", "   ", "********", 42} {
				out, err = s.Update("one", map[string]any{"password": "ignored", "note": nil, "litellm_params": map[string]any{"api_key": secret, "api_base": "new"}})
				if err != nil {
					t.Fatal(err)
				}
				if st.rows["one"]["litellm_params"].(map[string]any)["api_key"] != "original" || out["note"] != nil {
					t.Fatal("秘密或null合并改变")
				}
			}
			out, err = s.Update("one", map[string]any{"litellm_params": map[string]any{"api_key": "replacement"}})
			if err != nil {
				t.Fatal(err)
			}
			before := string(mustJSON(t, st.rows))
			if _, err = s.Get("one"); err != nil {
				t.Fatal(err)
			}
			if rows, err = s.List(); err != nil || len(rows) != 1 {
				t.Fatal("列表", err)
			}
			if string(mustJSON(t, st.rows)) != before {
				t.Fatal("读取污染存储")
			}
			if _, err = s.Get("missing"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("缺失错误", err)
			}
			// 同名创建为替换，更新缺失为创建；保留原行为而非新加冲突限制。
			out, err = s.Create("one", map[string]any{"zero": 0})
			if err != nil || out["litellm_params"] != nil {
				t.Fatal("同名创建未替换", err)
			}
			out, err = s.Update("upsert", map[string]any{"enabled": false, "litellm_params": map[string]any{"api_key": "upsert-key"}})
			if err != nil || out["created_at"] == nil || out["enabled"] != false {
				t.Fatal("upsert", err)
			}
			for _, create := range []bool{true, false} {
				if create {
					out, err = s.Create("", nil)
				} else {
					out, err = s.Update("", nil)
				}
				if err != nil || !strings.HasPrefix(out["id"].(string), strings.TrimSuffix(kind, "s")+"_") {
					t.Fatal("空ID", err)
				}
			}
			n, err := s.Delete([]string{"one", "one", "missing"})
			if err != nil || n != 1 {
				t.Fatal("重复删除", n, err)
			}
			for _, storedKind := range st.kinds {
				if storedKind != kind {
					t.Fatal("命名空间改变")
				}
			}
		})
	}
}

// TestNormalizeLegacyRecord 验证读取旧数据时仅补缺省且不覆盖非空别名/零/空字符串；纯对象，无清理。
func TestNormalizeLegacyRecord(t *testing.T) {
	for _, id := range []any{nil, "", 42} {
		obj := map[string]any{"id": id, "search_tool_id": nil, "created_at": nil, "search_tool_name": nil, "password": "remove"}
		Normalize("search-tools", obj)
		if obj["password"] != nil || obj["created_at"] == nil || obj["search_tool_name"] != "" {
			t.Fatal("默认字段", obj)
		}
		before := string(mustJSON(t, obj))
		Normalize("search-tools", obj)
		if string(mustJSON(t, obj)) != before {
			t.Fatal("规范化不幂等")
		}
	}
}
