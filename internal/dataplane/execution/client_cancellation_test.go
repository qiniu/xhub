package execution

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	providerregistry "github.com/sunqirui1987/xhub/internal/provider/registry"
	"github.com/sunqirui1987/xhub/internal/router"
)

// cancellationHost 记录取消分支的供应商失败和唯一记账；复用纯内存宿主，无共享状态。
type cancellationHost struct {
	*streamAuditHost
	failures, bills int
}

// NoteFailure 统计执行器报告的部署故障；参数为路由身份和策略，无返回或外部副作用。
func (h *cancellationHost) NoteFailure(string, router.RouteSettings) { h.failures++ }

// RecordSpend 捕获一次最终状态；参数沿用记账契约，供取消与真实超时对照，不写数据库。
func (h *cancellationHost) RecordSpend(_ http.ResponseWriter, _ *auth.Principal, _, _, _ string, _ map[string]any, _ time.Time, _ bool, status int, _ string) {
	h.status = status
	h.bills++
}

// cancellationBody 在读取已有正文后产生指定错误；可先取消入站请求，以区分调用方和上游失败。
type cancellationBody struct {
	data   string
	cancel context.CancelFunc
	err    error
}

// Read 返回剩余正文，随后执行可选取消并返回故障；参数接收字节，返回读取长度/错误，无网络副作用。
func (b *cancellationBody) Read(p []byte) (int, error) {
	if b.data != "" {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	if b.cancel != nil {
		b.cancel()
	}
	return 0, b.err
}

// TestClientCancellationClassification 验证出站前、流读取和普通正文读取的取消只记499、不重试或冷却。
// 参数t为测试上下文；同位置真实上游超时必须仍记502和供应商失败；内存传输隔离，context由Cleanup释放。
func TestClientCancellationClassification(t *testing.T) {
	for _, phase := range []string{"dial", "stream", "body"} {
		for _, caller := range []bool{true, false} {
			name := phase + map[bool]string{true: "_caller", false: "_upstream"}[caller]
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)
				attempts := 0
				stream := phase == "stream"
				client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
					attempts++
					failure := error(context.DeadlineExceeded)
					var stop context.CancelFunc
					if caller {
						failure = context.Canceled
						stop = cancel
					}
					if phase == "dial" {
						if stop != nil {
							stop()
						}
						return nil, failure
					}
					data := `{"choices":[`
					headers := http.Header{"Content-Type": {"application/json"}}
					if stream {
						data = "data: {\"id\":\"chatcmpl_cancel\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"
						headers.Set("Content-Type", "text/event-stream")
					}
					return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(&cancellationBody{data: data, cancel: stop, err: failure})}, nil
				})}
				cfg := chatConfig(config.ModelEntry{ModelName: "cancel", LiteLLMParams: map[string]any{"model": "cancel", "api_base": "https://example.invalid/v1", "api_key": "test"}})
				settings := router.BuiltinSettings()
				settings.Settings["retry_policy"] = map[string]any{"max_attempts": 3, "timeout_seconds": 60, "failure_threshold": 3}
				h := &cancellationHost{streamAuditHost: &streamAuditHost{logHost: newLogHost(cfg, client), settings: settings}}
				Serve(h, httptest.NewRecorder(), chatRequest(t, "cancel", stream).WithContext(ctx), "chat")
				wantAttempts, wantStatus, wantFailures := 3, 502, 3
				if stream {
					wantAttempts, wantFailures = 1, 1
				}
				if caller {
					wantAttempts, wantStatus, wantFailures = 1, 499, 0
				}
				if attempts != wantAttempts || h.status != wantStatus || h.failures != wantFailures || h.bills != 1 || h.commits != 0 {
					t.Fatalf("取消/故障分类错误 attempts=%d status=%d failures=%d bills=%d commits=%d", attempts, h.status, h.failures, h.bills, h.commits)
				}
			})
		}
	}
}

// canceledNativeHost 在原生测试宿主上捕获记账状态，不改变原有鉴权或路由测试契约。
type canceledNativeHost struct {
	*fallbackNativeHost
	status, bills int
}

// RecordSpend 记录原生入口最终HTTP分类和次数；其余参数仅满足宿主契约，无外部结算。
func (h *canceledNativeHost) RecordSpend(_ http.ResponseWriter, _ *auth.Principal, _, _, _ string, _ map[string]any, _ time.Time, _ bool, status int, _ string) {
	h.status = status
	h.bills++
}

