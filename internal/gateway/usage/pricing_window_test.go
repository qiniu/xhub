package usage

import (
	"testing"

	"github.com/sunqirui1987/xhub/internal/iam"
)

// TestPricingWindowRequiresSnapshotEvidence 验证分时标识只来自实际应用费率的历史快照；固定价、旧快照、冲突和未定价不推断，内存数据无需清理。
func TestPricingWindowRequiresSnapshotEvidence(t *testing.T) {
	for _, tc := range []struct{ name, snapshot, want string }{
		{"高峰", `{"window":"peak","applied":[{"side":"input","rate_window":"peak","quantity":2,"usd":1}]}`, "peak"},
		{"低谷", `{"window":"offpeak","applied":[{"side":"input","rate_window":"all","quantity":2,"usd":1},{"side":"output","rate_window":"offpeak","quantity":1,"usd":1}]}`, "offpeak"},
		{"固定价", `{"window":"peak","applied":[{"side":"input","rate_window":"all","quantity":2,"usd":1}]}`, ""},
		{"旧快照", `{"window":"peak","applied":[{"side":"input","source_key":"ncache_peak","quantity":2,"usd":1}]}`, ""},
		{"冲突", `{"window":"peak","applied":[{"side":"input","rate_window":"offpeak","quantity":2,"usd":1}]}`, ""},
		{"未计价", `{"window":"peak","pricing_status":"unpriced","applied":[{"side":"input","rate_window":"peak","quantity":2,"usd":1}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := costBreakdown(iam.UsageEvent{PriceSnapshot: tc.snapshot, Cost: 2})
			if window, _ := got["pricing_window"].(string); window != tc.want {
				t.Fatalf("分时快照展示时段=%q，期望=%q，输出=%v", window, tc.want, got)
			}
			if got["window"] == nil {
				t.Fatal("原有 window 字段必须保留供旧客户端兼容")
			}
		})
	}
}
