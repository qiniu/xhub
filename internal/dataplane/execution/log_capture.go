package execution

import (
	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/dataplane/contract"
)

// beginLogCapture 将已认证的请求标识和主体交给支持正文记录的网关；不支持的宿主不产生副作用。
// 标准和原生入口在读取正文前调用，记录行为不参与鉴权、路由或预算。
func beginLogCapture(host any, callID string, p *auth.Principal) {
	if capture, ok := host.(contract.CaptureBeginner); ok {
		capture.BeginLogCapture(callID, p)
	}
}
