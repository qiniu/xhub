package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/store"
	"github.com/sunqirui1987/xhub/internal/util"
)

// routeAuditSnapshot 仅保存入口注册证据；分组名不是能力或业务成功声明。
// Catalog 包含每条目录声明，ModuleOnly 补充目录之外的动态模块入口；无数据库和请求副作用。
type routeAuditSnapshot struct {
	Catalog    map[string][]string `json:"catalog"`
	ModuleOnly map[string][]string `json:"module_only"`
}

// routeAuditRegistrar 将模块声明交给真实 handle，按实际注册结果记录首个拥有者。
// server 是仅挂路由的进程，owner 是当前模块名；不执行处理器、不连接外部服务。
type routeAuditRegistrar struct {
	server   *Server
	owner    string
	owners   map[string]string
	patterns map[string]string
}

// newRouteAuditServer 返回仅用于注册审计的内存进程；无需参数，不启动后台任务或数据库。
// installModules 与生产共用模块列表，处理器只被挂载；业务行为必须由独立真实回归证明。
func newRouteAuditServer() *Server {
	s := &Server{Cfg: &config.Config{}, Store: &store.Store{}, engine: newEngine(), registered: map[string]struct{}{}, catalog: catalog.Load()}
	s.installModules()
	return s
}

// routeAuditKey 使用真实 Gin 参数规范化规则计算注册键，供审计匹配参数别名。
// pattern 为 METHOD /path；非法格式返回空串，不产生注册副作用。
func routeAuditKey(pattern string) string {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return ""
	}
	gp, _ := ginPathNames(path)
	return method + " " + gp
}

// Handle 将 pattern 与 h 交给生产注册器；仅当真实注册表新增时记下 owner，保留先注册者。
// 无效、nil 或被移除的入口不会凭审计逻辑恢复；参数别名同样受生产去重规则控制。
func (r *routeAuditRegistrar) Handle(pattern string, h http.HandlerFunc) {
	key := routeAuditKey(pattern)
	_, before := r.server.registered[key]
	r.server.handle(pattern, h)
	if _, after := r.server.registered[key]; after && !before {
		r.owners[key] = r.owner
		r.patterns[key] = pattern
	}
}

// collectRouteAudit 挂载实际模块与目录并分组返回注册证据；s 需尚未挂载路由。
// 返回快照与真实 Gin 路由键；分类使用实际 handlerFor 返回的函数和现有鉴权分类，不推测其内部业务。
func collectRouteAudit(s *Server) (routeAuditSnapshot, []string) {
	out := routeAuditSnapshot{Catalog: map[string][]string{}, ModuleOnly: map[string][]string{}}
	recorder := &routeAuditRegistrar{server: s, owners: map[string]string{}, patterns: map[string]string{}}
	for _, m := range s.modules {
		recorder.owner = m.Name()
		m.Mount(recorder)
	}
	catalogKeys := map[string]bool{}
	selected := map[string]string{}
	for _, rt := range s.catalog {
		pattern := rt.M + " " + rt.P
		key := routeAuditKey(pattern)
		catalogKeys[key] = true
		handler := s.handlerFor(rt.P)
		s.handle(pattern, handler)
		bucket := "removed"
		if _, mounted := s.registered[key]; mounted {
			if owner, ok := recorder.owners[key]; ok {
				bucket = "module:" + owner
			} else if prior, ok := selected[key]; ok {
				// 参数别名共享 Gin 键；记录真正先挂载的处理器，不能将后续候选误记为生效。
				bucket = prior
			} else {
				name := runtime.FuncForPC(reflect.ValueOf(handler).Pointer()).Name()
				name = strings.TrimSuffix(name[strings.LastIndex(name, ".")+1:], "-fm")
				class := "manage"
				if catalog.IsPublicPath(rt.M, rt.P) {
					class = "public"
				} else if catalog.AuthOf(rt.P) == catalog.AuthData {
					class = "data"
				} else if catalog.AuthOf(rt.P) == catalog.AuthMixed {
					class = "mixed"
				}
				bucket = "catalog:" + name + ":" + class
				selected[key] = bucket
			}
		}
		out.Catalog[bucket] = append(out.Catalog[bucket], pattern)
	}
	for key, owner := range recorder.owners {
		if !catalogKeys[key] {
			out.ModuleOnly[owner] = append(out.ModuleOnly[owner], recorder.patterns[key])
		}
	}
	for _, groups := range []map[string][]string{out.Catalog, out.ModuleOnly} {
		for _, rows := range groups {
			sort.Strings(rows)
		}
	}
	return out, routeAuditEngineKeys(s)
}

// routeAuditEngineKeys 读取真实 Gin 引擎路由键并排序，供审计与正常 mountModules/mountCatalog 结果比对。
// s 为已挂载内存进程；返回副本，不执行 HTTP 处理器或修改注册表。
func routeAuditEngineKeys(s *Server) []string {
	out := []string{}
	for _, rt := range s.engine.Routes() {
		out = append(out, rt.Method+" "+rt.Path)
	}
	sort.Strings(out)
	return out
}

// checkRouteAudit 将实际分组与已评审 JSON 比较，供 CI 阻止静默归属漂移。
// want 是快照字节，got 是当前注册结果；非法/未知字段及差异均返回错误，无文件修改。
func checkRouteAudit(want []byte, got routeAuditSnapshot) error {
	actual, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		return err
	}
	// 使用规范字节比较，避免重复 JSON 键、缺字段或尾随内容被宽松解码隐藏。
	if !bytes.Equal(want, append(actual, '\n')) {
		return fmt.Errorf("路由归属快照不同；检查注册/移除/覆盖变化，再显式更新 testdata/route-ownership.json；快照不是业务覆盖证明")
	}
	return nil
}

