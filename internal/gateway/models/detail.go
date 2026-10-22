package models

import (
	"github.com/sunqirui1987/xhub/internal/util"
	"net/http"
	"time"
)

// Detail 为原生SDK返回可见公开模型，参数为宿主、响应和带id路径的请求，返回无。
// 调用GET /v1/models/{id}；沿用列表权限，未知或不可见模型统一404，避免泄露其他租户目录。
func Detail(s Host, w http.ResponseWriter, r *http.Request) {
	util.SetCallID(w, util.CallID())
	p, err := s.Resolve(r)
	if err != nil || (!s.AllowLLM(p) && !p.PlatformAdmin()) {
		util.WriteTypedError(w, r.URL.Path, 401, "invalid_api_key", "Authentication required")
		return
	}
	id := r.PathValue("id")
	for _, name := range availableNames(s, r, p) {
		if name == id {
			util.WriteJSON(w, 200, modelDetail(name, r.Header.Get("anthropic-version") != ""))
			return
		}
	}
	util.WriteTypedError(w, r.URL.Path, 404, "not_found", "Model not found")
}

// modelDetail 按客户端协议生成最小公开模型字段；参数为公开名及Anthropic标识，返回响应对象。
// 列表权限通过后调用；固定兼容时间不代表供应商发布日期，不访问上游或修改数据。
func modelDetail(name string, anthropic bool) map[string]any {
	if anthropic {
		return map[string]any{"id": name, "type": "model", "display_name": name, "created_at": time.Unix(defaultModelCreatedAt, 0).UTC().Format(time.RFC3339)}
	}
	return map[string]any{"id": name, "object": "model", "created": defaultModelCreatedAt, "owned_by": "openai"}
}
