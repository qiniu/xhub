package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPromptStorageReadWrite 验证日志开关默认、开关两种持久化值、无权限写入及恢复默认。
// 前置私有数据库和真实管理会话；读取接口与实际日志执行设置必须一致，夹具最终删除专属 schema。
func TestPromptStorageReadWrite(t *testing.T) {
	cfg, st, db := testGatewayStores(t)
	// 通用夹具为其他日志回归默认启用正文；此用例显式从未配置的产品默认值开始。
	delete(cfg.GeneralRaw, "store_prompts_in_spend_logs")
	cfg.GeneralSettings.StorePromptsInSpendLogs = false
	server := httptest.NewServer(New(cfg, st, db).Handler())
	defer server.Close()
	key := loginAdmin(t, server.URL, db)
	// 每次通过 HTTP 读取而非内部配置快照，确保覆盖页面实际消费的目录契约。
	read := func(want bool, persisted any) {
		t.Helper()
		status, body := authed(t, server.URL, key, http.MethodGet, "/config/list?config_type=general_settings", nil)
		if status != 200 {
			t.Fatalf("日志配置读取失败：%d %s", status, body)
		}
		var rows []map[string]any
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, row := range rows {
			if row["field_name"] == "store_prompts_in_spend_logs" {
				count++
				if row["field_value"] != want || row["stored_in_db"] != persisted {
					t.Fatalf("日志开关读回错误：%v", row)
				}
			}
		}
		if count != 1 {
			t.Fatalf("日志字段缺失或重复：%d", count)
		}
	}
	read(false, nil)
	for _, value := range []bool{true, false} {
		payload, _ := json.Marshal(map[string]any{"general_settings": map[string]any{"store_prompts_in_spend_logs": value}})
		status, body := authed(t, server.URL, key, http.MethodPost, "/config/update", payload)
		if status != 200 {
			t.Fatalf("日志开关保存失败：%d %s", status, body)
		}
		read(value, true)
		if got, err := New(cfg, st, db).promptsEnabled(context.Background()); err != nil || got != value {
			t.Fatalf("新服务实例实际执行值错误：%v", got)
		}
	}
	status, body := authed(t, server.URL, "invalid-credential", http.MethodPost, "/config/update", []byte(`{"general_settings":{"store_prompts_in_spend_logs":true}}`))
	if status != 401 {
		t.Fatalf("未认证写入必须拒绝：%d %s", status, body)
	}
	read(false, true)
	status, body = authed(t, server.URL, key, http.MethodPost, "/config/field/delete", []byte(`{"config_type":"general_settings","field_name":"store_prompts_in_spend_logs"}`))
	if status != 200 {
		t.Fatalf("日志配置清理失败：%d %s", status, body)
	}
	read(false, nil)
}
