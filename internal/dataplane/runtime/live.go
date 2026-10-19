// 本文件读取 Redis 路由热状态、报告失败/延迟/用量，并将日志队列先落数据库再确认删除。
// 调用关系与边界：路由热状态及花费日志后台刷写；具体参数、返回及副作用见各方法说明。
package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	"github.com/sunqirui1987/xhub/internal/router"
	"github.com/sunqirui1987/xhub/internal/util"
)

// logTraceOnceLive 限制 State 的入口跟踪日志在本进程生命周期内只输出一次；不参与路由状态同步。
var logTraceOnceLive sync.Once

// State 组装路由器需要的当前并发、冷却、延迟和 token 用量快照。
// 参数 h 为运行时能力宿主；返回 router.State；gateway 的路由状态方法调用。
// 先取本进程 BusyMap，再用当前模型的 CooldownID 批量查询 Redis；没有 Redis 时只返回 Busy。
// 此快照只用于本轮排序，不构成分布式锁或事务一致视图；首次调用额外输出一次跟踪日志。
// 函数读取外部热状态，不修改部署或预算；是否复制 BusyMap 由宿主契约负责。
func State(h Runtime) router.State {
	logTraceOnceLive.Do(func() { util.Trace("enter dataplane.State") })

	st := router.State{Busy: h.BusyMap()}
	redis := h.Redis()
	if redis == nil {
		return st
	}
	ids := make([]string, 0, len(h.Models()))
	for _, m := range h.Models() {
		ids = append(ids, router.CooldownID(m))
	}
	st.Cooldown = redis.Cooled(ids)
	st.Latency = redis.Latencies(ids)
	st.Usage = redis.Usages(ids)
	return st
}

// RecordFailure 按本次请求的模板阈值增加部署失败计数，并在达到阈值时冷却部署。
// 参数 h 提供 Redis，id 为部署运行身份，settings 为本轮 RouteSettingsFor 的结果；返回值无。
// gateway 的 NoteFailure 调用；无 Redis、空 ID 或 AllowedFails 小于 1 时直接返回。
// 非正冷却时间回退为一分钟；使用请求模板而非全局配置，使不同租户各自的阈值真正生效。
// 副作用为 Redis 失败计数/冷却状态；Redis 错误在此不向客户端传播，不能据此假定记录必定成功。
func RecordFailure(h Runtime, id string, settings router.RouteSettings) {
	redis := h.Redis()
	if redis == nil || id == "" {
		return
	}
	allowed := settings.AllowedFails()
	if allowed < 1 {
		return
	}
	cd := time.Duration(settings.CooldownSeconds() * float64(time.Second))
	if cd <= 0 {
		cd = time.Minute
	}
	_ = redis.RecordFailure(id, allowed, cd)
}

// RecordLatency 把部署本次延迟样本累积到 Redis，供延迟策略下轮排序。
// 参数 h 提供热状态客户端，id 为运行身份，ms 为毫秒耗时；返回值无，gateway 的 NoteLatency 调用。
// 无 Redis 时跳过，写入错误不传播；本方法不自行校验样本范围，也不改变当前已经选出的部署。
// 副作用仅为热统计更新，不写数据库账单。
func RecordLatency(h Runtime, id string, ms float64) {
	if h.Redis() == nil {
		return
	}
	_ = h.Redis().AddLatency(id, ms)
}

// RecordUsage 将部署本次 token 数累积到 Redis 路由用量统计。
// 参数 h 为宿主，id 为运行身份，tokens 为本次统计量；返回值无，gateway 的用量记录方法调用。
// 无 Redis 时跳过，写入错误不传播；参数有效性由调用链负责，不能把路由统计成功视作费用持久化成功。
// 副作用为热状态更新，不直接扣预算或写数据库。
func RecordUsage(h Runtime, id string, tokens int) {
	if h.Redis() == nil {
		return
	}
	_ = h.Redis().AddUsage(id, tokens)
}

