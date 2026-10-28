package accessgroupconfig

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
	s := Service{Store: st, Kind: "access_group"}
	if _, err := s.Create("one", map[string]any{"parameters": map[string]any{"model": "old-key"}}); err != nil {
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
			_, err = s.Update("one", map[string]any{"parameters": map[string]any{"model": "new-key"}})
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

// TestServiceLifecycle 验证访问组替换、浅层补丁、缺失更新及删除；内存夹具无外部清理。
// 正常和边界值保留未知字段、null及零值；授权和预算隔离由真实后台回归验证。
func TestServiceLifecycle(t *testing.T) {
	st := &memoryStore{rows: map[string]map[string]any{}}
	s := Service{Store: st, Kind: "access_group"}
	rows, err := s.List()
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("空列表", err)
	}
	body := map[string]any{"password": "ignored", "name": "display", "enabled": false, "note": nil, "description": "top", "policy": map[string]any{"description": "nested", "note": "expanded"}, "parameters": map[string]any{"keep": true, "old": 1}, "models": []any{"allowed"}}
	created, err := s.Create("one", body)
	if err != nil {
		t.Fatal(err)
	}
	if created["access_group_name"] != "display" || created["access_group_id"] != "one" || created["password"] != nil || created["note"] != "expanded" || created["description"] != "top" || created["enabled"] != false {
		t.Fatal("创建兼容改变", created)
	}
	if body["password"] != "ignored" || body["note"] != nil {
		t.Fatal("污染输入")
	}
	updated, err := s.Update("one", map[string]any{"id": "wrong", "access_group_id": "wrong", "password": "removed", "description": nil, "note": nil, "models": nil, "parameters": map[string]any{"new": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if updated["id"] != "one" || updated["access_group_id"] != "one" || updated["description"] != "" || updated["note"] != nil || len(updated["models"].([]any)) != 0 || updated["parameters"].(map[string]any)["old"] != nil || updated["updated_at"] != created["created_at"] {
		t.Fatal("浅层更新或时间语义改变", updated)
	}
	before := string(mustJSON(t, st.rows))
	read, err := s.Get("one")
	if err != nil {
		t.Fatal(err)
	}
	read["parameters"].(map[string]any)["new"] = 999
	rows, err = s.List()
	if err != nil || len(rows) != 1 {
		t.Fatal("列表失败", err)
	}
	if string(mustJSON(t, st.rows)) != before {
		t.Fatal("读操作污染存储")
	}
	if _, err = s.Get("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("缺失错误", err)
	}
	for _, create := range []bool{true, false} {
		var out map[string]any
		if create {
			out, err = s.Create("", nil)
		} else {
			out, err = s.Update("", nil)
		}
		if err != nil || !strings.HasPrefix(out["id"].(string), "access_group_") || out["access_group_name"] != out["id"] || out["created_at"] == nil {
			t.Fatal("自动ID或upsert", out, err)
		}
	}
	out, err := s.Create("one", map[string]any{"name": "replacement"})
	if err != nil || out["description"] != "" || out["parameters"] != nil {
		t.Fatal("创建未替换", out, err)
	}
	n, err := s.Delete([]string{"one", "one", "missing"})
	if err != nil || n != 1 {
		t.Fatal("删除计数", n, err)
	}
}

// TestNormalizeLegacy 验证两个命名空间的缺省、非nil边界值和幂等；内存数据自动回收。
// 不将配置中的模型/团队/Key字段解释为实际授权。
func TestNormalizeLegacy(t *testing.T) {
	for _, kind := range []string{"access_group", "access_groups"} {
		for _, id := range []any{nil, "", 42} {
			obj := map[string]any{"id": id, "password": "removed", "access_group_name": false}
			Normalize(kind, obj)
			if obj["password"] != nil || obj["created_at"] == nil || obj["description"] != "" {
				t.Fatal("缺省改变", obj)
			}
			for _, field := range []string{"models", "access_model_names", "access_mcp_server_ids", "access_agent_ids", "assigned_key_ids", "assigned_team_ids", "access_mcp_servers", "access_agents", "assigned_keys", "assigned_teams"} {
				if a, ok := obj[field].([]any); !ok || len(a) != 0 {
					t.Fatal("默认数组", field, obj[field])
				}
			}
			before := string(mustJSON(t, obj))
			Normalize(kind, obj)
			if string(mustJSON(t, obj)) != before {
				t.Fatal("规范化不幂等")
			}
		}
		st := &memoryStore{rows: map[string]map[string]any{}}
		s := Service{Store: st, Kind: kind}
		obj, err := s.Create("", map[string]any{"name": "Display", "models": false, "description": 0, "created_at": "old", "created_by": false, "budget_id": "legacy"})
		if err != nil || obj["access_group_name"] != "Display" || obj["models"] != false || obj["description"] != float64(0) || obj["updated_at"] != "old" || obj["created_by"] != false || obj["budget_id"] != "legacy" {
			t.Fatal("边界值改变", obj, err)
		}
		for _, got := range st.kinds {
			if got != kind {
				t.Fatal("命名空间被合并", got)
			}
		}
	}
}
