package metadataconfig

import (
	"errors"
	"reflect"
	"testing"
)

// tagReader 提供确定性的只读行与故障；kind用于验证没有误读复数或用量库，无外部资源。
type tagReader struct {
	rows []map[string]any
	err  error
	kind string
}

// ListKV 返回测试输入并记录命名空间；不写数据库，供ReadTagCatalog边界测试使用。
func (s *tagReader) ListKV(kind string) ([]map[string]any, error) {
	s.kind = kind
	return s.rows, s.err
}

// TestReadTagCatalog 验证正常配置、空库、旧id字段、零/null/未知字段及故障；内存测试无需清理。
// 转换不能变更原始行，名称字典必须保留用户原文，损坏或重名不能伪装部分成功。
func TestReadTagCatalog(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []map[string]any
		err  error
		want map[string]any
		fail bool
	}{
		{name: "empty", want: map[string]any{}},
		{name: "stored", rows: []map[string]any{{"name": "原名", "max_budget": 0, "models": nil, "extra": false, "password": "not-public"}}, want: map[string]any{"原名": map[string]any{"name": "原名", "max_budget": 0, "models": nil, "extra": false}}},
		{name: "legacy-id", rows: []map[string]any{{"id": "legacy"}}, want: map[string]any{"legacy": map[string]any{"id": "legacy", "name": "legacy"}}},
		{name: "database", err: errors.New("private-storage-error"), fail: true},
		{name: "missing-name", rows: []map[string]any{{"description": "broken"}}, fail: true},
		{name: "duplicate", rows: []map[string]any{{"name": "same"}, {"name": "same"}}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &tagReader{rows: tc.rows, err: tc.err}
			before := make([]map[string]any, len(tc.rows))
			for i, row := range tc.rows {
				before[i] = map[string]any{}
				for k, v := range row {
					before[i][k] = v
				}
			}
			got, err := ReadTagCatalog(reader)
			if (err != nil) != tc.fail || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("读取结果不符: got=%v err=%v", got, err)
			}
			if reader.kind != "tag" {
				t.Fatalf("读错存储空间: %s", reader.kind)
			}
			for i := range tc.rows {
				if !reflect.DeepEqual(tc.rows[i], before[i]) {
					t.Fatal("读取修改了原始行")
				}
			}
		})
	}
}

// customerReader 返回按空间划分的测试行，可仅让指定空间失败；没有数据库资源。
type customerReader struct {
	rows map[string][]map[string]any
	fail string
}

// ListKV 为列表投影测试返回共享行，允许验证读取不修改输入及部分故障不伪成功。
func (s customerReader) ListKV(kind string) ([]map[string]any, error) {
	if s.fail == kind {
		return nil, errors.New("private")
	}
	return s.rows[kind], nil
}

// TestCustomerProjection 验证两个空间的旧四字段投影、相同ID两行、不泄露扩展字段及故障；纯内存无清理。
func TestCustomerProjection(t *testing.T) {
	st := customerReader{rows: map[string][]map[string]any{"customer": {{"id": "same", "secret": "not-in-list"}}, "end_user": {{"user_id": "same", "blocked": true, "max_budget": 0}}}}
	before := string(mustJSON(t, st.rows))
	out, err := CustomerList(st)
	if err != nil || len(out) != 2 || len(out[0]) != 4 || out[0]["user_id"] != "same" || out[1]["user_id"] != "same" || out[1]["blocked"] != true || out[1]["max_budget"] != float64(0) {
		t.Fatal("投影改变", out, err)
	}
	if before != string(mustJSON(t, st.rows)) {
		t.Fatal("列表修改了原记录")
	}
	for _, kind := range []string{"customer", "end_user"} {
		st.fail = kind
		if rows, err := CustomerList(st); err == nil || rows != nil {
			t.Fatal("返回了部分成功", rows, err)
		}
	}
	if rows, err := CustomerList(customerReader{}); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("空目录错误", rows, err)
	}
	if _, err := CustomerList(customerReader{rows: map[string][]map[string]any{"customer": {{"bad": make(chan int)}}}}); err == nil {
		t.Fatal("损坏数据未拒绝")
	}
}
