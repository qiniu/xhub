package family

import (
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// TestSkillBoundaryRoutes 验证精确路径及方法边界；纯解析无外部依赖，保留词必须仍是资源ID，其他接口不受影响，无需清理。
func TestSkillBoundaryRoutes(t *testing.T) {
	for _, id := range []string{"ordinary", "new", "list", "info", "update", "delete", "register", "runs", "skills", "archive", "settings", "plugins"} {
		for method, want := range map[string]string{"GET": "info", "DELETE": "delete"} {
			for _, suffix := range []string{"", "/"} {
				kind, action, got := parseMgmt(method, "/v1/skills/"+id+suffix, map[string]any{"skill_id": "other"})
				if kind != "skills" || action != want || got != id {
					t.Fatalf("资源ID被动作或正文覆盖: %s %s -> %s %s %s", method, id, kind, action, got)
				}
			}
		}
	}
	for _, tc := range []struct{ method, path string }{{"POST", "/v1/skills/new"}, {"GET", "/v1/skills"}, {"GET", "/v1/skills/"}, {"GET", "/v1/skills/x/archive"}, {"GET", "/skills/new"}, {"GET", "/v1/skills-extra/new"}} {
		if _, _, matched := skillResourceAction(tc.method, tc.path); matched {
			t.Fatalf("不应接管其他路径: %+v", tc)
		}
	}
	for _, tc := range []struct {
		method, path string
		matched      bool
	}{
		{"GET", "/v1/skills/a/archive", true}, {"GET", "/v1/skills/a/archive/", true}, {"GET", "/v1/skills/archive", false}, {"POST", "/v1/skills/a/archive", false}, {"GET", "/v1/skills//archive", false}, {"GET", "/v1/skills/a/archive/more", false}, {"GET", "/skills/a/archive", false},
	} {
		w := httptest.NewRecorder()
		matched := rejectSkillArchive(w, httptest.NewRequest(tc.method, tc.path, nil))
		if matched != tc.matched {
			t.Fatalf("归档匹配错误: %+v", tc)
		}
		if matched && (w.Code != 501 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "unsupported_operation")) {
			t.Fatal("归档错误响应不正确", w)
		}
		if !matched && w.Body.Len() != 0 {
			t.Fatal("未命中路径不应写响应")
		}
	}
}

// TestSkillBoundaryStore 验证GET只读、缺失404、读/删失败500、删除仅目标记录；内存存储仅用于确定性故障注入，不替代真实回归，无外部清理。
func TestSkillBoundaryStore(t *testing.T) {
	for _, tc := range []struct {
		name, method      string
		getErr, deleteErr error
		missing           bool
		status            int
	}{
		{"read", "GET", nil, nil, false, 200}, {"missing", "GET", nil, nil, true, 404}, {"read-failure", "GET", errors.New("private-sql"), nil, false, 500}, {"delete", "DELETE", nil, nil, false, 200}, {"delete-missing", "DELETE", nil, nil, true, 200}, {"delete-failure", "DELETE", nil, errors.New("private-sql"), false, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := map[string]map[string]any{"other": {"id": "other"}}
			if !tc.missing {
				rows["new"] = map[string]any{"id": "new", "skill_id": "new", "name": "original", "enabled": false}
			}
			st := &catalogMemoryStore{rows: rows, getErr: tc.getErr, deleteErr: map[string]error{"new": tc.deleteErr}}
			before := cloneCatalogTestObject(map[string]any{"rows": rows})
			w := httptest.NewRecorder()
			writeCatalogStore(st, w, httptest.NewRequest(tc.method, "/v1/skills/new", nil), map[string]any{"skill_id": "other"})
			if w.Code != tc.status || strings.Contains(w.Body.String(), "private-sql") {
				t.Fatal("状态或脱敏错误", w)
			}
			if st.putCalls != 0 {
				t.Fatal("详情读取或删除调用了写入")
			}
			if tc.method == "GET" || tc.deleteErr != nil {
				if !reflect.DeepEqual(before, cloneCatalogTestObject(map[string]any{"rows": rows})) {
					t.Fatal("只读或失败请求修改配置")
				}
			} else if rows["new"] != nil || rows["other"] == nil {
				t.Fatal("删除目标不正确", rows)
			}
		})
	}
	// 归档拒绝应在存储访问前生效，无数据库也不能回退到假清单。
	w := httptest.NewRecorder()
	writeCatalogPersist(catalogPersistenceHost{}, w, httptest.NewRequest("GET", "/v1/skills/x/archive", nil), nil)
	if w.Code != 501 {
		t.Fatal("缺存储时归档仍应明确未实现", w)
	}
}

// TestSkillBoundaryDeleteBody 验证详情删除只能删除路径ID，正文单值/数组和重复ID不能扩张目标；内存故障注入仅测存储边界，逐例重新建夹具无需外部清理。
func TestSkillBoundaryDeleteBody(t *testing.T) {
	for _, id := range []string{"new", "skills", "skill", "ordinary"} {
		for _, body := range []map[string]any{nil, {"skill_id": "other"}, {"id": "other"}, {"skillss": []any{"other", id, id}}, {"skill_id": id}} {
			st := &catalogMemoryStore{rows: map[string]map[string]any{id: {"skill_id": id}, "other": {"skill_id": "other"}}}
			w := httptest.NewRecorder()
			writeCatalogStore(st, w, httptest.NewRequest("DELETE", "/v1/skills/"+id, nil), body)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":1`) || st.rows[id] != nil || st.rows["other"] == nil {
				t.Fatalf("路径删除误删或目标被正文覆盖: id=%s body=%v status=%d response=%s rows=%v", id, body, w.Code, w.Body.String(), st.rows)
			}
		}
	}
}
