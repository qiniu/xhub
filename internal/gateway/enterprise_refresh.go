package gateway

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/crewjam/saml"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/util"
)

// samlMetadataExpiry 返回 XML 与所有 IdP 描述符中最早的显式期限；解析失败或没有期限返回零值。
// 管理状态与调度调用，不修改 XML、不把 cacheDuration 当作新的可信截止日期。
func samlMetadataExpiry(raw string) time.Time {
	var e saml.EntityDescriptor
	if xml.Unmarshal([]byte(raw), &e) != nil {
		return time.Time{}
	}
	until := e.ValidUntil
	for _, d := range e.IDPSSODescriptors {
		if d.ValidUntil != nil && !d.ValidUntil.IsZero() && (until.IsZero() || d.ValidUntil.Before(until)) {
			until = *d.ValidUntil
		}
	}
	return until
}

// samlRefreshTrust 将有效期、文档 ID 和外层签名剥离后比较所有已解析协议材料；原 XML 可过期，新 XML 必须有效。
// 固定实体、证书 DER、端点及协议字段；不允许后台更换信任锚，失败返回稳定错误码，不泄露 XML。
func samlRefreshTrust(c *iam.EnterpriseOIDC, raw string, now time.Time) string {
	old, err := enterpriseSAMLMetadata(c.IDPMetadata, c.Issuer, time.Time{}, c.AllowHTTP)
	if err != nil {
		return "invalid_baseline"
	}
	fresh, err := enterpriseSAMLMetadata(raw, c.Issuer, now, c.AllowHTTP)
	if err != nil {
		return "invalid_metadata"
	}
	expiry := samlMetadataExpiry(raw)
	if expiry.IsZero() || !expiry.After(now.Add(2*time.Minute)) || expiry.Before(samlMetadataExpiry(c.IDPMetadata)) {
		return "invalid_expiry"
	}
	normalizeSAMLRefresh(old)
	normalizeSAMLRefresh(fresh)
	if !reflect.DeepEqual(old, fresh) {
		return "trust_changed"
	}
	return ""
}

// normalizeSAMLRefresh 仅移除允许更新的描述性时效字段；用于两个独立内存副本，不修改保存的 XML。
func normalizeSAMLRefresh(e *saml.EntityDescriptor) {
	e.ValidUntil = time.Time{}
	e.CacheDuration = 0
	e.ID = ""
	e.Signature = nil
	for i := range e.IDPSSODescriptors {
		d := &e.IDPSSODescriptors[i]
		d.ValidUntil = nil
		d.CacheDuration = 0
		d.ID = ""
		d.Signature = nil
	}
}

// samlRefreshNext 按间隔与剩余有效期的一半选择下一次执行；失败五分钟重试，最短一分钟避免忙循环。
// now/expiry 为已验证时间；返回持久化 Unix 秒，临近过期时优先提前刷新。
func samlRefreshNext(now, expiry time.Time, hours int, failed bool) int64 {
	delay := time.Duration(hours) * time.Hour
	if failed {
		delay = 5 * time.Minute
	}
	if !expiry.IsZero() && expiry.After(now) && expiry.Sub(now)/2 < delay {
		delay = expiry.Sub(now) / 2
	}
	if delay < time.Minute {
		delay = time.Minute
	}
	return now.Add(delay).Unix()
}

// runSAMLRefresh 领取数据库租约并通过受限 HTTPS 下载；后台与立即刷新共用，最长 20 秒。
// 校验成功才原子替换 XML；任何失败保留旧材料并记录固定错误码，迟到任务不能覆盖管理员新配置。
func (s *Server) runSAMLRefresh(ctx context.Context, id string, manual bool, by iam.Actor) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	job, err := s.Identity().ClaimSAMLRefresh(ctx, id, manual)
	if err != nil {
		return err
	}
	c, err := s.Identity().EnterpriseOIDCByID(ctx, id)
	if err != nil {
		return err
	}
	code := ""
	raw := ""
	if c.Protocol != "saml" || c.IDPMetadataURL != "" || strings.TrimSpace(job.SourceURL) == "" {
		code = "invalid_configuration"
	} else {
		source := *c
		source.IDPMetadataURL = job.SourceURL
		raw, err = s.enterpriseSAMLMetadataXML(ctx, &source)
		if err != nil {
			code = "fetch_failed"
		} else {
			code = samlRefreshTrust(c, raw, time.Now())
		}
		if code == "" {
			if _, err = enterpriseSAMLProvider(c, s.Cfg.GeneralSettings.MasterKey, raw); err != nil {
				code = "invalid_metadata"
			}
		}
	}
	expiry := samlMetadataExpiry(c.IDPMetadata)
	if code == "" {
		expiry = samlMetadataExpiry(raw)
	}
	// 下载超时不能阻止保存失败状态；独立的短写入上下文仍受租约和版本检查保护。
	saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err = s.Identity().FinishSAMLRefresh(saveCtx, by, c, job, raw, code, samlRefreshNext(time.Now(), expiry, job.IntervalHours, code != "")); err != nil {
		return err
	}
	if code != "" {
		return errors.New("SAML metadata refresh failed")
	}
	return nil
}

