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

// BeginLogCapture 在身份确认后冻结当前个人和全局设置，callID 标识本次调用。
// 数据面在发往上游前调用；关闭只影响之后的新请求，查询错误按不记录处理并显式标记。
// 临时策略由 recordSpend 消费或 ReleaseCall 回收，不保存用户正文或改变鉴权与账单。
func (s *Server) BeginLogCapture(callID string, p *auth.Principal) {
	if s == nil || callID == "" {
		return
	}
	policy := s.selectCapturePolicy(p)
	s.mu.Lock()
	if s.capturePolicies == nil {
		s.capturePolicies = map[string]capturePolicy{}
	}
	s.capturePolicies[callID] = policy
	s.mu.Unlock()
}

// selectCapturePolicy 使用认证主体及管理员设置解析一次策略；p 可以为空，绝不猜测共享密钥的个人归属。
// 返回来源、用户及失败状态；读取失败不阻断模型请求，也不保存正文。
func (s *Server) selectCapturePolicy(p *auth.Principal) capturePolicy {
	policy := capturePolicy{userID: captureUserID(p)}
	if s.promptsEnabled() {
		policy.source = "global"
		return policy
	}
	if policy.userID == "" || s.IAM == nil {
		policy.status = "disabled"
		return policy
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
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

// captureExchange 根据已认证身份和管理员全局设置保存一次交换的正文；start 为原始请求时间，streamed 标识实际流式传输。
// 返回可展示状态和到期时间；超限或数据库故障不改变上游响应及账单，正文不进入持久账单队列。
// 控制平面/调用结束时调用；流式捕获到达现有限额时保守标记为不完整，不伪称完整。
func (s *Server) captureExchange(p *auth.Principal, requestID string, ex promptExchange, start time.Time, streamed bool) (string, string) {
	s.mu.Lock()
	policy, frozen := s.capturePolicies[requestID]
	delete(s.capturePolicies, requestID)
	s.mu.Unlock()
	// 入站读取错误等没有冻结个人策略；不能以失败时的新个人设置回溯保存已到达的正文。
	// 管理员全局开启时仍按全局策略保留失败证据，个人开关不覆盖管理员策略。
	if !frozen {
		if !s.promptsEnabled() {
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
