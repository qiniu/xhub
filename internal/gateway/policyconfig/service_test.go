package policyconfig

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
	s := Service{Store: st, Kind: "policies"}
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

// TestServiceLifecycle 验证策略创建、浅层补丁、覆盖、缺失更新和删除兼容；内存服务无外部清理。
// 正常/边界输入保留 null、零、扩展字段及原始策略正文；真实调用、权限和账务由后台回归验证。
func TestServiceLifecycle(t *testing.T) {
	st := &memoryStore{rows: map[string]map[string]any{}}
	s := Service{Store: st, Kind: "policies"}
	rows, err := s.List()
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("空列表", err)
	}
	body := map[string]any{"password": "ignored", "name": "display", "version": 0, "note": nil, "enabled": false, "description": "Top description", "policy": map[string]any{"description": "Nested description", "note": "legacy", "extension": true}, "parameters": map[string]any{"model": "old", "temperature": 0.5}}
	created, err := s.Create("one", body)
	if err != nil {
		t.Fatal(err)
	}
	if created["policie_alias"] != "display" || created["status"] != "active" || created["policy_name"] != "" || created["password"] != nil || created["policy_id"] != "one" || created["description"] != "Top description" || created["note"] != "legacy" || created["version"] != float64(0) || created["enabled"] != false {
		t.Fatal("旧字段/嵌套展开改变", created)
	}
	if body["password"] != "ignored" || body["note"] != nil {
		t.Fatal("创建污染调用方对象")
	}
	updated, err := s.Update("one", map[string]any{"id": "wrong", "policy_id": "wrong", "password": "ignored", "version": nil, "note": nil, "parameters": map[string]any{"model": "new"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated["id"] != "one" || updated["policy_id"] != "one" || updated["version"] != 1 || updated["note"] != nil || updated["parameters"].(map[string]any)["temperature"] != nil {
		t.Fatal("更新不是旧浅层替换语义", updated)
	}
	before := string(mustJSON(t, st.rows))
	read, err := s.Get("one")
	if err != nil {
		t.Fatal(err)
	}
	read["parameters"].(map[string]any)["model"] = "modified response"
	rows, err = s.List()
	if err != nil || len(rows) != 1 {
		t.Fatal("列表", err)
	}
	if string(mustJSON(t, st.rows)) != before {
		t.Fatal("返回对象污染存储")
	}
	if _, err = s.Get("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("缺失应报告ErrNoRows", err)
	}
	for _, create := range []bool{true, false} {
		var out map[string]any
		if create {
			out, err = s.Create("", nil)
		} else {
			out, err = s.Update("", nil)
		}
		if err != nil || !strings.HasPrefix(out["id"].(string), "policie_") || out["created_at"] == nil || out["version"] != 1 {
			t.Fatal("自动ID或upsert默认值", out, err)
		}
	}
	out, err := s.Create("one", map[string]any{"name": "replacement"})
	if err != nil || out["description"] != nil {
		t.Fatal("同名创建不是替换", out, err)
	}
	n, err := s.Delete([]string{"one", "one", "missing"})
	if err != nil || n != 1 {
		t.Fatal("删除计数", n, err)
	}
	for _, kind := range st.kinds {
		if kind != "policies" {
			t.Fatal("命名空间改变", kind)
		}
	}
}

// TestNormalizeLegacy 验证旧数据缺省及非nil值保留，重复规范化幂等；纯对象无待清理资源。
func TestNormalizeLegacy(t *testing.T) {
	for _, id := range []any{nil, "", 42} {
		obj := map[string]any{"id": id, "policy_id": nil, "created_at": nil, "version": nil, "password": "removed"}
		Normalize("policies", obj)
		if obj["password"] != nil || obj["created_at"] == nil || obj["version"] != 1 {
			t.Fatal("规范化缺省错误", obj)
		}
		before := string(mustJSON(t, obj))
		Normalize("policies", obj)
		if string(mustJSON(t, obj)) != before {
			t.Fatal("规范化不幂等")
		}
	}
	obj := map[string]any{"id": "fixed", "policy_id": "legacy", "version": 0, "created_at": "old"}
	Normalize("policies", obj)
	if obj["policy_id"] != "legacy" || obj["version"] != 0 || obj["created_at"] != "old" {
		t.Fatal("覆盖旧非空字段")
	}
}

// TestServiceLegacyNamespaces 验证两个历史种类分别写入对应命名空间与别名前缀；纯内存无外部清理。
func TestServiceLegacyNamespaces(t *testing.T) {
	for _, kind := range []string{"policies", "policy"} {
		st := &memoryStore{rows: map[string]map[string]any{}}
		svc := Service{Store: st, Kind: kind}
		obj, err := svc.Create("", map[string]any{"name": "Display", "status": "", "policy_name": false, "created_at": "old"})
		if err != nil {
			t.Fatal(err)
		}
		if obj[prefix(kind)+"_alias"] != "Display" || obj["status"] != "" || obj["policy_name"] != false || obj["created_at"] != "old" {
			t.Fatal("旧默认或别名改变", obj)
		}
		if _, err = svc.Get(obj["id"].(string)); err != nil {
			t.Fatal(err)
		}
		for _, got := range st.kinds {
			if got != kind {
				t.Fatal("命名空间被合并", got)
			}
		}
	}
}
