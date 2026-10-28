package metadataconfig

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	s := Service{Store: st, Kind: "customer"}
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

// TestServiceLifecycle 验证四空间各自的创建/替换/upsert、旧字段及浅层补丁；共享对象内存夹具检测污染，无外部清理。
func TestServiceLifecycle(t *testing.T) {
	for _, kind := range []string{"tag", "tags", "customer", "end_user"} {
		t.Run(kind, func(t *testing.T) {
			st := &memoryStore{rows: map[string]map[string]any{}}
			s := Service{Store: st, Kind: kind}
			if !Supports(kind) {
				t.Fatal("资源不被识别")
			}
			rows, err := s.List()
			if err != nil || rows == nil || len(rows) != 0 {
				t.Fatal("空列表", rows, err)
			}
			body := map[string]any{"name": "Display", "password": "ignored", "spend": 0, "max_budget": nil, "blocked": false, "note": nil, "skill": map[string]any{"note": "nested", "extension": true}, "parameters": map[string]any{"model": "old", "temperature": 0.5}}
			got, err := s.Create("one", body)
			if err != nil || got[identityField(kind)] != "one" || got["password"] != nil || got["note"] != "nested" || got["spend"] != float64(0) || got["extension"] != true || body["password"] != "ignored" {
				t.Fatal("创建变化", got, err)
			}
			if kind != "tags" && got[aliasField(kind)] != "Display" {
				t.Fatal("旧别名丢失", got)
			}
			before := string(mustJSON(t, st.rows))
			read, err := s.Get("one")
			if err != nil {
				t.Fatal(err)
			}
			read["parameters"].(map[string]any)["model"] = "modified"
			if string(mustJSON(t, st.rows)) != before {
				t.Fatal("读响应修改存储")
			}
			got, err = s.Update("one", map[string]any{"id": "wrong", identityField(kind): "wrong", "spend": nil, "max_budget": nil, "blocked": nil, "note": nil, "password": "ignored", "parameters": map[string]any{"model": "new"}})
			if err != nil || got["id"] != "one" || got[identityField(kind)] != "one" || got["spend"] != 0 || got["note"] != nil || got["password"] != nil || got["parameters"].(map[string]any)["temperature"] != nil {
				t.Fatal("浅层更新变化", got, err)
			}
			rows, err = s.List()
			if err != nil || len(rows) != 1 {
				t.Fatal("读取失败", err)
			}
			if _, err = s.Get("missing"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("缺失错误变化", err)
			}
			for _, create := range []bool{true, false} {
				if create {
					got, err = s.Create("", nil)
				} else {
					got, err = s.Update("", nil)
				}
				if err != nil || got["created_at"] == nil || got["id"] == "" || got["spend"] != 0 {
					t.Fatal("生成ID/upsert变化", got, err)
				}
			}
			got, err = s.Create("one", map[string]any{"name": "replacement"})
			if err != nil || got["parameters"] != nil {
				t.Fatal("未替换记录", err)
			}
			n, err := s.Delete([]string{"one", "one", "missing"})
			if err != nil || n != 1 {
				t.Fatal("删除计数变化", n, err)
			}
			for _, k := range st.kinds {
				if k != kind {
					t.Fatal("串空间", st.kinds)
				}
			}
		})
	}
	if Supports("user") || Supports("policy") {
		t.Fatal("扩展到了业务账户或策略")
	}
}

// TestNormalizeLegacy 验证旧缺省/null、零、非字符串ID和标签名称优先级；不写存储且重复调用幂等。
func TestNormalizeLegacy(t *testing.T) {
	for _, kind := range []string{"tag", "tags", "customer", "end_user"} {
		for _, id := range []any{nil, "", 42} {
			obj := map[string]any{"id": id, "spend": nil, "password": "remove"}
			Normalize(kind, obj)
			if obj["password"] != nil || obj["created_at"] == nil || obj["spend"] != 0 {
				t.Fatal("默认值错误", obj)
			}
			before := string(mustJSON(t, obj))
			Normalize(kind, obj)
			if before != string(mustJSON(t, obj)) {
				t.Fatal("不幂等")
			}
		}
		obj := map[string]any{"id": "fixed", "name": "original", "tag_name": "标签原文", "spend": 0, "blocked": true, "max_budget": 0, "created_at": "old"}
		Normalize(kind, obj)
		if obj["blocked"] != true || obj["spend"] != 0 || obj["max_budget"] != 0 || obj["created_at"] != "old" {
			t.Fatal("改变非nil字段", obj)
		}
		if (kind == "tag" || kind == "tags") && obj["name"] != "标签原文" {
			t.Fatal("标签名称优先级改变", obj)
		}
	}
}
