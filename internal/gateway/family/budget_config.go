package family

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/sunqirui1987/xhub/internal/gateway/budgetconfig"
	"github.com/sunqirui1987/xhub/internal/util"
)

// writeBudgetConfigStore 将已鉴权且已解析的旧预算配置请求适配到独立服务。
// st 非空；kind/action/id 来自旧路由，body 为合法对象；只负责 HTTP 包络、删除 ID 提取和错误映射。
// 无返回值；存储失败沿用旧错误文案，未改变权限、预算、URL 或成功/失败状态码。
func writeBudgetConfigStore(st catalogRecordStore, w http.ResponseWriter, r *http.Request, kind, action, id string, body map[string]any) {
	svc := budgetconfig.Service{Store: st, Kind: kind}
	var result any
	var err error
	switch action {
	case "list":
		var rows []map[string]any
		rows, err = svc.List()
		result = catalogListBody(kind, r.URL.Path, rows)
	case "info":
		if id == "" {
			util.WriteJSON(w, 200, map[string]any{"object": "list", "data": []any{}})
			return
		}
		result, err = svc.Get(id)
		if errors.Is(err, sql.ErrNoRows) {
			util.WriteError(w, 404, "not_found", "catalog resource not found")
			return
		}
	case "delete":
		ids := idsFrom(body, kind+"s", idField(kind))
		if id != "" {
			ids = append(ids, id)
		}
		var n int
		n, err = svc.Delete(ids)
		result = map[string]any{"deleted": n, "budget_id": id, "id": id}
	case "update":
		result, err = svc.Update(id, body)
	default:
		result, err = svc.Create(id, body)
	}
	if err != nil {
		writeCatalogStoreFailure(w, kind, action, err)
		return
	}
	util.WriteJSON(w, 200, result)
}