// FlushLoop 按指定周期将 Redis 用量队列刷入身份数据库；网关启动及隔离测试调用。
// 参数 ctx 控制循环退出，h 提供存储依赖，interval 为启动时确定的周期；非正值回退到 5 秒。
// 返回值无；取消后停止 ticker，正在进行的 Flush 会完成，不额外确认或清空未提交的队列。
// 每轮同时补执行到期组织预算，即使 Redis 不可用；失败保留状态供下轮重试，重置与审计同事务。
// 队列失败保留批次，数据库提交后才确认 Redis，不改变可靠结算语义。
func FlushLoop(ctx context.Context, h Runtime, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// 没有 Redis 或没有调用时也执行到期充值；失败保留状态，由下轮/准入重试。
			if db := h.Identity(); db != nil {
				if err := db.ResetOrganizationBudgets(ctx, time.Now()); err != nil {
					util.Error("organization budget reset failed: %v", err)
				}
			}
			Flush(h)
		}
	}
}

// Flush 将至多 500 条队列用量先持久化，再确认删除并调整热预算计数。
// 参数 h 提供 Redis 与身份数据库；返回值无，FlushLoop 或网关显式刷写方法调用。
// 任一依赖为 nil 或队列为空时跳过；PeekLogs 只窥视，数据库写入失败记录日志且保留整批队列。
// PeekLogs 遇到损坏记录只返回此前的有效前缀；损坏记录位于队头时返回空批次，保留原条目等待修复。
// 成功后用有效前缀的原始条数及首元素 head 确认，防止并发刷写误删已变化的队头。
// hotDeltas 扣回已经持久化的临时预算增量；数据库幂等性处理重试，确认失败可能导致下次再次写同一批。
// 副作用涉及数据库和 Redis，二者不是同一事务；不应宣称调用返回就意味着队列一定已清空。
func Flush(h Runtime) {
	redis := h.Redis()
	db := h.Identity()
	if redis == nil || db == nil {
		return
	}
	logs, raw := redis.PeekLogs(500)
	n := len(raw)
	if n == 0 {
		return
	}
	head := raw[0]
	// logs 与 raw 是同一有效前缀的两种表示；只确认这段前缀，不越过损坏记录删除后续数据。
	records := make([]iam.UsageRecord, 0, len(logs))
	for _, row := range logs {
		records = append(records, queuedUsageRecord(row))
	}
	if len(records) == 0 {
		// 防御性处理有原始条目却没有转换记录的批次；当前 PeekLogs 不会把损坏条目放进 raw。
		_ = redis.AckFlushed(nil, n, head)
		return
	}
	if err := db.RecordUsage(context.Background(), records); err != nil {
		util.Error("spend flush failed: %v", err)
		return
	}
	// 数据库先成功再删除队列；同时扣回热预算，避免同一笔费用同时算在热状态与持久余额中。
	// Ack 的队头比较防止另一刷写者改变队列后误删新记录，失败留待下一轮重试。
	_ = redis.AckFlushed(hotDeltas(logs), n, head)
}

// queuedUsageRecord 将一条 Redis 日志转成完整数据库用量快照。
// 参数 row 为队列记录；返回 iam.UsageRecord；Flush 的批量转换调用，无网络或存储副作用。
// 解析纳秒时间戳，非法起止时间各自回退到当前时间；SpendValid 为 false 时费用保留零。
// 会话来源、端点、供应商、流标志、原始上游诊断及价格快照直接继承，不能根据正文保存开关猜测或丢弃。
// 只做字段映射，不重新测量 token、重新定价或推断旧记录缺失的归属。
func queuedUsageRecord(row live.SpendLog) iam.UsageRecord {
	start, err := time.Parse(time.RFC3339Nano, row.Start)
	if err != nil {
		start = time.Now()
	}
	end, err := time.Parse(time.RFC3339Nano, row.End)
	if err != nil {
		end = time.Now()
	}
	spend := 0.0
	if row.SpendValid {
		spend = row.Spend
	}
	return iam.UsageRecord{
		InboundEndpoint: row.InboundEndpoint, OutboundEndpoint: row.OutboundEndpoint,
		APIBase: row.APIBase, Supplier: row.Supplier, UpstreamModel: row.UpstreamModel, Streamed: row.Streamed,
		RequestID: row.RequestID, TS: start, KeyID: row.KeyID, OwnerType: row.OwnerType,
		UserID: row.UserID, TeamID: row.TeamID, ProjectID: row.ProjectID, OrganizationID: row.OrgID,
		Model: row.Model, CallType: row.CallType, Status: row.Status, HTTPStatus: row.HTTPStatus,
		PromptTokens: row.Prompt, CompletionTokens: row.Completion, Cost: spend,
		DurationMS:  int(end.Sub(start).Milliseconds()),
		RequestBody: row.Messages, ResponseBody: row.Response, Error: row.Error, ProxyRequest: row.ProxyRequest,
		UpstreamResponse: row.UpstreamResponse,
		EndedAt:          end, TTFTMs: row.TTFTMs, CacheHit: row.CacheHit,
		KeyHash: row.KeyHash, KeyAlias: row.KeyAlias, TeamAlias: row.TeamAlias,
		Provider: row.Provider, CachedTokens: row.CachedTokens,
		// 会话来源必须随队列快照落库；仅保留 SessionID 会让异步账单丢失 explicit/parent 归属。
		SessionID: row.SessionID, SessionSource: row.SessionSource, CacheKey: row.CacheKey, Guardrail: row.Guardrail,
		PriceSnapshot: row.PriceSnapshot, Label: row.Label,
	}
}

