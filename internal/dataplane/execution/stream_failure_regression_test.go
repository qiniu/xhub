// 本文件验证 execution 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package execution

import (
	"errors"
	"github.com/sunqirui1987/xhub/internal/router"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
)

// auditRoundTripper 通过回调模拟 HTTP 请求结果，用于执行失败回归；不进行真实拨号，回调负责构造响应与错误。
type auditRoundTripper func(*http.Request) (*http.Response, error)

// RoundTrip 将请求交给本测试提供的函数，适配 http.RoundTripper 接口。
// 参数 r 为执行器构造的出站请求；返回回调生成的响应和错误，原样保留失败语义。
// 供内存 HTTP 上游测试调用；是否读取正文、注入断流或计数由回调控制，本适配器不发起真实网络请求。
func (f auditRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// streamAuditHost 捕获最终记账状态及会话提交次数，验证流写出后失败不能重试或登记成功归属。
type streamAuditHost struct {
	// 复用统一日志宿主，覆写结算和归属以计数。
	*logHost
	// 最后提交的记账状态。
	status int
	// 成功归属提交次数，断流期望为零。
	commits int
	// 本例指定的重试策略或模板错误。
	settings router.RouteSettings
}

// RouteSettingsFor 是 streamAuditHost 的测试接口实现。
// 忽略身份并返回 h.settings；供流失败回归指定重试策略或模板错误，不查询数据库。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *streamAuditHost) RouteSettingsFor(*auth.Principal, []config.ModelEntry) router.RouteSettings {
	return h.settings
}

// RecordSpend 是 streamAuditHost 的测试接口实现。
// 只保存参数 status，忽略其他记账值；无返回，供失败状态断言，不扣费。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *streamAuditHost) RecordSpend(_ http.ResponseWriter, _ *auth.Principal, _, _, _ string, _ map[string]any, _ time.Time, _ bool, status int, _ string) {
	h.status = status
}

// CommitRoute 是 streamAuditHost 的测试接口实现。
// 忽略计划与 ID，将 commits 加一；无返回，只计数成功归属提交，不写会话缓存。
// 调用方为本文件或同包的执行测试；仅模拟所述边界，不能据此推断生产宿主的行为。
func (h *streamAuditHost) CommitRoute(dpcontract.RoutePlan, string, string) { h.commits++ }

// TestStreamFailureAfterOutputDoesNotRetryOrPin 验证已经发出文本后断流只调用上游一次，记账状态为 502 且不提交成功会话归属。
// 参数 t 为 Go 测试上下文；返回值无，断言失败定位具体协议、路由或日志行为。
// 前置使用独立测试配置及内存传输/纯函数输入，不依赖外部供应商凭据。
// 测试状态只存在本例内存，不产生持久化数据。
func TestStreamFailureAfterOutputDoesNotRetryOrPin(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(&FragmentedErrorReader{Parts: [][]byte{[]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")}, Err: errors.New("reset")})}, nil
	})}
	cfg := chatConfig(config.ModelEntry{ModelName: "audit", LiteLLMParams: map[string]any{"model": "openai/audit", "api_base": "https://example.invalid/v1", "api_key": "test-key"}})
	h := &streamAuditHost{logHost: newLogHost(cfg, client), settings: router.RouteSettings{Settings: map[string]any{
		"model_routes": []any{},
		"retry_policy": map[string]any{"max_attempts": 3, "timeout_seconds": 60, "failure_threshold": 3, "cooldown_seconds": 0},
	}}}
	rec := httptest.NewRecorder()
	Serve(h, rec, chatRequest(t, "audit", true), "chat")
	if attempts != 1 || h.status != 502 || h.commits != 0 || rec.Body.Len() == 0 {
		t.Fatalf("attempts=%d spendStatus=%d commits=%d body=%q", attempts, h.status, h.commits, rec.Body.String())
	}
}

// TestTemplateLookupFailureDoesNotContactUpstream 验证请求模板加载失败返回 503，并在构造或发送上游请求前终止。
// 参数 t 为 Go 测试上下文；返回值无，断言失败定位具体协议、路由或日志行为。
// 前置使用独立测试配置及内存传输/纯函数输入，不依赖外部供应商凭据。
// 测试状态只存在本例内存，不产生持久化数据。
func TestTemplateLookupFailureDoesNotContactUpstream(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) { attempts++; return nil, errors.New("unexpected request") })}
	h := &streamAuditHost{logHost: newLogHost(chatConfig(), client), settings: router.RouteSettings{Err: errors.New("database unavailable")}}
	rec := httptest.NewRecorder()
	Serve(h, rec, chatRequest(t, "audit", false), "chat")
	if rec.Code != 503 || attempts != 0 {
		t.Fatalf("status=%d attempts=%d body=%s", rec.Code, attempts, rec.Body.String())
	}
}
