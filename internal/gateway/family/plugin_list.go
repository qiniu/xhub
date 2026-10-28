package family

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// writePluginList 处理精确插件配置列表并遵守enabled_only；st非空，w/r来自已鉴权且正文已校验的目录请求。
// 命中返回true并写响应，否则返回false不写响应；列表故障沿用脱敏500，参数错误400，读取绝不持久化。
// 只筛选配置元数据，不表示插件已经安装、发布或执行，也不改变任何身份或预算规则。
func writePluginList(st catalogRecordStore, w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || strings.TrimSuffix(r.URL.Path, "/") != "/claude-code/plugins" {
		return false
	}
	query, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		util.WriteError(w, 400, "invalid_request", "invalid plugin list query")
		return true
	}
	values, present := query["enabled_only"]
	only := false
	if present {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			util.WriteError(w, 400, "invalid_request", "enabled_only must be a single true or false value")
			return true
		}
		only = values[0] == "true"
	}
	rows, err := st.ListKV("claude-code")
	if err != nil {
		writeCatalogStoreFailure(w, "claude-code", "list", err)
		return true
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		// 与旧列表一致：缺失/null沿用默认true；无效类型不得被猜测成true。
		Freeze("claude-code", row)
		if !only || row["enabled"] == true {
			result = append(result, row)
		}
	}
	util.WriteJSON(w, 200, catalogListBody("claude-code", r.URL.Path, redactCatalogList("claude-code", result, true)))
	return true
}