// TestRouteOwnershipAudit 检查全部实际模块（含动态路径）与目录优先级，再对照已评审快照。
// 前置无外部依赖；只注册内存路由，结果必须等同生产挂载过程；显式更新模式仅供本地生成，不影响 CI 默认只读检查。
func TestRouteOwnershipAudit(t *testing.T) {
	got, keys := collectRouteAudit(newRouteAuditServer())
	normal := newRouteAuditServer()
	normal.mountModules()
	normal.mountCatalog()
	if !reflect.DeepEqual(keys, routeAuditEngineKeys(normal)) {
		t.Fatal("审计与正常网关挂载结果不一致")
	}
	const path = "testdata/route-ownership.json"
	if os.Getenv("XHUB_UPDATE_ROUTE_AUDIT") == "1" {
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRouteAudit(want, got); err != nil {
		t.Fatal(err)
	}
	for group, rows := range got.Catalog {
		t.Logf("%s: %d", group, len(rows))
	}
	t.Logf("实际注册路由: %d", len(keys))
}

// TestRouteAuditRegistrarBoundaries 用真实 Gin HTTP 调用验证先注册者和参数还原；非法或退役入口不得恢复。
// 前置内存进程与两个确定性处理器；断言响应来自首个处理器，测试结束没有外部资源需要清理。
func TestRouteAuditRegistrarBoundaries(t *testing.T) {
	s := newRouteAuditServer()
	r := &routeAuditRegistrar{server: s, owner: "first", owners: map[string]string{}, patterns: map[string]string{}}
	r.Handle("GET /audit/{user_id}", func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, req.PathValue("user_id")) })
	r.owner = "second"
	r.Handle("GET /audit/{id}", func(w http.ResponseWriter, req *http.Request) { t.Error("后注册处理器覆盖了原路由") })
	r.Handle("invalid", func(http.ResponseWriter, *http.Request) {})
	r.Handle("GET /audit/nil", nil)
	r.Handle("GET /access_group/example/budget", func(http.ResponseWriter, *http.Request) {})
	if len(r.owners) != 1 || r.owners["GET /audit/:p2"] != "first" {
		t.Fatalf("错误的有效归属: %v", r.owners)
	}
	reply := httptest.NewRecorder()
	s.engine.ServeHTTP(reply, httptest.NewRequest("GET", "/audit/alice", nil))
	if reply.Code != 200 || reply.Body.String() != "alice" {
		t.Fatalf("首个真实处理器或参数丢失: %d %s", reply.Code, reply.Body.String())
	}
}

// TestRouteAuditDetectsDrift 验证归属、增删及非法快照确实触发门禁；不调用外部服务，无需清理。
// 基线使用小型确定性分组，失败输入逐项变异，不能将更新快照当作测试通过的默认路径。
func TestRouteAuditDetectsDrift(t *testing.T) {
	base := routeAuditSnapshot{Catalog: map[string][]string{"module:identity": {"GET /user/list"}}, ModuleOnly: map[string][]string{}}
	data, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := checkRouteAudit(data, base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		groups map[string][]string
	}{
		{"owner", map[string][]string{"catalog:serveFamilyRoute:manage": {"GET /user/list"}}},
		{"added", map[string][]string{"module:identity": {"GET /user/list", "POST /user/new"}}},
		{"removed", map[string][]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if checkRouteAudit(data, routeAuditSnapshot{Catalog: tc.groups, ModuleOnly: base.ModuleOnly}) == nil {
				t.Fatal("归属漂移未阻止通过")
			}
		})
	}
	for _, bad := range [][]byte{nil, []byte("{}"), []byte("not-json"), append(append([]byte{}, data...), []byte("{}")...)} {
		if checkRouteAudit(bad, base) == nil {
			t.Fatal("非法或缺失快照未阻止通过")
		}
	}
}

// TestRouteAuditMountDrift 从真实模块表删除 identity、增加目录以及改变既有路由归属，验证快照能抓住实际注册变化。
// 前置仅内存路由，无依赖或持久化；每个用例重新挂载，原快照必须拒绝变化，避免仅测试手写 JSON 的自我验证。
func TestRouteAuditMountDrift(t *testing.T) {
	want, err := os.ReadFile("testdata/route-ownership.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"remove-module", "add-catalog", "remove-catalog", "replace-catalog-handler"} {
		t.Run(name, func(t *testing.T) {
			s := newRouteAuditServer()
			switch name {
			case "remove-module":
				for i, m := range s.modules {
					if m.Name() == "identity" {
						s.modules = append(s.modules[:i], s.modules[i+1:]...)
						break
					}
				}
			case "add-catalog":
				s.catalog = append(s.catalog, catalog.Route{M: "GET", P: "/audit/new"})
			case "remove-catalog":
				s.catalog = s.catalog[1:]
			case "replace-catalog-handler":
				s.modules = append(s.modules, util.Bind("audit", func(reg util.Registrar) { reg.Handle("GET /budget/list", func(http.ResponseWriter, *http.Request) {}) }))
			}
			got, _ := collectRouteAudit(s)
			if checkRouteAudit(want, got) == nil {
				t.Fatalf("%s 实际注册变化未被发现", name)
			}
		})
	}
}
