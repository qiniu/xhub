package family

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sunqirui1987/xhub/internal/gateway/metadataconfig"
	"github.com/sunqirui1987/xhub/internal/util"
)

// catalogRecordStore 限定通用目录需要的读写能力，不扩展 Host 或鉴权边界。
// 参数由方法声明给出；Get/Delete 的 sql.ErrNoRows 表示不存在，其余错误必须向调用方传播。
// 生产由 store.Store 实现，单元测试可独立验证故障发生在读取还是写入，接口本身不产生副作用。
type catalogRecordStore interface {
	ListKV(kind string) ([]map[string]any, error)
	GetKV(kind, id string) (map[string]any, error)
	PutKV(kind, id, body string) error
	DeleteKV(kind, id string) error
}

// writeCatalogStore 处理已鉴权、已解析的通用目录读写；st 为存储，w/r 为 HTTP 输出及路径，body 为请求对象。
// 已迁移的配置委派独立服务；剩余目录保留CRUD，仅真实持久化成功才返回成功。
// 插件列表精确筛选；RPC信封拒绝，Skill只删除路径ID；其他批量删除失败必须显式返回。
func writeCatalogStore(st catalogRecordStore, w http.ResponseWriter, r *http.Request, body map[string]any) {
	path := r.URL.Path
	// 此入口是已鉴权的REST资源接口，没有RPC执行器。拒绝信封，而非吞掉操作并伪造空工具列表。
	// 仅检查顶层标记，资源内部的嵌套元数据仍按正常目录字段持久化。
	if _, ok := body["jsonrpc"]; ok {
		util.WriteError(w, http.StatusBadRequest, "unsupported_protocol", "JSON-RPC is not supported by this REST management endpoint")
		return
	}
	if writeDiscovery(w, r) {
		return
	}
	// 未接执行器的运维操作不能被当成同名配置的CRUD，避免返回假执行成功。
	if rejectRuntimeOperation(w, r) {
		return
	}
	// 旧身份别名没有签发或邀请执行器，不能落入公共KV后伪装成业务成功。
	if rejectIdentityAlias(w, r) {
		return
	}
	if rejectLegacyInference(w, r) {
		return
	}
	if rejectProviderBatch(w, r) {
		return
	}
	if rejectMedicalAlias(w, r) {
		return
	}
	if rejectServiceAux(w, r) {
		return
	}
	kind, action, id := parseMgmt(r.Method, path, body)
	// Skill详情路径已明确指定唯一资源；不能再被通用批量操作的正文ID覆盖或扩大删除范围。
	_, _, skillResource := skillResourceAction(r.Method, path)
	if st == nil {
		util.WriteError(w, http.StatusServiceUnavailable, "unavailable", "catalog store unavailable")
		return
	}
	if writePluginList(st, w, r) {
		return
	}
	if writePluginRegistration(st, w, r, body) {
		return
	}
	if !skillResource && (id == "" || ((action == "update" || action == "delete") && (id == kind || id == singular(kind)))) {
		if alt := str(body[idField(kind)]); alt != "" {
			id = alt
		} else if alt := str(body["id"]); alt != "" && id == "" {
			id = alt
		}
	}
	if kind == "credentials" || kind == "credential" {
		writeSupplierStore(st, w, r, kind, action, id, body)
		return
	}
	if kind == "search_tools" || kind == "search_tool" || kind == "search-tools" {
		writeSearchConfigStore(st, w, r, kind, action, id, body)
		return
	}
	if kind == "prompts" {
		writePromptConfigStore(st, w, r, kind, action, id, body)
		return
	}
	if kind == "policies" || kind == "policy" {
		writePolicyConfigStore(st, w, r, kind, action, id, body)
		return
	}
	if kind == "access_group" || kind == "access_groups" {
		writeAccessGroupConfigStore(st, w, r, kind, action, id, body)
		return
	}
	if kind == "vector_store" {
		writeVectorConfigStore(st, w, r, kind, action, id, body)
		return
	}
	// 只迁移明确的CRUD入口；block/unblock和统计等执行型旧路径不因此获得新的业务语义。
	if metadataconfig.Supports(kind) && metadataCRUDPath(path) {
		writeMetadataConfigStore(st, w, r, kind, action, id, body)
		return
	}
	if kind == "budget" || kind == "budgets" {
		writeBudgetConfigStore(st, w, r, kind, action, id, body)
		return
	}
	switch action {
	case "list":
		list, err := st.ListKV(kind)
		if err != nil {
			writeCatalogStoreFailure(w, kind, action, err)
			return
		}
		if list == nil {
			list = []map[string]any{}
		}
		for i := range list {
			Freeze(kind, list[i])
		}
		util.WriteJSON(w, 200, catalogListBody(kind, path, redactCatalogList(kind, list, true)))
	case "info":
		if id == "" {
			util.WriteJSON(w, 200, map[string]any{"object": "list", "data": []any{}})
			return
		}
		m, err := st.GetKV(kind, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				util.WriteError(w, http.StatusNotFound, "not_found", "catalog resource not found")
			} else {
				writeCatalogStoreFailure(w, kind, action, err)
			}
			return
		}
		Freeze(kind, m)
		util.WriteJSON(w, 200, redactCatalogObject(kind, m, true))
	case "delete":
		ids := []string{id}
		if !skillResource {
			// 其他旧入口仍保留原有批量删除语义，收窄只针对Skill详情路由。
			ids = idsFrom(body, kind+"s", idField(kind))
			if id != "" {
				ids = append(ids, id)
			}
		}
		n := 0
		for _, x := range ids {
			err := st.DeleteKV(kind, x)
			if err == nil {
				n++
			} else if !errors.Is(err, sql.ErrNoRows) {
				// 现有删除接口允许缺失项；数据库失败不能伪装成缺失项，也不能宣称整批成功。
				writeCatalogStoreFailure(w, kind, action, err)
				return
			}
		}
		util.WriteJSON(w, 200, map[string]any{"deleted": n, idField(kind): id, "id": id})
	case "update":
		if id == "" {
			id = singular(kind) + "_" + util.CallID()[:12]
		}
		m, err := st.GetKV(kind, id)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				writeCatalogStoreFailure(w, kind, action, err)
				return
			}
			// 只在确认不存在时沿用更新即创建的契约，故障读取不得覆盖已有记录。
			m = map[string]any{"id": id, idField(kind): id, "created_at": time.Now().UTC().Format(time.RFC3339)}
		}
		for k, v := range body {
			if k != "password" {
				m[k] = v
			}
		}
		m["id"] = id
		m[idField(kind)] = id
		Freeze(kind, m)
		if !persistCatalogObject(st, w, kind, id, m) {
			return
		}
		util.WriteJSON(w, 200, redactCatalogObject(kind, m, true))
	default:
		if id == "" && kind == "memory" {
			if k := str(body["key"]); k != "" {
				id = k
			}
		}
		if id == "" {
			id = singular(kind) + "_" + util.CallID()[:12]
		}
		obj := map[string]any{}
		for k, v := range body {
			if k == "password" {
				continue
			}
			obj[k] = v
		}
		for _, nest := range []string{"search_tool", "prompt", "guardrail", "policy", "skill", "vector_store"} {
			inner, ok := obj[nest].(map[string]any)
			if !ok {
				continue
			}
			for ik, iv := range inner {
				if obj[ik] == nil {
					obj[ik] = iv
				}
			}
		}
		obj["id"] = id
		obj[idField(kind)] = id
		if obj["created_at"] == nil {
			obj["created_at"] = time.Now().UTC().Format(time.RFC3339)
		}
		if alias := aliasField(kind); str(obj[alias]) == "" {
			if n := str(body["name"]); n != "" {
				obj[alias] = n
			}
		}
		Freeze(kind, obj)
		if !persistCatalogObject(st, w, kind, id, obj) {
			return
		}
		util.WriteJSON(w, 200, redactCatalogObject(kind, obj, false))
	}
}

// persistCatalogObject 编码并保存目录对象；st 为窄存储、w 为错误响应、kind/id 定位资源，obj 为待保存字段。
// 由创建及更新路径调用，成功返回 true；编码或数据库失败写脱敏 500 并返回 false，不发送任何成功对象。
func persistCatalogObject(st catalogRecordStore, w http.ResponseWriter, kind, id string, obj map[string]any) bool {
	raw, err := json.Marshal(obj)
	if err == nil {
		err = st.PutKV(kind, id, string(raw))
	}
	if err != nil {
		writeCatalogStoreFailure(w, kind, "save", err)
		return false
	}
	return true
}

// writeCatalogStoreFailure 统一目录内部失败响应；w 为输出、kind/action 用于定位日志、err 只用于辨别错误类型。
// 读写路径调用，无返回值；既不向客户端也不向日志输出 SQL、连接串或可能含凭据的底层错误正文。
func writeCatalogStoreFailure(w http.ResponseWriter, kind, action string, err error) {
	util.Error("catalog store failure kind=%s action=%s error_type=%T", kind, action, err)
	util.WriteError(w, http.StatusInternalServerError, "internal", "catalog store failure")
}
