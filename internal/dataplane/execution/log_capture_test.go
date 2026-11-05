package execution

import (
	"context"
	"testing"

	"github.com/sunqirui1987/xhub/internal/auth"
)

// captureProbe 记录执行器传给宿主的身份和调用标识；仅用于内存单元测试。
type captureProbe struct {
	ctx       context.Context
	call      string
	principal *auth.Principal
}

// BeginLogCapture 将执行器的参数记到测试对象；不读写外部资源。
func (p *captureProbe) BeginLogCapture(ctx context.Context, call string, principal *auth.Principal) {
	p.ctx, p.call, p.principal = ctx, call, principal
}

// TestBeginLogCapture 验证支持的宿主收到认证主体，不支持的宿主安全忽略；纯内存测试无需清理。
func TestBeginLogCapture(t *testing.T) {
	p := &auth.Principal{UserID: "user"}
	probe := &captureProbe{}
	beginLogCapture(t.Context(), probe, "call", p)
	if probe.ctx != t.Context() || probe.call != "call" || probe.principal != p {
		t.Fatal("执行器未传递已认证主体")
	}
	beginLogCapture(t.Context(), struct{}{}, "call", p)
}
