package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// phase4Closure 保存按精确入口评审的收尾范围与历史观察；它不是OpenAPI支持声明。
// 基线对应main提交；Groups描述批次，Routes标注证据；其他目录与模块数量防止边界悄悄扩大或丢失。
type phase4Closure struct {
	Baseline   string `json:"baseline"`
	ReviewedAt string `json:"reviewed_at"`
	Scope      string `json:"scope"`
	Groups     map[string]struct {
		Title      string `json:"title"`
		Batch      string `json:"batch"`
		Source     string `json:"source"`
		Conclusion string `json:"conclusion"`
		PendingPRs []int  `json:"pending_prs"`
	} `json:"groups"`
	Routes []struct {
		Route       string   `json:"route"`
		Owner       string   `json:"owner"`
		Group       string   `json:"group"`
		MainStatus  string   `json:"main_status"`
		Evidence    []string `json:"evidence"`
		Observation struct {
			Route         string `json:"route"`
			Anonymous     int    `json:"anonymous"`
			User          int    `json:"user"`
			Admin         int    `json:"admin"`
			KVChanged     bool   `json:"kv_changed"`
			UpstreamCalls int    `json:"upstream_calls"`
		} `json:"observation"`
	} `json:"routes"`
	OtherCatalogCounts map[string]int `json:"other_catalog_counts"`
	ModuleOnlyCounts   map[string]int `json:"module_only_counts"`
}

// checkPhase4Closure 对照实际注册结果验证清单完整性；plan为静态评审记录，snapshot为生产注册器输出。
// 返回缺失、重复、错误归属或无证据等错误；只读取证据文件，不修改配置，不把观察状态码当业务正确性。
func checkPhase4Closure(plan phase4Closure, snapshot routeAuditSnapshot) error {
	expected := map[string]string{}
	other := map[string]int{}
	modules := map[string]int{}
	for owner, routes := range snapshot.Catalog {
		if strings.HasSuffix(owner, ":manage") {
			for _, route := range routes {
				expected[route] = owner
			}
		} else {
			other[owner] = len(routes)
		}
	}
	for owner, routes := range snapshot.ModuleOnly {
		modules[owner] = len(routes)
	}
	if len(plan.Baseline) != 40 || plan.ReviewedAt == "" || plan.Scope == "" {
		return fmt.Errorf("缺少评审基线")
	}
	if !reflect.DeepEqual(other, plan.OtherCatalogCounts) || !reflect.DeepEqual(modules, plan.ModuleOnlyCounts) {
		return fmt.Errorf("排除范围漂移，必须重新评审边界")
	}
	seen := map[string]bool{}
	used := map[string]bool{}
	for _, row := range plan.Routes {
		if seen[row.Route] || expected[row.Route] == "" || expected[row.Route] != row.Owner {
			return fmt.Errorf("重复、未知或错误归属: %s", row.Route)
		}
		seen[row.Route] = true
		group, ok := plan.Groups[row.Group]
		if !ok || group.Title == "" || group.Batch == "" || group.Conclusion == "" {
			return fmt.Errorf("缺少批次或结论: %s", row.Route)
		}
		used[row.Group] = true
		switch row.MainStatus {
		case "verified", "needs_fix", "needs_verification", "unsupported":
		default:
			return fmt.Errorf("非法状态: %s", row.Route)
		}
		if len(row.Evidence) == 0 {
			return fmt.Errorf("没有证据: %s", row.Route)
		}
		realRegression := false
		for _, path := range append(append([]string{}, row.Evidence...), group.Source) {
			if filepath.IsAbs(path) || strings.Contains(path, "..") {
				return fmt.Errorf("证据必须为仓库内路径")
			}
			if _, err := os.Stat(filepath.Join("../..", path)); err != nil {
				return fmt.Errorf("证据不存在: %s", path)
			}
			realRegression = realRegression || (strings.HasPrefix(path, "cmd/regression/") && strings.HasSuffix(path, "_test.go"))
		}
		if row.MainStatus == "verified" && !realRegression {
			return fmt.Errorf("已验证入口缺真实回归证据: %s", row.Route)
		}
		o := row.Observation
		if o.Route != row.Route || o.Anonymous < 100 || o.Anonymous > 599 || o.User < 100 || o.User > 599 || o.Admin < 100 || o.Admin > 599 || o.UpstreamCalls < 0 {
			return fmt.Errorf("观察记录缺失或不匹配: %s", row.Route)
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("收尾清单缺项: %d/%d", len(seen), len(expected))
	}
	if len(used) != len(plan.Groups) {
		return fmt.Errorf("批次未绑定任何入口")
	}
	return nil
}

// readPhase4Closure 为审计测试读取评审文件；t用于报告错误，返回独立对象以免变异用例互相污染。
// 无外部依赖，拒绝未知字段和非规范JSON，文件由代码评审人工更新，CI不自动接受路由变化。
func readPhase4Closure(t *testing.T) phase4Closure {
	t.Helper()
	raw, err := os.ReadFile("testdata/phase4-closure.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan phase4Closure
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&plan); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(append(canonical, '\n')) != string(raw) {
		t.Fatal("清单JSON含重复键、尾随内容或未规范化字段")
	}
	return plan
}

// TestPhase4ClosureInventory 验证所有剩余入口都有独立状态和证据；前置实际路由注册，无数据库。
// 成功只表示清单完整，不证明业务验收；不写文件，无清理副作用。
func TestPhase4ClosureInventory(t *testing.T) {
	snapshot, _ := collectRouteAudit(newRouteAuditServer())
	if err := checkPhase4Closure(readPhase4Closure(t), snapshot); err != nil {
		t.Fatal(err)
	}
}

// TestPhase4ClosureRejectsDrift 验证遗漏、重复、错误归属、虚假已验证及缺失观察不能通过清单门禁。
// 每个子例读取独立基线并仅改内存，预期返回错误；不访问生产，不产生需要清理的数据。
func TestPhase4ClosureRejectsDrift(t *testing.T) {
	snapshot, _ := collectRouteAudit(newRouteAuditServer())
	cases := map[string]func(*phase4Closure){
		"missing":   func(p *phase4Closure) { p.Routes = p.Routes[1:] },
		"duplicate": func(p *phase4Closure) { p.Routes = append(p.Routes, p.Routes[0]) },
		"owner":     func(p *phase4Closure) { p.Routes[0].Owner = "module:identity" },
		"status":    func(p *phase4Closure) { p.Routes[0].MainStatus = "200-is-success" },
		"unproved": func(p *phase4Closure) {
			p.Routes[0].MainStatus = "verified"
			p.Routes[0].Evidence = []string{"internal/gateway/family/handlers.go"}
		},
		"observation": func(p *phase4Closure) { p.Routes[0].Observation.Admin = 0 },
		"boundary":    func(p *phase4Closure) { p.ModuleOnlyCounts["identity"]++ },
		"batch":       func(p *phase4Closure) { p.Routes[0].Group = "invented" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			plan := readPhase4Closure(t)
			mutate(&plan)
			if checkPhase4Closure(plan, snapshot) == nil {
				t.Fatal("错误清单被接受")
			}
		})
	}
}
