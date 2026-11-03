package catalog

import (
	"encoding/json"
	"testing"
	"time"
)

// TestSnapshotPreservesSelectedRateWindow 验证仅把实际命中的费率时段写入快照；固定价与分时价均使用内存数据，无外部资源需清理。
func TestSnapshotPreservesSelectedRateWindow(t *testing.T) {
	at := time.Date(2026, 3, 2, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ name, window string }{{"固定价", "all"}, {"高峰价", "peak"}} {
		t.Run(tc.name, func(t *testing.T) {
			charge, ok := CostFromRates([]Rate{{Measure: "token", Side: "input", Window: tc.window, UnitSize: 1, USD: 0.01}}, Usage{PromptTokens: 10}, at)
			if !ok {
				t.Fatal("测试费率应可计价")
			}
			var snap struct {
				Applied []map[string]any `json:"applied"`
			}
			if err := json.Unmarshal([]byte(Snapshot(charge)), &snap); err != nil {
				t.Fatal(err)
			}
			if len(snap.Applied) != 1 || snap.Applied[0]["rate_window"] != tc.window {
				t.Fatalf("快照未保存实际命中费率时段：%v", snap.Applied)
			}
		})
	}
}
