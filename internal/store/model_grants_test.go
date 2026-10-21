package store

import (
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
	"reflect"
	"testing"
)

// TestRenamedModelGrants 验证显式授权替换、共享名保留、去重和继承边界；纯内存输入，无清理。
func TestRenamedModelGrants(t *testing.T) {
	for _, tc := range []struct {
		name          string
		in, want      []string
		keep, changed bool
	}{
		{"replace", []string{"old", "other"}, []string{"new", "other"}, false, true},
		{"shared", []string{"old"}, []string{"old", "new"}, true, true},
		{"deduplicate", []string{"old", "new"}, []string{"new"}, false, true},
		{"inherit", nil, nil, false, false},
		{"unrelated", []string{"other"}, []string{"other"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := renamedModelGrants(tc.in, "old", "new", tc.keep)
			if !reflect.DeepEqual(got, tc.want) || changed != tc.changed {
				t.Fatalf("授权迁移错误: %v %v", got, changed)
			}
		})
	}
}

// TestRenameGrantsRollsBackWithDirectory 前置独立PG及损坏路由模板，验证授权和部署同时回滚。
// 使用真实存储事务，不调用上游；测试夹具自动关闭连接并删除私有schema。
func TestRenameGrantsRollsBackWithDirectory(t *testing.T) {
	st, db := openModelWeightStores(t)
	org, err := db.CreateOrgWithConfig(t.Context(), iam.Actor{Kind: "system"}, "rename", nil, iam.OrgConfig{Models: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	entry := ProxyModel{ID: "rename", ModelName: "old", Params: map[string]any{}, Info: map[string]any{"id": "rename"}}
	if err := st.SaveModelDirectory(&entry, "", []config.ModelEntry{weightDeployment("old", "rename")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateRouteTemplate(t.Context(), iam.Actor{Kind: "system"}, iam.TemplateOwner{}, "broken", "{"); err != nil {
		t.Fatal(err)
	}
	entry.ModelName = "new"
	if err := st.SaveModelDirectory(&entry, "", []config.ModelEntry{weightDeployment("new", "rename")}); err == nil {
		t.Fatal("损坏模板未阻止改名")
	}
	var models string
	if err := st.DB.QueryRow("SELECT models FROM organizations WHERE id=$1", org.ID).Scan(&models); err != nil {
		t.Fatal(err)
	}
	if models != "[\"old\"]" {
		t.Fatalf("失败事务留下了迁移授权: %s", models)
	}
	entries, err := st.ListProxyModels()
	if err != nil || len(entries) != 1 || entries[0].ModelName != "old" {
		t.Fatalf("失败事务改变了部署: %v %v", entries, err)
	}
}
