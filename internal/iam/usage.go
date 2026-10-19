package iam

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/sunqirui1987/xhub/internal/util"
	"xorm.io/xorm"
)

var logTraceOnceUsage sync.Once

// UsageEvent 是 usage_events 中一条推理用量和调用日志事实，由刷库链写入、授权查询读取。
// 身份与供应商字段是调用时快照，成员离队或配置删除不改变历史费用；SessionID 只用于归组。
type UsageEvent struct {
	// 客户端明确指定的推理强度快照；独立于提示词保存，空值表示未指定或历史未记录，不推断厂商默认值。
	ReasoningEffort string `xorm:"reasoning_effort" json:"reasoning_effort"`
	// 客户端实际请求路径，不含查询串；由执行器记录，独立于提示词存储。
	InboundEndpoint string `xorm:"inbound_endpoint" json:"inbound_endpoint"`
	// 实际发送的上游路径，由传输层观察；未发送时为空。
	OutboundEndpoint string `xorm:"outbound_endpoint" json:"outbound_endpoint"`
	// 是否实际流式执行，供性能统计区分首字延迟与非流完整响应耗时。
	Streamed bool `xorm:"streamed" json:"streamed"`
	// 调用时供应商连接身份快照；连接修改后不重写历史归属。
	Supplier string `xorm:"'supplier'" json:"supplier"`
	// 调用时上游基础地址快照，已剥离认证信息，不随部署修改或删除改变。
	APIBase string `xorm:"api_base" json:"api_base"`
	// 实际发送的上游型号快照，与公开 Model 分开保存。
	UpstreamModel string `xorm:"'upstream_model'" json:"upstream_model"`
	// 原异步请求的持久化结算标记，防止并发或重复轮询二次收费。
	TaskSettled bool `xorm:"'task_settled'" json:"-"`
	// usage_events 自增内部行号；API 日志关联使用 RequestID。
	ID int64 `xorm:"pk autoincr 'id'" json:"-"`
	// 调用日志/账单事件唯一标识，用于关联正文及结算去重；不能用 SessionID 替代。
	RequestID string `xorm:"'request_id'" json:"request_id"`
	// 请求开始时间，不使用异步刷库时刻，避免账单按错误日期归属。
	TS time.Time `xorm:"'ts'" json:"ts"`
	// 鉴权时 api_keys 行 ID 快照，用于日志权限和账单归属；会话用户可为空。
	KeyID string `xorm:"'key_id'" json:"key_id"`
	// 鉴权时账单归属类型快照，与各层 ID 一起确定用量归属。
	OwnerType string `xorm:"'owner_type'" json:"owner_type"`
	// 实际调用用户的 ID 快照，用于日志权限隔离和账单归属。
	UserID string `xorm:"'user_id'" json:"user_id"`
	// 调用时团队 ID 快照，未归属团队时为空，不随后续成员关系变化。
	TeamID string `xorm:"'team_id'" json:"team_id"`
	// 调用时项目 ID 快照，未归属项目时为空。
	ProjectID string `xorm:"'project_id'" json:"project_id"`
	// 调用时组织 ID 快照，异步刷库时不重新查成员关系。
	OrganizationID string `xorm:"'organization_id'" json:"organization_id"`
	// 请求日志采用的公开模型/组别名，由 RecordSpend 的 alias 传入；实际上游型号由 UpstreamModel 单独记录。
	Model string `xorm:"'model'" json:"model"`
	// 执行操作类别，如 chat 或官方端点动作，用于日志及用量分类。
	CallType string `xorm:"'call_type'" json:"call_type"`
	// 成功或失败分类，用于日志筛选；精确响应码见 HTTPStatus。
	Status string `xorm:"'status'" json:"status"`
	// 最终返回客户端的 HTTP 状态码；0 表示未保存该事实。
	HTTPStatus int `xorm:"'http_status'" json:"http_status,omitempty"`
	// 归一化输入 token 数，由上游用量产生，无实测时为 0。
	PromptTokens int `xorm:"'prompt_tokens'" json:"prompt_tokens"`
	// 归一化输出 token 数，由上游用量产生，无实测时为 0。
	CompletionTokens int `xorm:"'completion_tokens'" json:"completion_tokens"`
	// 持久化费用，单位沿用平台账单币种；零费用调用也保留日志。
	Cost float64 `xorm:"'cost'" json:"cost"`
	// 从请求开始到结束的耗时，单位毫秒，不等同于首字延迟。
	DurationMS int `xorm:"'duration_ms'" json:"duration_ms"`
	// 请求结束时间，未取得结束事实时为空或零值，由写库逻辑处理。
	EndedAt *time.Time `xorm:"'ended_at'" json:"ended_at,omitempty"`
	// 流请求的首字节或非流请求的完整响应耗时，单位毫秒；nil 表示没有测量。
	TTFTMs *int `xorm:"'ttft_ms'" json:"ttft_ms,omitempty"`
	// 是否返回网关响应缓存；命中仍记录用量但推理费用为 0，不提交上游成功偏好。
	CacheHit bool `xorm:"'cache_hit'" json:"cache_hit"`
	// 调用密钥哈希快照，用于定位身份，不是明文密钥。
	KeyHash string `xorm:"'key_hash'" json:"key_hash"`
	// 调用时密钥显示名称快照，名称变化后不重写旧日志。
	KeyAlias string `xorm:"'key_alias'" json:"key_alias"`
	// 调用时团队显示名称快照，供日志展示。
	TeamAlias string `xorm:"'team_alias'" json:"team_alias"`
	// 执行供应商/适配器标识，区别于具体供应商连接 ID。
	Provider string `xorm:"'provider'" json:"provider"`
	// 上游报告的缓存输入 token 数；nil 表示没有统计，0 表示明确没有命中。
	CachedTokens *int `xorm:"'cached_tokens'" json:"cached_tokens,omitempty"`
	// SessionID 的识别来源，不是供应商来源或路由策略。由 PlanRoute 生成，经 CallNote 写入日志。
	// explicit：标准 X-Session-Id/正文 session_id；parent：授权父响应继承；
	// cache_key：prompt_cache_key 哈希；prompt：稳定首轮内容指纹；generated：随机 UUID。
	// cache_key/prompt 在日志中标为推断；不参与权限判断。空值表示未记录来源；JSON 和入库行为由当前载体字段标签决定。
	SessionSource string `xorm:"'session_source'" json:"session_source"`
	// 明确、继承、推断或生成的会话标识，用于响应头和授权范围内日志归组；不是响应 ID，不授予历史读取权限。
	SessionID string `xorm:"'session_id'" json:"session_id"`
	// 网关响应缓存键，与会话指纹、原生 prompt_cache_key 及偏好缓存键相互独立。
	CacheKey string `xorm:"'cache_key'" json:"cache_key"`
	// 护栏检查结果 JSON 数组，日志抽屉读取；空值表示未记录检查结果。
	Guardrail string `xorm:"'guardrail'" json:"-"`
	// 客户端备注，仅用于日志关联，不授予权限或证明测试通过。
	Label string `xorm:"label" json:"label,omitempty"`
	// 实际费率和计费数量 JSON 快照，读取日志不按现价重算；空值表示未定价。
	PriceSnapshot string `xorm:"'price_snapshot'" json:"price_snapshot,omitempty"`
}

