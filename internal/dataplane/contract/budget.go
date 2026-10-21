package contract

import (
	"net/http"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
)

// BudgetHost 是生产网关在每次实际 HTTP 尝试前实施软预算准入的扩展接口。
// 旧测试宿主可以只测试协议能力；生产 Server 必须实现此接口，两条推理执行器均调用。
type BudgetHost interface {
	// PrepareBudgetAttempt 检查各级剩余额度并冻结实际计价源，不修改协议正文或预占；失败已写错误，不得外发。
	// 参数为响应、原请求、主体、请求 ID、实际部署、协议、正文、开始时间；返回原正文及是否继续。
	PrepareBudgetAttempt(http.ResponseWriter, *http.Request, *auth.Principal, string, config.ModelEntry, string, []byte, time.Time) ([]byte, bool)
	// MarkBudgetAttemptSubmitted 在真正发送前检查请求取消状态；失败已写错误，不得外发。
	// 参数为响应、原请求和调用 ID；返回是否继续，ReleaseCall 负责释放临时快照和队列槽位。
	MarkBudgetAttemptSubmitted(http.ResponseWriter, *http.Request, string) bool
}
