package family

import (
	"github.com/sunqirui1987/xhub/internal/util"
	"net/http"
	"strings"
)

// unsupportedSettingsOperations 只登记没有真实消费者的15条旧设置入口；真实主题和config/update不在此范围。
var unsupportedSettingsOperations = map[string]struct{}{
	"GET /config/field/info":               {},
	"GET /config/yaml":                     {},
	"GET /get/default_team_settings":       {},
	"GET /get/internal_user_settings":      {},
	"GET /get/ui_settings":                 {},
	"GET /get/user_banner":                 {},
	"GET /get_favicon":                     {},
	"GET /get_image":                       {},
	"GET /get_logo_url":                    {},
	"GET /settings":                        {},
	"GET /sso/get/ui_settings":             {},
	"PATCH /update/default_team_settings":  {},
	"PATCH /update/internal_user_settings": {},
	"PATCH /update/ui_settings":            {},
	"PATCH /update/user_banner":            {},
}

// rejectSettingsOperation 为已鉴权、完成JSON校验的旧设置请求返回明确不支持。
// w/r为目录调度器的输出与请求；精确方法、路径（允许一个尾斜杠）命中时返回true，其他返回false。
// 不读取或迁移遗留KV：这些值从未接入默认用户/团队、横幅等消费者，贸然启用会改变权限和预算规则。
func rejectSettingsOperation(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := unsupportedSettingsOperations[r.Method+" "+strings.TrimSuffix(r.URL.Path, "/")]; !ok {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	util.WriteError(w, http.StatusNotImplemented, "unsupported_settings_operation", "This legacy settings endpoint has no implementation; existing runtime settings are unchanged")
	return true
}