// 告诉 xorm 这个结构体对应数据库表 usage_events。
// 参数：无。
// 返回 string（string）：xorm 使用的表名 usage_events。这个结构体的行都进这张表。
// 调用：xorm 在映射这张表时。
// 测试：无直接单测
func (UsageEvent) TableName() string { return "usage_events" }

// RequestLog is the stored request and response for one usage event. It is the
// sensitive half of a log: reading another account's row is what the audit log
// records.
type RequestLog struct {
	RequestID    string `xorm:"pk 'request_id'" json:"request_id"`
	RequestBody  string `xorm:"'request_body'" json:"request_body"`
	ResponseBody string `xorm:"'response_body'" json:"response_body"`
	Error        string `xorm:"'error'" json:"error"`
	// UpstreamResponse 独立保存诊断，不受提示词存储开关控制。
	UpstreamResponse string `xorm:"'upstream_response'"`
	ProxyRequest     string `xorm:"'proxy_request'" json:"proxy_request"`
}

// 告诉 xorm 这个结构体对应数据库表 request_logs。
// 参数：无。
// 返回 string（string）：xorm 使用的表名 request_logs。这个结构体的行都进这张表。
// 调用：xorm 在映射这张表时。
// 测试：无直接单测
func (RequestLog) TableName() string { return "request_logs" }

