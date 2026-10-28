package gateway

import (
	"github.com/sunqirui1987/xhub/internal/iam"
	"strings"
	"testing"
	"time"
)

// TestSAMLRefreshTrust 验证纯内存可信边界；证书固定，仅时效更新允许，畸形/换钥/过期拒绝，无资源清理。
func TestSAMLRefreshTrust(t *testing.T) {
	_, _, cert := enterpriseSAMLTestMaterial(t)
	base := enterpriseSAMLTestMetadata(cert)
	now := time.Now().UTC().Truncate(time.Second)
	// 为固定夹具加入显式期限，不依赖真实时间推进或远程身份平台。
	withExpiry := func(raw string, at time.Time) string {
		return strings.Replace(raw, "<EntityDescriptor", `<EntityDescriptor validUntil="`+at.Format(time.RFC3339)+`"`, 1)
	}
	old := withExpiry(base, now.Add(-time.Hour))
	good := withExpiry(base, now.Add(48*time.Hour))
	c := &iam.EnterpriseOIDC{Issuer: "https://idp.example/entity", IDPMetadata: old}
	if samlMetadataExpiry(old).IsZero() {
		t.Fatal("过期夹具未设置期限")
	}
	for _, tc := range []struct{ name, raw, want string }{{"renew", good, ""}, {"malformed", "<broken>", "invalid_metadata"}, {"expired", old, "invalid_metadata"}, {"missing expiry", base, "invalid_expiry"}, {"endpoint", strings.ReplaceAll(good, "/sso", "/changed"), "trust_changed"}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := samlRefreshTrust(c, tc.raw, now); got != tc.want {
				t.Fatalf("信任检查=%q want=%q", got, tc.want)
			}
		})
	}
	_, _, otherCert := enterpriseSAMLTestMaterial(t)
	if got := samlRefreshTrust(c, withExpiry(enterpriseSAMLTestMetadata(otherCert), now.Add(48*time.Hour)), now); got != "trust_changed" {
		t.Fatal("更换证书未要求管理员确认", got)
	}
	c.IDPMetadata = "<bad>"
	if samlRefreshTrust(c, good, now) != "invalid_baseline" {
		t.Fatal("无效基线被接受")
	}
}

// TestSAMLRefreshNext 覆盖默认周期、临近到期、失败重试和最小间隔；固定时钟纯函数，无资源。
func TestSAMLRefreshNext(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		expiry time.Time
		fail   bool
		want   time.Duration
	}{{now.Add(48 * time.Hour), false, 6 * time.Hour}, {now.Add(time.Hour), false, 30 * time.Minute}, {now.Add(time.Hour), true, 5 * time.Minute}, {now.Add(time.Second), false, time.Minute}, {time.Time{}, true, 5 * time.Minute}} {
		if got := samlRefreshNext(now, tc.expiry, 6, tc.fail); got != now.Add(tc.want).Unix() {
			t.Fatal("调度时间错误", got)
		}
	}
	if !samlMetadataExpiry("<bad>").IsZero() {
		t.Fatal("畸形 XML 被当成期限")
	}
}
