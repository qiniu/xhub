package searchconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProviders 验证公开目录字段、真实适配器默认URL及返回副本；未知值不猜测供应商。
// 纯单元测试无环境凭据，修改副本不污染下一次读取，无需清理。
func TestProviders(t *testing.T) {
	rows := Providers()
	if len(rows) != 3 {
		t.Fatal("供应商数量漂移")
	}
	for _, p := range rows {
		if p.Label == "" || ProviderEndpoint(p.Name) != p.Endpoint || !strings.HasPrefix(p.Endpoint, "https://") {
			t.Fatal("目录与执行默认端点不同", p)
		}
	}
	raw, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(raw), "https://") || !strings.Contains(string(raw), "ui_friendly_name") {
		t.Fatal("公开契约变化", string(raw), err)
	}
	rows[0].Name = "mutated"
	if Providers()[0].Name != "tavily" {
		t.Fatal("返回副本污染定义")
	}
	for _, name := range []string{"", "unknown", "TAVILY", " tavily ", "serper/extra"} {
		if ProviderEndpoint(name) != "" {
			t.Fatal("错误推断供应商", name)
		}
	}
}
