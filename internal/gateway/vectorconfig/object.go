package vectorconfig

import (
	"github.com/sunqirui1987/xhub/internal/util"
	"time"
)

// Normalize 补齐向量库管理配置的旧默认字段；obj为可修改JSON对象，供服务和兼容Freeze调用。
// 仅补nil和删除顶层password，不落库；status/file_counts为历史元数据，不代表已执行索引或检索。
// 数据面复数vector_stores仍走原路径，此函数只处理管理命名空间。
func Normalize(obj map[string]any) {
	delete(obj, "password")
	if obj["id"] == nil {
		obj["id"] = "vector_store_" + util.CallID()[:12]
	}
	if obj["created_at"] == nil {
		obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	if obj["vector_store_id"] == nil {
		id, _ := obj["id"].(string)
		obj["vector_store_id"] = id
	}
	if obj["object"] == nil {
		obj["object"] = "vector_store"
	}
	if obj["name"] == nil {
		obj["name"] = ""
	}
	if obj["status"] == nil {
		obj["status"] = "completed"
	}
	if obj["file_counts"] == nil {
		obj["file_counts"] = map[string]any{"in_progress": 0, "completed": 0, "failed": 0, "cancelled": 0, "total": 0}
	}
	if obj["vector_store_name"] == nil {
		obj["vector_store_name"] = obj["name"]
	}
}
