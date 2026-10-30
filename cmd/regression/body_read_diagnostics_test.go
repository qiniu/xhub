package regression

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestBodyReadDiagnostics 通过真实 TCP 不完整上传、读取超时和超限触发入口错误，验证日志身份、事实与隔离。
// 前置私有数据库，额外HTTP服务器仅在测试设置短读取截止；所有连接、服务和schema由Cleanup释放，不调用外部供应商。
func TestBodyReadDiagnostics(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession()
	owner := h.openScope(t, admin, "body-owner")
	other := h.openScope(t, admin, "body-other")
	for _, tc := range []struct {
		name    string
		timeout bool
		size    int
		status  int
		kind    string
	}{
		{"truncated", false, 100, 400, "unexpected_eof"},
		{"timeout", true, 100, 408, "timeout"},
		{"too_large", false, (64 << 20) + 1, 413, "body_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(h.gw.Handler())
			if tc.timeout {
				server.Config.ReadTimeout = 100 * time.Millisecond
			}
			server.Start()
			defer server.Close()
			address, _ := url.Parse(server.URL)
			conn, err := net.DialTimeout("tcp", address.Host, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", address.Host, owner.key, tc.size)
			if tc.name == "too_large" {
				_, err = io.CopyN(conn, strings.NewReader(strings.Repeat("x", tc.size)), int64(tc.size))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				fmt.Fprint(conn, "{")
			}
			if tc.name == "truncated" {
				if err = conn.(*net.TCPConn).CloseWrite(); err != nil {
					t.Fatal(err)
				}
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, _ := io.ReadAll(response.Body)
			if response.StatusCode != tc.status {
				t.Fatalf("%s分类错误: status=%d body=%s", tc.name, response.StatusCode, raw)
			}
			id := response.Header.Get("X-Litellm-Call-Id")
			detail := logDetail(t, h, owner.session, id)
			d := detail["metadata"].(map[string]any)["upstream_response"].(map[string]any)
			f := d["failure"].(map[string]any)
			if f["phase"] != "request_body" || f["kind"] != tc.kind || f["bytes_read"].(float64) <= 0 || f["transport_error"] == "" || d["status_code"] != float64(0) {
				t.Fatalf("正文读取证据不完整: %v", d)
			}
			if h.do("GET", "/spend/logs/ui/"+id, other.session, nil).status != 404 {
				t.Fatal("失败日志身份隔离失效")
			}
			if len(h.upstreamCalls()) != 0 {
				t.Fatal("正文读取失败仍发起上游请求")
			}
		})
	}
}
