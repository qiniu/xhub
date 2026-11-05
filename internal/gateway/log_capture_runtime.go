package gateway

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/requestlog"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

// 编译时确保真实网关始终支持数据面的正文策略快照。
var _ contract.CaptureBeginner = (*Server)(nil)

// capturePolicy 是在请求鉴权后冻结的正文策略，不包含正文和凭据。
type capturePolicy struct{ userID, source, status string }

// captureSnapshotKey 是仅在网关内部使用的上下文键，客户端不能构造记录策略。
type captureSnapshotKey struct{}

// captureSnapshot 在单次 HTTP 处理栈中保留已鉴权策略，直到外层补写早期错误日志。
// 不保存正文；执行器的 ReleaseCall 仍立即清理共享 map，避免其他调用路径泄漏临时状态。
type captureSnapshot struct {
	callID string
	policy capturePolicy
}

// BeginLogCapture 在身份确认后冻结个人和全局设置；ctx 为请求上下文，callID 标识本次调用。
// 数据面在发往上游前调用；关闭只影响之后的新请求，查询错误按不记录处理并显式标记。
// 临时 map 由 recordSpend 或 ReleaseCall 回收；请求上下文副本供外层错误日志使用，不改变鉴权与账单。
func (s *Server) BeginLogCapture(ctx context.Context, callID string, p *auth.Principal) {
	if s == nil || callID == "" {
		return
	}
	policy := s.selectCapturePolicy(ctx, p)
	if snapshot, ok := ctx.Value(captureSnapshotKey{}).(*captureSnapshot); ok {
		snapshot.callID, snapshot.policy = callID, policy
	}
	s.mu.Lock()
	if s.capturePolicies == nil {
		s.capturePolicies = map[string]capturePolicy{}
	}
	s.capturePolicies[callID] = policy
	s.mu.Unlock()
}

// selectCapturePolicy 在 ctx 下最多花一秒读取管理员及个人设置；p 可以为空，不猜测共享密钥归属。
// 返回来源、用户及失败状态；读取失败不阻断模型请求，也不保存正文。
func (s *Server) selectCapturePolicy(ctx context.Context, p *auth.Principal) capturePolicy {
	policy := capturePolicy{userID: captureUserID(p)}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	global, err := s.promptsEnabled(ctx)
	if err != nil {
		util.Error("read global capture preference failed: %v", err)
		policy.status = "settings_unavailable"
		return policy
	}
	if global {
		policy.source = "global"
		return policy
	}
	if policy.userID == "" || s.IAM == nil {
		policy.status = "disabled"
		return policy
	}
	enabled, err := s.IAM.PersonalLogCaptureEnabled(ctx, policy.userID)
	if err != nil {
		util.Error("read personal capture preference failed: %v", err)
		policy.status = "settings_unavailable"
		return policy
	}
	if enabled {
		policy.source = "personal"
	} else {
		policy.status = "disabled"
	}
	return policy
}

// captureUserID 仅从已鉴权的会话或个人密钥确定用户；团队共享密钥及匿名请求返回空。
// p 为鉴权结果；供正文选择和容量归属使用，不从客户端正文、请求头或团队推断个人身份。
func captureUserID(p *auth.Principal) string {
	if p == nil || p.UserID == "" {
		return ""
	}
	if p.Kind == authz.KindSession || (p.Kind == authz.KindKey && p.OwnerType == iam.OwnerPersonal) {
		return p.UserID
	}
	return ""
}

