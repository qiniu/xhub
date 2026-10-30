package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestLocalStreamFailureClassification 验证本地取消、超时、断流不伪装成供应商明确错误。
// 参数 t 为测试上下文；输入含包装错误及明确供应商错误，验证稳定类型和原文保留；纯内存，无清理。
func TestLocalStreamFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind string
	}{
		{"cancel", context.Canceled, "request_canceled"},
		{"wrapped_cancel", fmt.Errorf("read: %w", context.Canceled), "request_canceled"},
		{"deadline", context.DeadlineExceeded, "request_timeout"},
		{"network_timeout", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}, "request_timeout"},
		{"truncated", io.ErrUnexpectedEOF, "stream_transport_error"},
		{"decode", errors.New("invalid event"), "stream_transport_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ErrorEnvelope(tc.err)
			fields := got["error"].(map[string]any)
			if fields["type"] != tc.kind || fields["code"] != tc.kind {
				t.Fatalf("本地失败被误分类: %v", got)
			}
			if got["error_origin"] != "gateway" || fields["message"] == "" {
				t.Fatalf("本地错误来源或详情缺失: %v", got)
			}
			if tc.kind == "request_canceled" && got["cancellation_source"] != "unknown" {
				t.Fatalf("不能把取消推断为用户操作: %v", got)
			}
		})
	}
	vendor := &UpstreamError{Envelope: map[string]any{"error": map[string]any{"code": "upstream_error", "message": "context canceled"}, "request_id": "vendor-1"}}
	if got := ErrorEnvelope(fmt.Errorf("wrapped: %w", vendor)); got["error_origin"] != nil || got["request_id"] != "vendor-1" {
		t.Fatalf("供应商原始错误不能被重新归因: %v", got)
	}
}

// TestUpstreamErrorExtraction 用内存事件验证正常、嵌套、字符串、缺失和非法输入边界。
// 参数 t 为测试上下文；保留供应商错误及关联 ID，未命中不报错；无外部资源需清理。
func TestUpstreamErrorExtraction(t *testing.T) {
	for _, tc := range []struct {
		name, raw, marker string
		failed            bool
	}{
		{"normal", `{"type":"response.completed","response":{"error":null}}`, "", false},
		{"empty", `{}`, "", false},
		{"chat", `{"error":{"code":"capacity","message":"Vendor busy","details":{"retryable":true}},"request_id":"req-vendor"}`, "req-vendor", true},
		{"responses", `{"type":"response.failed","response":{"id":"resp-vendor","error":{"code":"busy","message":"Vendor busy"}}}`, "resp-vendor", true},
		{"flat", `{"type":"error","code":"overloaded","message":"Vendor busy"}`, "Vendor busy", true},
		{"string", `{"error":"Vendor busy"}`, "Vendor busy", true},
		{"missing", `{"type":"response.failed"}`, "without error details", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal([]byte(tc.raw), &doc); err != nil {
				t.Fatal(err)
			}
			got := ExtractUpstreamError(doc)
			if (got != nil) != tc.failed {
				t.Fatalf("失败分类错误: %v", got)
			}
			if got != nil && (!strings.Contains(got.Error(), tc.marker) || ErrorEnvelope(got)["error"] == nil) {
				t.Fatalf("错误内容丢失: %v", got)
			}
			if string(mustErrorJSON(doc)) != string(mustErrorJSON(jsonDoc(t, tc.raw))) {
				t.Fatal("修改了原事件")
			}
		})
	}
	if ErrorEnvelope(nil) != nil {
		t.Fatal("nil 不应生成错误")
	}
	if !strings.Contains(string(mustErrorJSON(ErrorEnvelope(errors.New("network failed")))), "network failed") {
		t.Fatal("网络错误丢失")
	}
	if got := (&UpstreamError{Envelope: map[string]any{"bad": func() {}}}).Error(); got != "invalid upstream error payload" {
		t.Fatal(got)
	}
}

// mustErrorJSON 序列化测试 JSON 值作结构比较；仅接收可编码夹具，返回字节，无外部副作用。
func mustErrorJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// jsonDoc 解码测试事件，失败终止当前用例；参数为上下文和JSON，返回独立对象，无清理。
func jsonDoc(t *testing.T, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestUpstreamErrorObservation 验证 HTTP 200 流的 error 与真实 HTTP 状态独立记录，正常/损坏正文不伪造上游错误。
// 前置内存 HTTP 响应，参数 t 为上下文；读取字节不变并关闭所有正文，无外部状态。
func TestUpstreamErrorObservation(t *testing.T) {
	for _, raw := range []string{`{"error":{"message":"Vendor busy"},"request_id":"req-vendor"}`, `{"usage":{}}`, `invalid`} {
		for _, stream := range []bool{false, true} {
			input := raw
			header := http.Header{}
			if stream {
				input = "data: " + raw + "\n\n"
				header.Set("Content-Type", "text/event-stream")
			}
			resp := &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(input))}
			diag := ObserveUpstream(resp)
			got, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(got) != input || diag.StatusCode != 200 {
				t.Fatalf("观察器改变传输: %v", err)
			}
			if (diag.Error != nil) != strings.Contains(raw, "Vendor busy") {
				t.Fatalf("错误观测不符: %+v", diag)
			}
		}
	}
}
