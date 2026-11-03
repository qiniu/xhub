package gateway

import (
	"net/http/httptest"
	"testing"
)

// TestLegacyWebsocketAlias 验证模板、实际路径及尾斜杠匹配，不接管相近路径或标准协议。
// 前置内存路由器，实际GET和历史伪方法均由原handler处理并保留参数；无需外部资源清理。
func TestLegacyWebsocketAlias(t *testing.T) {
	for _, path := range []string{"/vertex_ai/live", "/openai_passthrough/responses", "/openai_passthrough/{endpoint}"} {
		for _, suffix := range []string{"", "/"} {
			if !legacyWebsocketAlias(path + suffix) {
				t.Fatal("未识别旧别名", path+suffix)
			}
		}
	}
	for _, path := range []string{"", "/vertex_ai/live/extra", "/vertex_ai/lives", "/openai_passthrough/", "/openai_passthrough/responses/extra", "/v1/responses", "/openai/responses", "/bypass/openai/responses", "/vertex_ai/live//"} {
		if legacyWebsocketAlias(path) {
			t.Fatal("错误接管协议", path)
		}
		if (&Server{}).rejectLegacyWebsocket(httptest.NewRecorder(), httptest.NewRequest("GET", "http://test"+path, nil)) {
			t.Fatal("近似路径不应鉴权或写响应", path)
		}
	}
	s := newRouteAuditServer()
	s.handle("WEBSOCKET /openai_passthrough/{endpoint}", s.passthroughContract)
	for _, method := range []string{"GET", "WEBSOCKET"} {
		if _, ok := s.registered[method+" /openai_passthrough/:p2"]; !ok {
			t.Fatal("未登记真实握手或历史方法", method)
		}
	}
}