// UsageDaily is the pre-aggregated roll-up, keyed by day and full ownership.
// Every dimension is part of the key so one increment touches exactly one row
// and concurrent flushes cannot land on different rows.
type UsageDaily struct {
	Day              time.Time `xorm:"pk 'day'" json:"day"`
	OrganizationID   string    `xorm:"pk 'organization_id'" json:"organization_id"`
	TeamID           string    `xorm:"pk 'team_id'" json:"team_id"`
	ProjectID        string    `xorm:"pk 'project_id'" json:"project_id"`
	UserID           string    `xorm:"pk 'user_id'" json:"user_id"`
	KeyID            string    `xorm:"pk 'key_id'" json:"key_id"`
	OwnerType        string    `xorm:"pk 'owner_type'" json:"owner_type"`
	Model            string    `xorm:"pk 'model'" json:"model"`
	Requests         int64     `xorm:"'requests'" json:"requests"`
	PromptTokens     int64     `xorm:"'prompt_tokens'" json:"prompt_tokens"`
	CompletionTokens int64     `xorm:"'completion_tokens'" json:"completion_tokens"`
	Cost             float64   `xorm:"'cost'" json:"cost"`
}

// 告诉 xorm 这个结构体对应数据库表 usage_daily。
// 参数：无。
// 返回 string（string）：xorm 使用的表名 usage_daily。这个结构体的行都进这张表。
// 调用：xorm 在映射这张表时。
// 测试：无直接单测
func (UsageDaily) TableName() string { return "usage_daily" }

