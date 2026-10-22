package execution

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNativeTokenCount 验证计数动作原样转发状态与结果且不生成费用。
// 参数 t 为测试上下文；前置本地上游与独立内存宿主，覆盖成功、失败和缺少模型；defer 关闭服务，无持久数据。
func TestNativeTokenCount(t *testing.T) {
	for _, status := range []int{200, 402, 403, 429, 500} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/messages/count_tokens" || r.Header.Get("X-Api-Key") != "supplier" {
				t.Error("计数出站路径或凭据错误")
			}
			w.WriteHeader(status)
			io.WriteString(w, ` {"input_tokens":123,"usage":{"input_tokens":999}}`)
		}))
		t.Cleanup(up.Close)
		h := officialHost(up, Deployment("public", "anthropic/upstream", "supplier", up.URL, "bypass_anthropic_messages", nil))
		rec := h.call(t, "POST", "/bypass/anthropic/v1/messages/count_tokens", `{"model":"public","messages":[]}`)
		if rec.Code != status || len(h.spend) != 1 || h.spend[0].usage != nil {
			t.Fatalf("计数状态或零费用错误: %d %+v", rec.Code, h.spend)
		}
		if bad := h.call(t, "POST", "/bypass/anthropic/v1/messages/count_tokens", `{}`); bad.Code != 400 {
			t.Fatalf("缺少模型未拒绝: %d", bad.Code)
		}
		up.Close()
	}
}