// hotDeltas 汇总一批成功持久化日志对应的临时预算增量，供 Redis 确认时扣回。
// 参数 logs 为队列日志切片；返回 SpendRef 到金额的映射；仅由 Flush 的成功确认路径调用。
// 只累计 SpendValid 且金额非零的记录，按密钥、团队、用户、组织与项目各自聚合；空身份引用跳过。
// 无效或零费用不应扣回热预算，避免失败日志改变额度；函数只构造内存映射，不直接修改预算。
func hotDeltas(logs []live.SpendLog) map[string]float64 {
	out := map[string]float64{}
	for _, row := range logs {
		if !row.SpendValid || row.Spend == 0 {
			continue
		}
		for _, ref := range []struct{ kind, id string }{
			{"key", row.APIKey}, {"team", row.TeamID}, {"user", row.UserID},
			{"org", row.OrgID}, {"project", row.ProjectID},
		} {
			id := live.SpendRef(ref.kind, ref.id)
			if id != "" {
				out[id] += row.Spend
			}
		}
	}
	return out
}

// Runtime 定义路由热状态读取和用量队列刷写所需的后台能力。
// gateway 提供并发快照、模型目录、Redis 与身份数据库；runtime.State/Record*/Flush 消费，无 HTTP 正文依赖。
// Redis 或数据库可为 nil，具体降级见各方法；FlushLoop 由宿主传入取消上下文。
type Runtime interface {
	// BusyMap 返回本进程内每个部署 id 正在处理的请求数。
	// 参数：无。接收者是实现 Runtime 的进程。
	// 返回：部署 id 到正在处理的请求数。没有该 id 表示当前是 0，不是一份解析出来的 JSON。
	// 调用：State，放进 router.State.Busy。
	// 测试：failure_log_test.go 的夹具返回空表。
	BusyMap() map[string]int

	// Redis 返回热路径客户端。未配置 Redis 时返回 nil，State 只填 Busy，Flush 直接返回。
	// 参数：无。
	// 返回 *live.Client（*live.Client）：热路径 Redis 客户端。未配置 Redis 时为 nil，花费改为当场写数据库。
	// 调用：dataplane/runtime/live.go、gateway/wire.go
	// 测试：bypass_logic_test.go、failure_log_test.go
	Redis() *live.Client

	// Models 返回当前部署目录；无参数，State 只读计算每个部署的 CooldownID，用于批量查询热统计。
	// 返回切片及嵌套配置由宿主持有，不能在收集状态时修改；空目录仍可保留进程 BusyMap。
	Models() []config.ModelEntry

	// Identity 是 Flush 写入用量的库。没配 PostgreSQL 时为 nil，Flush 直接返回。
	// 参数：无。
	// 返回 *iam.DB（*iam.DB）：Flush 写入用量的库。没有 PostgreSQL 时为 nil，Flush 直接返回。
	// 调用：dataplane/runtime/live.go、gateway/identity/gate.go、gateway/identity/handlers.go、gateway/identity/members.go
	// 测试：bypass_logic_test.go、failure_log_test.go
	Identity() *iam.DB
}
