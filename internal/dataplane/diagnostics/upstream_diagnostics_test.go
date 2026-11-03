// 本文件验证 diagnostics 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package diagnostics

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestTraceFirstByteAndEndpoint 前置本地 HTTP 服务延迟发送正文，验证首个响应字节被追踪且出站端点只保留路径。
// 参数 t 为测试上下文；服务由 httptest 自动关闭，不访问外部网络，不留下持久化数据。
func TestTraceFirstByteAndEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("{\\\"usage\\\":{\\\"prompt_tokens\\\":2,\\\"completion_tokens\\\":3}}"))
	}))
	defer server.Close()
	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/responses?api_key=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	req, first := TraceFirstByte(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Request = req
	diagnostic := ObserveUpstream(resp)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if first.IsZero() || diagnostic.FirstByteAt.IsZero() || diagnostic.Endpoint != "/v1/responses" {
		t.Fatalf("首字节或端点记录错误: first=%v diagnostic=%v endpoint=%q", first, diagnostic.FirstByteAt, diagnostic.Endpoint)
	}
}

// diagnosticBrokenBody 让 Read 和 Close 返回同一预设错误，验证诊断观察器不会吞掉原始传输或关闭失败；不持有外部资源。
type diagnosticBrokenBody struct {
	// 读取和关闭都会返回的预设错误，无真实资源。
	cause error
}

// Read 接收调用方缓冲区并返回模拟断流错误；供观察器测试调用，不修改缓冲区或外部状态。
func (b diagnosticBrokenBody) Read(p []byte) (int, error) { return 0, b.cause }

// Close 无参数并返回模拟关闭错误；由观察器测试调用，无外部资源需要释放。
func (b diagnosticBrokenBody) Close() error { return b.cause }

// TestUpstreamDiagnosticsInterrupted 前置未读完正文及失败读取器，验证诊断不伪造用量且原错误保持。
// 参数 t 为上下文，无返回值；全部夹具在内存中，关闭正文完成清理。
func TestUpstreamDiagnosticsInterrupted(t *testing.T) {
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":10}}`))}
	diagnostic := ObserveUpstream(resp)
	if err := resp.Body.Close(); err != nil || diagnostic.ParseError == "" || diagnostic.UsageReported {
		t.Fatalf("提前关闭缺少不完整标记: %+v %v", diagnostic, err)
	}
	cause := errors.New("test disconnect")
	resp.Body = diagnosticBrokenBody{cause: cause}
	diagnostic = ObserveUpstream(resp)
	_, err := io.ReadAll(resp.Body)
	if err != cause || resp.Body.Close() != cause || diagnostic.ParseError != "upstream body read failed" || diagnostic.ReadError != cause.Error() || diagnostic.UsageReported {
		t.Fatalf("断流错误被改变或统计伪造: %+v %v", diagnostic, err)
	}
}

// TestUpstreamDiagnosticsJSON 前置内存 HTTP 响应，验证原字节、多值头、数值精度和缺失/零统计。
// 参数 t 为测试上下文；无返回，内存夹具无需外部清理。
func TestUpstreamDiagnosticsJSON(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reported   bool
	}{
		{"missing", "{}", false},
		{"empty", "{\"usage\":{}}", true},
		{"zero", "{\"usage\":{\"prompt_tokens_details\":{\"cached_tokens\":0},\"vendor\":9007199254740993}}", true},
		{"gemini", "{\"usageMetadata\":{\"cachedContentTokenCount\":12}}", true},
		{"invalid", "upstream unavailable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Trace": {"first", "second"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Trailer: http.Header{"X-Final": {"yes"}}}
			diagnostic := ObserveUpstream(resp)
			raw, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(raw) != tc.body {
				t.Fatalf("观察器改变响应: %q %v", raw, err)
			}
			if diagnostic.UsageReported != tc.reported || len(diagnostic.Headers["X-Trace"]) != 2 || diagnostic.Trailers.Get("X-Final") != "yes" {
				t.Fatalf("诊断事实错误: %+v", diagnostic)
			}
			if tc.name == "empty" && diagnostic.Usage == nil {
				t.Fatal("明确空 usage 必须保留空对象")
			}
			if tc.name == "zero" && diagnostic.Usage["vendor"] != json.Number("9007199254740993") {
				t.Fatal("上游整数精度丢失")
			}
			if tc.name == "invalid" && diagnostic.ParseError == "" {
				t.Fatal("无效统计缺少解析诊断")
			}
		})
	}
}

// TestUpstreamDiagnosticsSSE 前置分块 SSE 与多行 data，验证转换前缓存和扩展统计合并。
// 参数 t 为上下文；覆盖超过日志正文上限的流末尾统计及超限恢复，无外部数据。
// 测试只操作内存配置或本地假上游，HTTP 服务及正文由本例关闭，不创建外部付费任务或持久化数据。
func TestUpstreamDiagnosticsSSE(t *testing.T) {
	for _, prefix := range []string{"", "data: {\"delta\":\"" + strings.Repeat("a", 3<<20) + "\"}\n\n", "data: " + strings.Repeat("a", (8<<20)+1) + "\n\n"} {
		body := prefix + "data: {\"message\": {\"usage\": {\"input_tokens\":20,\"details\":{}}}}\r\n\r\n" +
			"data: {\"response\":\n" + "data: {\"usage\":{\"input_tokens_details\":{\"cached_tokens\":12},\"vendor\":{\"tier\":\"cached\"}}}}\n\n" + "data: [DONE]\n\n"
		resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
		diagnostic := ObserveUpstream(resp)
		var out strings.Builder
		buf := make([]byte, 127)
		_, err := io.CopyBuffer(&out, resp.Body, buf)
		resp.Body.Close()
		if err != nil || out.String() != body {
			t.Fatal("SSE 字节改变")
		}
		if !diagnostic.UsageReported || diagnostic.Usage["input_tokens"] != json.Number("20") || diagnostic.Usage["input_tokens_details"].(map[string]any)["cached_tokens"] != json.Number("12") {
			t.Fatalf("流末尾原始用量丢失: %+v", diagnostic)
		}
		if diagnostic.Usage["details"] == nil {
			t.Fatal("空嵌套对象丢失")
		}
	}
}
