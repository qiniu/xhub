package usage

import (
	"fmt"
	"net/http"

	"github.com/sunqirui1987/xhub/internal/util"
)

// tagCatalogReader 仅允许读取配置目录，便于对真实存储失败与字典转换独立测试；不提供写入或预算能力。
type tagCatalogReader interface {
	ListKV(string) ([]map[string]any, error)
}

// readTagCatalog 读取与/tag/new、update、delete共用的tag命名空间，返回前端约定的名称字典。
// st为非nil读取器；正常空库返回{}，存储或损坏/重名记录返回错误而非静默丢失配置。仅浅拷贝顶层，不修改存储行或解释额度/模型字段。
func readTagCatalog(st tagCatalogReader) (map[string]any, error) {
	rows, err := st.ListKV("tag")
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		name, _ := row["name"].(string)
		if name == "" {
			name, _ = row["id"].(string)
		}
		if name == "" {
			return nil, fmt.Errorf("tag record has no name")
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("duplicate tag name")
		}
		item := make(map[string]any, len(row)+1)
		for k, v := range row {
			if k != "password" {
				item[k] = v
			}
		}
		item["name"] = name
		out[name] = item
	}
	return out, nil
}

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
	rows, err := readTagCatalog(st)
	if err != nil {
		util.Error("tag catalog read failed: %v", err)
		util.WriteError(w, 500, "internal", "catalog store failure")
		return
	}
	util.WriteJSON(w, 200, rows)
}
