package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

const logCaptureDays = 30

// 单条及汇总上限按原始字节保守计算；后续可在不变更账单的前提下独立配置。
var defaultCaptureLimits = iam.CaptureLimits{PerRequest: 4 << 20, PerUser: 512 << 20, Global: 20 << 30}

// captureConfig 读取正文容量和全局留存的运维配置；未配置时采用保守默认值。
// 返回正整数限制与全局天数；任何非法配置返回错误，不会默认为无限容量或无限留存。
func captureConfig() (iam.CaptureLimits, int, error) {
	limits := defaultCaptureLimits
	// read 把环境变量解析为有界正整数；缺省使用默认值，非法值直接返回错误。
	read := func(name string, fallback int64, maximum int64) (int64, error) {
		raw := os.Getenv(name)
		if raw == "" {
			return fallback, nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 || n > maximum {
			return 0, fmt.Errorf("invalid %s", name)
		}
		return n, nil
	}
	var err error
	if limits.PerRequest, err = read("XHUB_LOG_BODY_REQUEST_BYTES", limits.PerRequest, 1<<40); err != nil {
		return limits, 0, err
	}
	if limits.PerUser, err = read("XHUB_LOG_BODY_USER_BYTES", limits.PerUser, 1<<50); err != nil {
		return limits, 0, err
	}
	if limits.Global, err = read("XHUB_LOG_BODY_GLOBAL_BYTES", limits.Global, 1<<50); err != nil {
		return limits, 0, err
	}
	days, err := read("XHUB_LOG_BODY_GLOBAL_RETENTION_DAYS", logCaptureDays, 3650)
	return limits, int(days), err
}

// LogCaptureSettings 只允许登录会话读取或修改本人的正文记录选择；不接受客户端指定用户。
// w、r 是 HTTP 响应与请求；返回设置、全局生效状态和当前占用。鉴权、格式或数据库失败均明确返回错误。
// 全局开关优先；此接口不更改现有日志权限、不删除历史正文，也不影响预算。
func (s *Server) LogCaptureSettings(w http.ResponseWriter, r *http.Request) {
	p := s.requireUser(w, r)
	if p == nil {
		return
	}
	if p.Kind != authz.KindSession || p.UserID == "" {
		util.WriteError(w, http.StatusForbidden, "forbidden", "A signed-in user session is required")
		return
	}
	if s.IAM == nil {
		util.WriteError(w, http.StatusServiceUnavailable, "unavailable", "User settings unavailable")
		return
	}
	limits, globalDays, err := captureConfig()
	if err != nil {
		util.WriteError(w, http.StatusServiceUnavailable, "unavailable", "Log capture configuration unavailable")
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || body.Enabled == nil {
			util.WriteError(w, 400, "invalid_request", "Expected an enabled boolean")
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			util.WriteError(w, 400, "invalid_request", "Expected a single JSON object")
			return
		}
		if err := s.IAM.SetPersonalLogCapture(r.Context(), p.UserID, *body.Enabled); err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
	}
	enabled, err := s.IAM.PersonalLogCaptureEnabled(r.Context(), p.UserID)
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	used, err := s.IAM.PersonalLogCaptureBytes(r.Context(), p.UserID)
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	global, err := s.promptsEnabled(r.Context())
	if err != nil {
		util.WriteError(w, http.StatusServiceUnavailable, "unavailable", "Log capture configuration unavailable")
		return
	}
	util.WriteJSON(w, 200, map[string]any{"enabled": enabled, "global_enabled": global, "effective_enabled": enabled || global, "retention_days": logCaptureDays, "global_retention_days": globalDays, "used_bytes": used, "limit_bytes": limits.PerUser})
}
