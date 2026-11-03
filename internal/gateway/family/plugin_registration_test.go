package family

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// pluginCreateStore提供确定性创建结果并记录写入；嵌入普通目录夹具，不替代真实数据库原子性测试。
type pluginCreateStore struct {
	catalogMemoryStore
	exists bool
	err    error
	calls  int
}

// CreateKVIfAbsent模拟原子创建的成功/冲突/失败；参数沿生产签名，返回结果用于HTTP映射，无外部数据。
func (s *pluginCreateStore) CreateKVIfAbsent(_ context.Context, kind, id, body string) (bool, error) {
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	if s.exists {
		return false, nil
	}
	return true, s.PutKV(kind, id, body)
}

// TestPluginRegistration验证格式、冲突、存储异常、编码错误和输入不被改写；仅内存夹具，测试结束释放。
func TestPluginRegistration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   map[string]any
		st     *pluginCreateStore
		status int
	}{
		{"valid", map[string]any{"name": "example-plugin", "source": map[string]any{"source": "github", "repo": "org/repo"}, "id": "wrong", "password": "private"}, &pluginCreateStore{}, 200},
		{"legacy-name", map[string]any{"name": "example", "source": map[string]any{"source": "github"}}, &pluginCreateStore{catalogMemoryStore: catalogMemoryStore{rows: map[string]map[string]any{"plugins": {"id": "plugins", "name": "example"}}}}, 409},
		{"read-failure", map[string]any{"name": "example", "source": map[string]any{"source": "github"}}, &pluginCreateStore{catalogMemoryStore: catalogMemoryStore{listErr: errors.New("private-read")}}, 500},
		{"duplicate", map[string]any{"name": "example", "source": map[string]any{"source": "github"}}, &pluginCreateStore{exists: true}, 409},
		{"db-failure", map[string]any{"name": "example", "source": map[string]any{"source": "github"}}, &pluginCreateStore{err: errors.New("private-sql")}, 500},
		{"uppercase-name", map[string]any{"name": "example-CN", "source": map[string]any{"source": "github"}}, &pluginCreateStore{}, 400},
		{"bad-name", map[string]any{"name": "../escape", "source": map[string]any{"source": "github"}}, &pluginCreateStore{}, 400},
		{"missing-name", map[string]any{"source": map[string]any{"source": "github"}}, &pluginCreateStore{}, 400},
		{"empty-source", map[string]any{"name": "example", "source": map[string]any{}}, &pluginCreateStore{}, 400},
		{"bad-source", map[string]any{"name": "example", "source": "github"}, &pluginCreateStore{}, 400},
		{"bad-value", map[string]any{"name": "example", "source": map[string]any{"repo": true}}, &pluginCreateStore{}, 400},
		{"encoding", map[string]any{"name": "example", "source": map[string]any{"source": "github"}, "bad": make(chan int)}, &pluginCreateStore{}, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := callCatalogStore(tc.st, "POST", "/claude-code/plugins", tc.body)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "private") {
				t.Fatal("注册错误或秘密泄露", w.Code, w.Body)
			}
			if tc.status == 200 {
				var got map[string]any
				if json.Unmarshal(w.Body.Bytes(), &got) != nil || got["id"] != "example-plugin" || got["name"] != "example-plugin" || tc.st.rows["example-plugin"] == nil || tc.body["id"] != "wrong" || tc.body["password"] != "private" {
					t.Fatal("注册身份或输入被错误修改", w.Body)
				}
			}
			if tc.status == 400 && tc.st.calls != 0 {
				t.Fatal("非法参数访问了存储")
			}
		})
	}
	w := callCatalogStore(&catalogMemoryStore{}, "POST", "/claude-code/plugins", map[string]any{"name": "example", "source": map[string]any{"source": "github"}})
	if w.Code != 503 {
		t.Fatal("缺原子存储能力不能退化为覆盖", w.Code)
	}
}

// TestPluginRegistrationRoutes验证精确方法/路径、保留词寻址及marketplace拒绝；无数据库，无需清理。
func TestPluginRegistrationRoutes(t *testing.T) {
	for _, name := range []string{"list", "new", "register", "plugins", "enable", "disable"} {
		for _, op := range []struct{ method, action string }{{"GET", "info"}, {"PUT", "update"}, {"DELETE", "delete"}} {
			kind, action, id := parseMgmt(op.method, "/claude-code/plugins/"+name, nil)
			if kind != "claude-code" || action != op.action || id != name {
				t.Fatal("插件名称被动作词吞掉", name, kind, action, id)
			}
		}
	}
	for _, tc := range []struct {
		method, path string
		matched      bool
	}{{"GET", "/claude-code/marketplace.json", true}, {"GET", "/claude-code/marketplace.json/", true}, {"POST", "/claude-code/marketplace.json", false}, {"GET", "/claude-code/plugins/marketplace.json", false}} {
		w := httptest.NewRecorder()
		if rejectPluginMarketplace(w, httptest.NewRequest(tc.method, tc.path, nil)) != tc.matched {
			t.Fatal("marketplace匹配错误", tc)
		}
		if tc.matched && (w.Code != 501 || w.Header().Get("Cache-Control") != "no-store") {
			t.Fatal("marketplace拒绝错误", w.Code)
		}
	}
	w := httptest.NewRecorder()
	if writePluginRegistration(nil, w, httptest.NewRequest("GET", "/claude-code/plugins", nil), nil) || w.Body.Len() != 0 {
		t.Fatal("注册处理器误处理列表")
	}
}
