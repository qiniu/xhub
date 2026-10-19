// 本文件验证 runtime 的相关执行契约，阅读顺序为测试宿主/辅助方法，再到各个 Test 场景。
// 具体前置、期望、边界和清理方式见每个测试方法；夹具的空实现仅用于隔离当前测试。
package runtime

import (
	"testing"
	"time"

	"github.com/sunqirui1987/xhub/internal/live"
)

// TestQueuedUsageRecordSessionAttribution 验证异步账单保留请求别名、实际供应商、推理强度和会话识别来源。
// 参数 t 为测试上下文；前置纯内存成功、失败、缓存命中及旧载荷，验证完整快照与缺失字段不被推断。
// 返回无；不连接外部服务、不创建持久数据，无需清理。
func TestQueuedUsageRecordSessionAttribution(t *testing.T) {
	for _, source := range []string{"explicit", "parent", "cache_key", "prompt", "generated", ""} {
		for _, status := range []string{"success", "error"} {
			t.Run(source+"/"+status, func(t *testing.T) {
				row := live.SpendLog{ReasoningEffort: "high", Label: "Affinity | 并发赢家", Model: "requested-group", APIBase: "https://supplier-b.example/v1", Supplier: "supplier-b", UpstreamModel: "actual-model", SessionID: "session", SessionSource: source, Status: status, CacheHit: true, Start: "2026-10-12T00:00:00Z", End: "2026-10-12T00:00:01Z"}
				got := queuedUsageRecord(row)
				if got.ReasoningEffort != row.ReasoningEffort || got.Label != row.Label || got.Model != row.Model || got.APIBase != row.APIBase || got.Supplier != row.Supplier || got.UpstreamModel != row.UpstreamModel || got.SessionID != row.SessionID || got.SessionSource != source || got.Status != status || !got.CacheHit {
					t.Fatalf("异步落库改变请求或执行归属：%+v", got)
				}
			})
		}
	}
	before := time.Now()
	got := queuedUsageRecord(live.SpendLog{Start: "invalid", End: "invalid", Spend: 99})
	if got.ReasoningEffort != "" || got.SessionSource != "" || got.SessionID != "" || got.APIBase != "" || got.Supplier != "" || got.UpstreamModel != "" || got.TS.Before(before) || got.EndedAt.Before(before) || got.Cost != 0 {
		t.Fatalf("旧载荷或非法时间不得伪造会话与计费事实：%+v", got)
	}
}

// TestHotDeltasIncludesProject 验证花费按密钥、团队、用户、组织及项目累积，项目预算热计数不会遗漏。
// 参数 t 为 Go 测试上下文；返回值无，断言失败定位具体协议、路由或日志行为。
// 前置使用独立测试配置及内存传输/纯函数输入，不依赖外部供应商凭据。
// 测试状态只存在本例内存，不产生持久化数据。
func TestHotDeltasIncludesProject(t *testing.T) {
	rows := []live.SpendLog{{APIKey: "k", TeamID: "t", UserID: "u", OrgID: "o", ProjectID: "p", Spend: 0.25, SpendValid: true}, {ProjectID: "p", Spend: 100}}
	got := hotDeltas(rows)
	for _, id := range []string{"key/k", "team/t", "user/u", "org/o", "project/p"} {
		if got[id] != 0.25 {
			t.Fatalf("%s delta = %v", id, got[id])
		}
	}
}

// TestQueuedUsageRecordEndpoints 前置内存队列日志，验证成功、失败、未配置端点与非法时间的转换。
// 参数 t 为测试上下文；端点、计时和错误均不得丢失，测试无外部数据，无需清理。
func TestQueuedUsageRecordEndpoints(t *testing.T) {
	for _, status := range []string{"success", "error"} {
		t.Run(status, func(t *testing.T) {
			ttft := 100
			row := live.SpendLog{RequestID: "endpoint", InboundEndpoint: "/v1/messages", OutboundEndpoint: "/v1/responses", Status: status, Error: "upstream error", Start: "2026-10-12T00:00:00Z", End: "2026-10-12T00:00:01Z", TTFTMs: &ttft, Spend: 0.2, SpendValid: true}
			got := queuedUsageRecord(row)
			if got.InboundEndpoint != row.InboundEndpoint || got.OutboundEndpoint != row.OutboundEndpoint || got.DurationMS != 1000 || got.TTFTMs != &ttft || got.Status != status || got.Error != row.Error || got.Cost != 0.2 {
				t.Fatalf("异步落库丢失端点或计量事实: %+v", got)
			}
		})
	}
	got := queuedUsageRecord(live.SpendLog{Start: "invalid", End: "invalid", Spend: 100})
	if got.InboundEndpoint != "" || got.OutboundEndpoint != "" || got.TS.IsZero() || got.EndedAt.IsZero() || got.Cost != 0 {
		t.Fatalf("缺失端点或非法计量不能伪造事实: %+v", got)
	}
}
