package family

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPluginListFilter 验证配置筛选、旧缺省和错误参数；内存存储只测确定性分支，不替代真实回归，结束自动释放。
func TestPluginListFilter(t *testing.T) {
	for _, tc := range []struct {
		query         string
		status, count int
	}{
		{"", 200, 6}, {"?enabled_only=false", 200, 6}, {"?enabled_only=true", 200, 3},
		{"?enabled_only=true;bad", 400, 0}, {"?enabled_only=%zz", 400, 0}, {"?enabled_only=", 400, 0}, {"?enabled_only=TRUE", 400, 0}, {"?enabled_only=1", 400, 0}, {"?enabled_only=true&enabled_only=false", 400, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			st := &catalogMemoryStore{rows: map[string]map[string]any{
				"on": {"id": "on", "enabled": true}, "off": {"id": "off", "enabled": false}, "missing": {"id": "missing"}, "null": {"id": "null", "enabled": nil}, "string": {"id": "string", "enabled": "true"}, "array": {"id": "array", "enabled": []any{true}},
			}}
			w := callCatalogStore(st, "GET", "/claude-code/plugins"+tc.query, nil)
			if w.Code != tc.status {
				t.Fatalf("筛选状态错误: %d %s", w.Code, w.Body)
			}
			if tc.status == 200 {
				var body struct {
					Plugins []map[string]any `json:"plugins"`
					Count   int              `json:"count"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Plugins == nil || len(body.Plugins) != tc.count || body.Count != tc.count {
					t.Fatal("筛选响应错误", w.Body)
				}
				if tc.count == 3 {
					for _, r := range body.Plugins {
						if r["enabled"] != true {
							t.Error("错误类型或停用配置进入已启用列表", r)
						}
					}
				}
			}
			if st.putCalls != 0 || len(st.deleteIDs) != 0 || len(st.rows["missing"]) != 1 {
				t.Fatal("列表读取修改了存储")
			}
		})
	}
}

// TestPluginListFailures 验证空列表、存储失败及其他路由隔离；无外部服务，无持久化数据需清理。
func TestPluginListFailures(t *testing.T) {
	for _, tc := range []struct {
		st     *catalogMemoryStore
		status int
	}{{&catalogMemoryStore{}, 200}, {&catalogMemoryStore{listErr: errors.New("private-sql-error")}, 500}} {
		w := callCatalogStore(tc.st, "GET", "/claude-code/plugins/?enabled_only=true", nil)
		if w.Code != tc.status {
			t.Fatal("空列表或失败契约错误", w.Code, w.Body)
		}
		if tc.status == 500 && strings.Contains(w.Body.String(), "private-sql-error") {
			t.Fatal("泄露底层错误")
		}
	}
	for _, path := range []string{"/claude-code/marketplace.json", "/claude-code/plugins/example", "/other/plugins", "/v1/skills"} {
		w := httptest.NewRecorder()
		if writePluginList(nil, w, httptest.NewRequest("GET", path, nil)) || w.Body.Len() != 0 {
			t.Fatal("误处理其他列表", path)
		}
	}
	w := httptest.NewRecorder()
	if writePluginList(nil, w, httptest.NewRequest("POST", "/claude-code/plugins", nil)) {
		t.Fatal("误处理插件注册")
	}
}