// UsageRecord 是等待持久化的一次调用，由网关或 Redis 消费者交给 RecordUsage。
// 身份采用鉴权时快照，刷库不重新解析层级关系；正文、费用和会话来源分别保留各自记录规则。
type UsageRecord struct {
	// 客户端明确指定的推理强度快照；独立于提示词保存，空值表示未指定或历史未记录，不推断厂商默认值。
	ReasoningEffort string
	// 客户端实际请求路径，不含查询串；由执行器记录，独立于提示词存储。
	InboundEndpoint string
	// 实际发送的上游路径，由传输层观察；未发送时为空。
	OutboundEndpoint string
	// 是否实际流式执行，供性能统计区分首字延迟与非流完整响应耗时。
	Streamed bool
	// 调用时供应商连接身份快照；连接修改后不重写历史归属。
	Supplier string
	// 实际发送的上游型号快照，与公开 Model 分开保存。
	UpstreamModel string
	// 调用时脱敏基础地址，供直接及异步落库保存上游来源。
	APIBase string
	// 调用日志/账单事件唯一标识，用于关联正文及结算去重；不能用 SessionID 替代。
	RequestID string
	// 请求开始时间，不使用异步刷库时刻，避免账单按错误日期归属。
	TS time.Time
	// 鉴权时 api_keys 行 ID 快照，用于日志权限和账单归属；会话用户可为空。
	KeyID string
	// 鉴权时账单归属类型快照，与各层 ID 一起确定用量归属。
	OwnerType string
	// 实际调用用户的 ID 快照，用于日志权限隔离和账单归属。
	UserID string
	// 调用时团队 ID 快照，未归属团队时为空，不随后续成员关系变化。
	TeamID string
	// 调用时项目 ID 快照，未归属项目时为空。
	ProjectID string
	// 调用时组织 ID 快照，异步刷库时不重新查成员关系。
	OrganizationID string
	// 请求日志采用的公开模型/组别名，由 RecordSpend 的 alias 传入；实际上游型号由 UpstreamModel 单独记录。
	Model string
	// 执行操作类别，如 chat 或官方端点动作，用于日志及用量分类。
	CallType string
	// 成功或失败分类，用于日志筛选；精确响应码见 HTTPStatus。
	Status string
	// 最终返回客户端的 HTTP 状态码；0 表示未保存该事实。
	HTTPStatus int
	// 归一化输入 token 数，由上游用量产生，无实测时为 0。
	PromptTokens int
	// 归一化输出 token 数，由上游用量产生，无实测时为 0。
	CompletionTokens int
	// 持久化费用，单位沿用平台账单币种；零费用调用也保留日志。
	Cost float64
	// 从请求开始到结束的耗时，单位毫秒，不等同于首字延迟。
	DurationMS int
	// 受提示词存储及脱敏规则控制的请求正文快照。
	RequestBody string
	// 受提示词存储及脱敏规则控制的响应正文快照。
	ResponseBody string
	// 失败的原始错误正文，供详情诊断；成功为空，沿用现有脱敏规则。
	Error string
	// 已脱敏的上游头和原始统计 JSON，独立于提示词存储。
	UpstreamResponse string
	// 网关方法、路径、脱敏请求头等元数据 JSON，供详情诊断。
	ProxyRequest string
	// 请求结束时间，未取得结束事实时为空或零值，由写库逻辑处理。
	EndedAt time.Time
	// 流请求的首字节或非流请求的完整响应耗时，单位毫秒；nil 表示没有测量。
	TTFTMs *int
	// 是否返回网关响应缓存；命中仍记录用量但推理费用为 0，不提交上游成功偏好。
	CacheHit bool
	// 调用密钥哈希快照，用于定位身份，不是明文密钥。
	KeyHash string
	// 调用时密钥显示名称快照，名称变化后不重写旧日志。
	KeyAlias string
	// 调用时团队显示名称快照，供日志展示。
	TeamAlias string
	// 执行供应商/适配器标识，区别于具体供应商连接 ID。
	Provider string
	// 上游报告的缓存输入 token 数；nil 表示没有统计，0 表示明确没有命中。
	CachedTokens *int
	// 明确、继承、推断或生成的会话标识，用于响应头和授权范围内日志归组；不是响应 ID，不授予历史读取权限。
	SessionID string
	// SessionID 的识别来源，不是供应商来源或路由策略。由 PlanRoute 生成，经 CallNote 写入日志。
	// explicit：标准 X-Session-Id/正文 session_id；parent：授权父响应继承；
	// cache_key：prompt_cache_key 哈希；prompt：稳定首轮内容指纹；generated：随机 UUID。
	// cache_key/prompt 在日志中标为推断；不参与权限判断。空值表示未记录来源；JSON 和入库行为由当前载体字段标签决定。
	SessionSource string
	// 网关响应缓存键，与会话指纹、原生 prompt_cache_key 及偏好缓存键相互独立。
	CacheKey string
	// 护栏检查结果 JSON 数组，日志抽屉读取；空值表示未记录检查结果。
	Guardrail string
	// 客户端备注随调用快照持久化，不受提示词保存开关影响。
	Label string
	// 实际费率和计费数量 JSON 快照，读取日志不按现价重算；空值表示未定价。
	PriceSnapshot string
}

// RecordUsage writes the event, its stored request/response, the daily roll-up and the live spend counters in one transaction. The batch is idempotent on request_id. A retried flush, or a second gateway
//
//	process replaying the same Redis entry, inserts the event once and therefore increments everything else once. The existence check and the increments share the transaction so a partial replay cannot double-count.
//
// 参数 ctx（context.Context）：上下文，取消时停止；records（[]UsageRecord）：一次调用的用量，含 token、费用、密钥和团队，准备写入用量表。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：dataplane/live.go、gateway/spend.go、gateway/wire.go
// 测试：activity_http_test.go、prompt_log_test.go、usage_idempotency_test.go
func (db *DB) RecordUsage(ctx context.Context, records []UsageRecord) error {
	logTraceOnceUsage.Do(func() { util.Trace("enter iam.RecordUsage") })

	if len(records) == 0 {
		return nil
	}
	records = append([]UsageRecord(nil), records...)
	for i := range records {
		r := &records[i]
		if r.RequestID == "" || r.Cost < 0 || math.IsNaN(r.Cost) || math.IsInf(r.Cost, 0) || r.PromptTokens < 0 || r.CompletionTokens < 0 || (r.CachedTokens != nil && *r.CachedTokens < 0) {
			return fmt.Errorf("usage record %d: invalid identity, cost or token count", i)
		}
		r.TS = stamp(r.TS)
	}
	return db.tx(ctx, func(s *xorm.Session) error {
		monitor := map[monitorKey]monitorDelta{}
		for _, r := range records {
			if err := settleBudgetHold(s, r); err != nil {
				return err
			}
			fresh, err := insertEvent(s, r)
			if err != nil {
				return err
			}
			if !fresh {
				continue
			}
			accumulateMonitor(monitor, r, 1)
			if err := putRequestLog(s, r); err != nil {
				return err
			}
			if err := bumpDaily(s, r); err != nil {
				return err
			}
			if err := addScopeSpend(s, r); err != nil {
				return err
			}
		}
		return flushMonitor(s, monitor)
	})
}

