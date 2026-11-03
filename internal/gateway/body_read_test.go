package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// failingRequestBody 模拟入站网络读取错误，不携带用户正文；供 handler 分类测试使用。
type failingRequestBody struct{ err error }

// Read 返回预设错误和零字节，验证异常路由；不修改缓冲且无外部副作用。
func (b failingRequestBody) Read([]byte) (int, error) { return 0, b.err }

// Close 释放测试读取器；无资源，返回 nil。
func (b failingRequestBody) Close() error { return nil }

// TestBodyReadFailureStatus 从真实 handler 入口验证超限、超时、取消和截断不能混为413；无数据库或外部数据。
func TestBodyReadFailureStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"too_large", &http.MaxBytesError{Limit: 64 << 20}, 413},
		{"timeout", context.DeadlineExceeded, 408},
		{"canceled", context.Canceled, 499},
		{"truncated", io.ErrUnexpectedEOF, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{engine: newEngine(), registered: map[string]struct{}{}, idem: map[string]*idemRec{}}
			r := httptest.NewRequest("POST", "/health/liveliness", nil)
			r.Body = failingRequestBody{tc.err}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("读取错误分类错误: got=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}

// TestBodyReadFailureLogOmitsPartialBody 验证读取失败时只保存诊断，完整但未读完的 JSON 也不能当作有效请求。
// 前置隔离 PostgreSQL；覆盖正文保存开关、零字节、超限、超时、取消和截断，检查原错误响应、身份无猜测及日志体积。
// 参数 t 为测试上下文；返回无；testGatewayStores 自动关闭连接并清理私有 schema。
func TestBodyReadFailureLogOmitsPartialBody(t *testing.T) {
	cfg, st, db := testGatewayStores(t)
	s := New(cfg, st, db)
	for _, save := range []bool{true, false} {
		cfg.GeneralSettings.StorePromptsInSpendLogs = save
		for _, tc := range []struct {
			name   string
			body   string
			err    error
			status int
		}{
			{"empty", "", io.ErrUnexpectedEOF, 400},
			{"truncated", `{"model":"partial-secret-model","reasoning_effort":"high","messages":[{"role":"user","content":"partial-secret-content"}]}`, io.ErrUnexpectedEOF, 400},
			{"too_large", strings.Repeat("partial-secret-content", 4096), &http.MaxBytesError{Limit: 64 << 20}, 413},
			{"timeout", "partial-secret-content", context.DeadlineExceeded, 408},
			{"cancel", "partial-secret-content", context.Canceled, 499},
		} {
			t.Run(fmt.Sprintf("prompts=%t/%s", save, tc.name), func(t *testing.T) {
				r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
				r.Body = io.NopCloser(io.MultiReader(strings.NewReader(tc.body), failingRequestBody{tc.err}))
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatalf("正文失败状态改变: got=%d want=%d", w.Code, tc.status)
				}
				var stored iam.RequestLog
				found, err := db.Engine.Where("request_id = ?", w.Header().Get("x-litellm-call-id")).Get(&stored)
				if err != nil || !found {
					t.Fatalf("正文失败未保存诊断: found=%t err=%v", found, err)
				}
				snapshot, err := json.Marshal(stored)
				if err != nil || len(snapshot) > 16<<10 || strings.Contains(string(snapshot), "partial-secret") {
					t.Fatalf("正文失败日志保存了未完成的请求或体积无界: bytes=%d err=%v", len(snapshot), err)
				}
				var diagnostic struct {
					Failure struct {
						Omitted bool  `json:"request_body_omitted"`
						Bytes   int64 `json:"bytes_read"`
					} `json:"failure"`
				}
				if err := json.Unmarshal([]byte(stored.UpstreamResponse), &diagnostic); err != nil || !diagnostic.Failure.Omitted || diagnostic.Failure.Bytes != int64(len(tc.body)) {
					t.Fatalf("省略正文后诊断丢失: diagnostic=%+v err=%v", diagnostic, err)
				}
				if stored.ResponseBody != w.Body.String() || stored.Error == "" {
					t.Fatal("省略入站正文误删了网关错误响应")
				}
			})
		}
	}
}