// captureExchange 根据已认证身份和管理员全局设置保存一次交换的正文；start 为原始请求时间，streamed 和 failed 标识执行结果。
// 返回可展示状态和到期时间；超限或数据库故障不改变上游响应及账单，正文不进入持久账单队列。
// 控制平面/调用结束时调用；流式捕获到达现有限额时保守标记为不完整，不伪称完整。
func (s *Server) captureExchange(p *auth.Principal, requestID string, ex promptExchange, start time.Time, streamed, failed bool) (string, string) {
	s.mu.Lock()
	policy, frozen := s.capturePolicies[requestID]
	delete(s.capturePolicies, requestID)
	s.mu.Unlock()
	// 已鉴权的参数或路由错误由外层统一落日志，此时执行器的 defer 已经释放共享 map。
	// 只恢复同一 HTTP 请求、同一 callID 的快照，不重新查询个人开关，也不让匿名请求继承他人策略。
	if !frozen && ex.request != nil {
		if snapshot, ok := ex.request.Context().Value(captureSnapshotKey{}).(*captureSnapshot); ok && snapshot.callID == requestID {
			policy, frozen = snapshot.policy, true
		}
	}
	// 入站读取错误等没有冻结个人策略；不能以失败时的新个人设置回溯保存已到达的正文。
	// 管理员全局开启时仍按全局策略保留失败证据，个人开关不覆盖管理员策略。
	if !frozen {
		global, err := s.promptsEnabled(context.Background())
		if err != nil {
			return "settings_unavailable", ""
		}
		if !global {
			return "early_failure", ""
		}
		policy = capturePolicy{userID: captureUserID(p), source: "global"}
	}
	if policy.source == "" {
		return policy.status, ""
	}
	if s.IAM == nil {
		return "storage_failed", ""
	}
	if len(ex.requestBody) == 0 && ex.rawResponse == "" {
		return "empty", ""
	}
	limits, globalDays, configErr := captureConfig()
	if configErr != nil {
		util.Error("invalid request body capture configuration: %v", configErr)
		return "settings_unavailable", ""
	}
	if int64(len(ex.requestBody))+int64(len(ex.rawResponse)) > limits.PerRequest {
		return "too_large", ""
	}
	// 当前日志展示为文本 JSON；拒绝静默改写二进制内容，仍可查看元数据及账单。
	if !utf8.Valid(ex.requestBody) || !utf8.ValidString(ex.rawResponse) {
		return "unsupported", ""
	}
	// 流采集器的 2 MiB 边界可能意味着尾部被截掉，不能把该内容作为完整响应展示。
	if streamed && len(ex.rawResponse) >= 2<<20 {
		return "partial", ""
	}
	_, response, proxy := requestlog.Encode(ex.request, ex.requestBody, []byte(ex.rawResponse))
	if failed {
		// 失败正文必须保留原始字节，包括流内网关终止事件；摘要和账单不携带这份正文。
		response = ex.rawResponse
	}
	days := logCaptureDays
	if policy.source == "global" {
		days = globalDays
	}
	expires := start.UTC().Add(time.Duration(days) * 24 * time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := s.IAM.SaveLogCapture(ctx, iam.LogCapture{RequestID: requestID, UserID: policy.userID, Source: policy.source, CreatedAt: start.UTC(), ExpiresAt: expires, Request: proxy, Response: response}, limits, time.Now())
	if err != nil {
		util.Error("save request body failed request=%s: %v", requestID, err)
		return "storage_failed", ""
	}
	if state == "duplicate" {
		// 碰撞不能关联到先前的其他正文；保守报存储失败，不猜测两条调用属于同一人。
		return "storage_failed", ""
	}
	if state != "saved" {
		return state, ""
	}
	return state, expires.Format(time.RFC3339Nano)
}

// logCaptureCleanupLoop 周期性分批清理过期正文；ctx 取消时退出，错误留待下轮重试并记录。
// 与网关监听生命周期绑定；清理只影响正文表及占用计数，不触及费用、诊断或续聊历史。
func (s *Server) logCaptureCleanupLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			n, err := s.IAM.PurgeExpiredLogCaptures(ctx, time.Now(), 500)
			delay := time.Hour
			if err != nil {
				if ctx.Err() == nil {
					util.Error("expired request body cleanup failed: %v", err)
				}
				delay = time.Minute
			} else if n == 500 {
				delay = time.Second
			}
			timer.Reset(delay)
		}
	}
}