// TestNativeClientCancellationClassification 验证原生Chat入口正常取消不重试、不污染供应商健康，真实上游超时仍失败。
// 参数t为测试上下文；使用内存传输且无供应商调用，cleanup释放独立上下文。
func TestNativeClientCancellationClassification(t *testing.T) {
	for _, caller := range []bool{true, false} {
		t.Run(map[bool]string{true: "caller", false: "upstream"}[caller], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			attempts := 0
			client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
				attempts++
				if caller {
					cancel()
					return nil, context.Canceled
				}
				return nil, context.DeadlineExceeded
			})}
			models := []config.ModelEntry{Deployment("cancel", "cancel", "test", "https://example.invalid/v1", "bypass_openai_chat", nil)}
			settings := router.BuiltinSettings()
			settings.Settings["retry_policy"] = map[string]any{"max_attempts": 3, "timeout_seconds": 60, "failure_threshold": 3}
			h := &canceledNativeHost{fallbackNativeHost: &fallbackNativeHost{logicHost: &logicHost{cfg: &config.Config{ModelList: models}, client: client, models: models, pins: map[string]string{}}, policy: settings}}
			req := chatRequest(t, "cancel", false).WithContext(ctx)
			req.URL.Path = "/bypass/openai/v1/chat/completions"
			hit, ok := providerregistry.Match("POST", req.URL.Path)
			if !ok {
				t.Fatal("missing native route")
			}
			ServeBypass(h, httptest.NewRecorder(), req, hit)
			count, status, failures := 3, 502, 3
			if caller {
				count, status, failures = 1, 499, 0
			}
			if attempts != count || h.status != status || h.bills != 1 || len(h.failures) != failures {
				t.Fatalf("native classification attempts=%d status=%d bills=%d failures=%d", attempts, h.status, h.bills, len(h.failures))
			}
		})
	}
}

// TestUpstream4xxPrereadClientCancellation 验证读取 4xx 正文时连接取消应记为未知来源中断而不是上游 400。
// 使用内存传输和请求上下文，覆盖流式/非流式及空/部分正文；断言零重试、零冷却、单次失败记录，无外部数据需清理。
func TestUpstream4xxPrereadClientCancellation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/partial=%t", stream, partial), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				attempts := 0
				client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
					attempts++
					data := ""
					if partial {
						data = `{"error":`
					}
					return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(&cancellationBody{data: data, cancel: cancel, err: context.Canceled})}, nil
				})}
				cfg := chatConfig(config.ModelEntry{ModelName: "cancel-4xx", LiteLLMParams: map[string]any{"model": "cancel-4xx", "api_base": "https://example.invalid/v1", "api_key": "test"}})
				settings := router.BuiltinSettings()
				settings.Settings["retry_policy"] = map[string]any{"max_attempts": 3, "timeout_seconds": 60, "failure_threshold": 3}
				h := &cancellationHost{streamAuditHost: &streamAuditHost{logHost: newLogHost(cfg, client), settings: settings}}
				Serve(h, httptest.NewRecorder(), chatRequest(t, "cancel-4xx", stream).WithContext(ctx), "chat")
				if attempts != 1 || h.status != 499 || h.failures != 0 || h.bills != 1 {
					t.Fatalf("4xx 预读取消分类错误: attempts=%d status=%d failures=%d bills=%d", attempts, h.status, h.failures, h.bills)
				}
			})
		}
	}
}

// TestUpstream4xxEmptyTruncationNeverRetries 验证 4xx 头已收到但正文截断时不应把空流当成可重试的空成功。
// 使用内存传输模拟意外 EOF 和可重试配置，覆盖流式与非流式；断言一次上游调用和零冷却，无外部资源需清理。
func TestUpstream4xxEmptyTruncationNeverRetries(t *testing.T) {
	for _, stream := range []bool{true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			attempts := 0
			client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(&cancellationBody{err: io.ErrUnexpectedEOF})}, nil
			})}
			cfg := chatConfig(config.ModelEntry{ModelName: "truncated-4xx", LiteLLMParams: map[string]any{"model": "truncated-4xx", "api_base": "https://example.invalid/v1", "api_key": "test"}})
			settings := router.BuiltinSettings()
			settings.Settings["retry_policy"] = map[string]any{"max_attempts": 3, "timeout_seconds": 60, "failure_threshold": 3}
			h := &cancellationHost{streamAuditHost: &streamAuditHost{logHost: newLogHost(cfg, client), settings: settings}}
			Serve(h, httptest.NewRecorder(), chatRequest(t, "truncated-4xx", stream), "chat")
			if attempts != 1 || h.status != 400 || h.failures != 0 || h.bills != 1 {
				t.Fatalf("截断 4xx 不应重试或换模型: attempts=%d status=%d failures=%d bills=%d", attempts, h.status, h.failures, h.bills)
			}
		})
	}
}