// insertEvent 插入一条带归属、计量和明确推理强度的不可变账单事实。
// 参数 s 为调用方事务，r 为完整调用快照；返回是否新增及数据库错误，重复 request_id 返回 false、nil。
// RecordUsage 的事务路径调用；ON CONFLICT 防止重放中断整批并导致重复计费，正文由独立日志写入。
// 推理强度只保存采集值，空值保持未知；失败由上层事务回滚，费用和现存记录均不在此重算。
func insertEvent(s *xorm.Session, r UsageRecord) (bool, error) {
	var ended any
	if !r.EndedAt.IsZero() {
		ended = r.EndedAt.UTC()
	}
	res, err := s.Exec(`INSERT INTO usage_events
        (request_id, ts, key_id, owner_type, user_id, team_id, project_id, organization_id,
		 model, call_type, status, http_status, prompt_tokens, completion_tokens, cost, duration_ms,
         ended_at, ttft_ms, cache_hit, key_hash, key_alias, team_alias, provider,
         cached_tokens, session_id, session_source, cache_key, guardrail, price_snapshot, supplier, upstream_model, streamed, inbound_endpoint, outbound_endpoint, label, api_base, reasoning_effort)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT (request_id) DO NOTHING`,
		r.RequestID, stamp(r.TS), r.KeyID, ownerType(r), r.UserID, r.TeamID, r.ProjectID,
		r.OrganizationID, r.Model, r.CallType, status(r), r.HTTPStatus,
		r.PromptTokens, r.CompletionTokens, r.Cost, r.DurationMS,
		ended, r.TTFTMs, r.CacheHit, r.KeyHash, r.KeyAlias, r.TeamAlias, r.Provider,
		r.CachedTokens, r.SessionID, r.SessionSource, r.CacheKey, r.Guardrail, r.PriceSnapshot, r.Supplier, r.UpstreamModel, r.Streamed, r.InboundEndpoint, r.OutboundEndpoint, r.Label, r.APIBase, r.ReasoningEffort)
	if err != nil {
		return false, mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, mapErr(err)
	}
	return n > 0, nil
}

// putRequestLog 保存调用正文及已脱敏的上游诊断；即使正文关闭存储仍保留日志行和独立诊断。
// 参数 s（*xorm.Session）：当前事务里的数据库会话。调用方负责提交，这里不关闭它；r（UsageRecord）：一次调用的用量，含 token、费用、密钥和团队，准备写入用量表。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：普通用量与异步任务创建事务；失败由调用方回滚。
// 测试：task_usage_test.go、cmd/regression/upstream_diagnostics_test.go 验证真实落库。
func putRequestLog(s *xorm.Session, r UsageRecord) error {
	row := RequestLog{RequestID: r.RequestID, RequestBody: r.RequestBody,
		ResponseBody: r.ResponseBody, Error: r.Error, ProxyRequest: r.ProxyRequest, UpstreamResponse: r.UpstreamResponse}
	if _, err := s.Insert(&row); err != nil {
		return mapErr(err)
	}
	return nil
}

