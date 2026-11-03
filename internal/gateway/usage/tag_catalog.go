package usage

import (
	"net/http"

	"github.com/sunqirui1987/xhub/internal/gateway/metadataconfig"

	"github.com/sunqirui1987/xhub/internal/util"
)

// TagList 在既有平台管理鉴权后返回真实标签配置；s为网关宿主，w/r为HTTP输出和查询。
// 日期参数保留可接受，但配置列表不按用量日期过滤。空库200{}、缺库503、读取/转换失败500；不新增标签路由、额度或用量聚合规则。
func TagList(s Host, w http.ResponseWriter, r *http.Request) {
	if s.RequireManage(w, r) == nil {
		return
	}
	st := s.RecordStore()
	if st == nil {
		util.WriteError(w, 503, "unavailable", "catalog store unavailable")
		return
	}
	rows, err := metadataconfig.ReadTagCatalog(st)
	if err != nil {
		util.Error("tag catalog read failed: %v", err)
		util.WriteError(w, 500, "internal", "catalog store failure")
		return
	}
	util.WriteJSON(w, 200, rows)
}
