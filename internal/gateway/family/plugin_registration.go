package family

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

var pluginRegistrationName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// pluginCreator 限定注册需要的原子创建能力；数据库冲突返回false，其他错误不得降级成普通覆盖写入。
type pluginCreator interface {
	CreateKVIfAbsent(context.Context, string, string, string) (bool, error)
}

// rejectPluginMarketplace 拒绝没有生成器的安装目录；参数是已鉴权且JSON已校验的响应和请求。
// 管理适配器调用，命中写501并返回true，否则无副作用返回false；不把普通KV内容当作可安装清单，也不开放匿名权限。
func rejectPluginMarketplace(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || strings.TrimSuffix(r.URL.Path, "/") != "/claude-code/marketplace.json" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, 501, "unsupported_operation", "plugin marketplace manifest generation is not implemented")
	return true
}

// writePluginRegistration 处理已鉴权的精确POST注册；st为目录存储，body为合法JSON对象，w/r为响应和请求。
// 命中返回true，按名称仅创建且重名409；参数错误400、缺能力503、存储错误500。只登记来源元数据，不联网安装或发布插件。
func writePluginRegistration(st catalogRecordStore, w http.ResponseWriter, r *http.Request, body map[string]any) bool {
	if r.Method != http.MethodPost || strings.TrimSuffix(r.URL.Path, "/") != "/claude-code/plugins" {
		return false
	}
	name, ok := body["name"].(string)
	source, sourceOK := body["source"].(map[string]any)
	if !ok || !pluginRegistrationName.MatchString(name) || !sourceOK || len(source) == 0 {
		util.WriteError(w, 400, "invalid_request", "plugin registration requires a kebab-case name and a non-empty source object")
		return true
	}
	for _, v := range source {
		if _, ok := v.(string); !ok {
			util.WriteError(w, 400, "invalid_request", "plugin source values must be strings")
			return true
		}
	}
	creator, ok := st.(pluginCreator)
	if !ok {
		util.WriteError(w, 503, "unavailable", "atomic plugin registration unavailable")
		return true
	}
	// 历史注册曾把name存入固定ID=plugins；阻止新记录悄悄遮蔽同名旧数据，不在本修复中迁移或删除。
	rows, err := st.ListKV("claude-code")
	if err != nil {
		writeCatalogStoreFailure(w, "claude-code", "register", err)
		return true
	}
	for _, row := range rows {
		if row["name"] == name {
			util.WriteError(w, 409, "conflict", "plugin name already exists")
			return true
		}
	}
	obj := make(map[string]any, len(body)+3)
	for k, v := range body {
		obj[k] = v
	}
	obj["id"], obj["claude-code_id"], obj["name"] = name, name, name
	Freeze("claude-code", obj)
	raw, err := json.Marshal(obj)
	if err != nil {
		writeCatalogStoreFailure(w, "claude-code", "register", err)
		return true
	}
	created, err := creator.CreateKVIfAbsent(r.Context(), "claude-code", name, string(raw))
	if err != nil {
		writeCatalogStoreFailure(w, "claude-code", "register", err)
		return true
	}
	if !created {
		util.WriteError(w, 409, "conflict", "plugin name already exists")
		return true
	}
	// 保留旧成功对象的平铺形状，避免把恢复注册寻址顺便变成响应包络迁移。
	util.WriteJSON(w, 200, redactCatalogObject("claude-code", obj, false))
	return true
}
