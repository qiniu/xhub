package usage

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/store"
)

// tagReader 提供确定性的只读行与故障；kind用于验证没有误读复数或用量库，无外部资源。
type tagReader struct {
	rows []map[string]any
	err  error
	kind string
}

// ListKV 返回测试输入并记录命名空间；不写数据库，供readTagCatalog边界测试使用。
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
			got, err := readTagCatalog(reader)
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

// tagHost 只替代鉴权和存储取得动作；不使用其他宿主方法，验证拒绝发生在访问配置库之前。
type tagHost struct {
	Host
	denied bool
	reads  int
}

// RequireManage 按测试前置决定403或管理员；不改变生产权限判断，无外部依赖。
func (h *tagHost) RequireManage(w http.ResponseWriter, r *http.Request) *auth.Principal {
	if h.denied {
		w.WriteHeader(403)
		return nil
	}
	return &auth.Principal{UserID: "admin"}
}

// RecordStore 计数后返回缺库；验证TagList缺失依赖不能伪装空列表，不连接数据库。
func (h *tagHost) RecordStore() *store.Store { h.reads++; return nil }

// TestTagListGuardAndUnavailable 检查拒绝先于读取及缺库503；请求与响应全部内存创建，无需清理。
func TestTagListGuardAndUnavailable(t *testing.T) {
	for _, denied := range []bool{true, false} {
		h := &tagHost{denied: denied}
		w := httptest.NewRecorder()
		TagList(h, w, httptest.NewRequest("GET", "/tag/list", nil))
		want, reads := 503, 1
		if denied {
			want, reads = 403, 0
		}
		if w.Code != want || h.reads != reads {
			t.Fatalf("标签鉴权/缺库结果错误: %d reads=%d", w.Code, h.reads)
		}
	}
}
