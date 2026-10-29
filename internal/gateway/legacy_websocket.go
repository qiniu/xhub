package gateway

import (
	"net/http"
	"strings"

	"github.com/sunqirui1987/xhub/internal/util"
)

// legacyWebsocketAlias 判断是否为C6审计的两条旧实时协议别名；path可以是目录模板或实际URL。
// 返回是否匹配，允许一个尾斜杠；不接管标准Responses/Realtime、OpenAI或Bypass，无副作用。
func legacyWebsocketAlias(path string) bool {
	path = strings.TrimSuffix(path, "/")
	if path == "/vertex_ai/live" {
		return true
	}
	parts := strings.Split(path, "/")
	return len(parts) == 3 && parts[0] == "" && parts[1] == "openai_passthrough" && parts[2] != ""
}

// rejectLegacyWebsocket 在旧入口执行原LLM鉴权，再明确拒绝没有转发执行器的协议。
// w/r为入站HTTP；返回是否处理。真实GET握手和目录伪方法共用此边界，不hijack、不连接上游、不改预算。
func (s *Server) rejectLegacyWebsocket(w http.ResponseWriter, r *http.Request) bool {
	if !legacyWebsocketAlias(r.URL.Path) {
		return false
	}
	if s.requireLLMPrincipal(w, r) != nil {
		w.Header().Set("Cache-Control", "no-store")
		util.WriteError(w, http.StatusNotImplemented, "unsupported_protocol_operation", "this legacy endpoint has no WebSocket forwarding implementation")
	}
	return true
}