// enterpriseSAMLRefreshAdmin 管理独立刷新设置与立即执行；沿用平台管理会话权限，不允许用户自选信任证书。
// GET 返回真实状态；PUT 保存设置并审计；POST 失败返回 502，调用方回读具体脱敏状态，不伪报刷新成功。
func (s *Server) enterpriseSAMLRefreshAdmin(w http.ResponseWriter, r *http.Request) {
	p := s.RequireManage(w, r)
	if p == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	c, err := s.Identity().EnterpriseOIDCByID(r.Context(), r.PathValue("connection_id"))
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	if c.Protocol != "saml" {
		util.WriteError(w, 400, "invalid_request", "SAML configuration required")
		return
	}
	by := iam.Actor{ID: p.UserID, Kind: "session"}
	if r.Method == http.MethodPut {
		var in struct {
			Enabled       bool   `json:"enabled"`
			SourceURL     string `json:"source_url"`
			IntervalHours int    `json:"interval_hours"`
		}
		if !directoryDecode(r, &in) || in.IntervalHours < 1 || in.IntervalHours > 24 {
			util.WriteError(w, 400, "invalid_request", "Invalid refresh settings")
			return
		}
		in.SourceURL = strings.TrimSpace(in.SourceURL)
		if in.SourceURL != "" {
			if _, err = enterpriseURL(in.SourceURL); err != nil {
				util.WriteError(w, 400, "invalid_request", "HTTPS metadata source required")
				return
			}
		}
		if in.Enabled && (in.SourceURL == "" || c.IDPMetadataURL != "" || strings.TrimSpace(c.IDPMetadata) == "") {
			util.WriteError(w, 400, "invalid_request", "Saved manual metadata required")
			return
		}
		err = s.Identity().SaveSAMLRefresh(r.Context(), by, c, iam.SAMLRefresh{Enabled: in.Enabled, SourceURL: in.SourceURL, IntervalHours: in.IntervalHours})
		if err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
	}
	if r.Method == http.MethodPost {
		if err = s.runSAMLRefresh(r.Context(), c.ConnectionID, true, by); err != nil {
			if errors.Is(err, iam.ErrConflict) {
				s.WriteIAMError(w, r, err)
			} else {
				util.WriteError(w, 502, "saml_refresh_failed", "Metadata refresh failed; inspect refresh status")
			}
			return
		}
		c, err = s.Identity().EnterpriseOIDCByID(r.Context(), c.ConnectionID)
		if err != nil {
			s.WriteIAMError(w, r, err)
			return
		}
	}
	settings, err := s.Identity().SAMLRefreshByID(r.Context(), c.ConnectionID)
	if err != nil {
		s.WriteIAMError(w, r, err)
		return
	}
	expiry := int64(0)
	if t := samlMetadataExpiry(c.IDPMetadata); !t.IsZero() {
		expiry = t.Unix()
	}
	util.WriteJSON(w, 200, struct {
		*iam.SAMLRefresh
		ExpiresAt int64 `json:"expires_at"`
	}{settings, expiry})
}

// samlRefreshLoop 在网关生命周期内每分钟扫描；多实例由数据库租约互斥，关闭浏览器不影响执行。
// ctx 取消停止并等待当前请求；失败记录脱敏日志，未开启连接不会发出外网请求。
func (s *Server) samlRefreshLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.refreshDueSAML(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// refreshDueSAML 处理有限批量已到期连接；由周期循环及隔离测试调用，错误不中断其他连接。
func (s *Server) refreshDueSAML(ctx context.Context) {
	jobs, err := s.Identity().DueSAMLRefresh(ctx)
	if err != nil {
		util.Error("SAML refresh schedule unavailable")
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		if err = s.runSAMLRefresh(ctx, job.ConnectionID, false, iam.Actor{Kind: "system"}); err != nil && !errors.Is(err, iam.ErrConflict) {
			util.Error("SAML metadata refresh failed for connection %s", job.ConnectionID)
		}
	}
}