// bumpDaily adds one request to the roll-up row for its day and ownership. The conflict target is the full primary key, so the increment is a single statement that cannot lose an update to a concurrentflusher.
// 参数 s（*xorm.Session）：当前事务里的数据库会话。调用方负责提交，这里不关闭它；r（UsageRecord）：一次调用的用量，含 token、费用、密钥和团队，准备写入用量表。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：仅在 usage.go 内使用
// 测试：无直接单测
func bumpDaily(s *xorm.Session, r UsageRecord) error {
	_, err := s.Exec(`INSERT INTO usage_daily
        (day, organization_id, team_id, project_id, user_id, key_id, owner_type, model,
         requests, prompt_tokens, completion_tokens, cost)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
        ON CONFLICT (day, organization_id, team_id, project_id, user_id, key_id, owner_type, model)
        DO UPDATE SET requests = usage_daily.requests + 1,
                      prompt_tokens = usage_daily.prompt_tokens + EXCLUDED.prompt_tokens,
                      completion_tokens = usage_daily.completion_tokens + EXCLUDED.completion_tokens,
                      cost = usage_daily.cost + EXCLUDED.cost`,
		day(r.TS), r.OrganizationID, r.TeamID, r.ProjectID, r.UserID, r.KeyID, ownerType(r), r.Model,
		r.PromptTokens, r.CompletionTokens, r.Cost)
	return mapErr(err)
}

// addScopeSpend 在账单同一事务更新准入读取的消费计数；回滚不遗留虚假扣款，晚到旧周期费用不占当前组织预算。
// 参数 s（*xorm.Session）：当前事务里的数据库会话。调用方负责提交，这里不关闭它；r（UsageRecord）：一次调用的用量，含 token、费用、密钥和团队，准备写入用量表。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：仅在 usage.go 内使用
// 测试：无直接单测
func addScopeSpend(s *xorm.Session, r UsageRecord) error {
	if r.Cost == 0 {
		return nil
	}
	// Stable scope order within an event. Differently ordered batches can still
	// deadlock; callers must retry the entire transaction on failure.
	for _, t := range []struct{ table, id string }{
		{"organizations", r.OrganizationID}, {"teams", r.TeamID}, {"projects", r.ProjectID},
		{"users", r.UserID}, {"api_keys", r.KeyID},
	} {
		if t.id == "" {
			continue
		}
		// 迟到或重放的旧周期事件仍写历史明细及其他层级，但不再次占组织当前周期。
		query := "UPDATE " + t.table + " SET spend = spend + ? WHERE id = ?"
		args := []any{r.Cost, t.id}
		if t.table == "organizations" {
			query += " AND (budget_period_start IS NULL OR budget_period_start <= ?)"
			args = append(args, r.TS)
		}
		if _, err := s.Exec(append([]any{query}, args...)...); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// stamp returns the event time in UTC, using now when the caller had none.
// 参数 t（time.Time）：时间点。零值表示调用方没有提供时间。
// 返回 time.Time（time.Time）：解析出的时间。
// 调用：仅在 usage.go 内使用
// 测试：无直接单测
func stamp(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

// day truncates an event time to the UTC calendar day the roll-up keys on.
// 参数 t（time.Time）：时间点。零值表示调用方没有提供时间。
// 返回 time.Time（time.Time）：解析出的时间。
// 调用：仅在 usage.go 内使用
// 测试：无直接单测
func day(t time.Time) time.Time {
	s := stamp(t)
	return time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.UTC)
}

// ownerType 为日志确定来源；未显式填写时，有 Key 的请求属于个人，否则属于会话。
// 参数 r（UsageRecord）：一次调用的用量，含 token、费用、密钥和团队，准备写入用量表。
// 返回 string（string）：个人或会话来源；仅转换输入，不写数据库，不创建业务账号。
// 调用：仅在 usage.go 内使用
// 测试：无直接单测
func ownerType(r UsageRecord) string {
	if r.OwnerType == "" {
		if r.KeyID != "" {
			return OwnerPersonal
		}
		return "session"
	}
	return r.OwnerType
}

// status is the stored outcome, defaulting to success.
// 参数 r（UsageRecord）：一次调用的用量，含 token、费用、密钥和团队，准备写入用量表。
// 返回 string（string）：库存的结果状态。空值按 success 处理。
// 调用：仅在 usage.go 内使用
// 测试：无直接单测
func status(r UsageRecord) string {
	if r.Status == "" {
		return "success"
	}
	return r.Status
}
