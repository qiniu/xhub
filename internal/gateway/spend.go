// spend.go writes one usage row for every finished inference call, chat and
// bypass included. recordSpend is the only writer. It copies the exchange
// (headers, body, response) and the call note (provider, TTFT, session,
// session) that the handler stored under the call id, then deletes them.
//
// Configured disk spool synchronously saves complete usage and commits PostgreSQL.
// Without it, Redis queues hot charges when available; otherwise PostgreSQL is written directly.
// A cache hit keeps the token counts and forces the billed amount to zero.
// A status at or above 400 keeps the row for the log and forces the billed
// amount to zero as well.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/gateway/billing"
	"github.com/sunqirui1987/xhub/internal/gateway/prefs"
	"github.com/sunqirui1987/xhub/internal/gateway/requestlog"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	"github.com/sunqirui1987/xhub/internal/util"
)

var logTraceOnceSpend sync.Once

// incBusy increments the in-process concurrency count for a deployment.
// 参数 id（string）：部署 id，形状是 api_base|model。空串时计数没有对应的部署。
// 返回：无。该部署的进程内在途数加一。
// 调用：gateway/wire.go
// 测试：无直接单测
func (s *Server) incBusy(id string) {
	logTraceOnceSpend.Do(func() { util.Trace("enter gateway.incBusy") })

	s.mu.Lock()
	s.Busy[id]++
	s.mu.Unlock()
}

// decBusy decrements the in-process concurrency count for a deployment.
// 参数 id（string）：部署 id，形状是 api_base|model。必须和 incBusy 用同一个 id。
// 返回：无。该部署的进程内在途数减一。必须和 incBusy 成对。
// 调用：gateway/wire.go
// 测试：无直接单测
func (s *Server) decBusy(id string) {
	s.mu.Lock()
	s.Busy[id]--
	s.mu.Unlock()
}

// setChatHeaders sets response headers such as the model name, spend, and latency.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态和正文写在这里；p（*auth.Principal）：已经鉴权的调用方，含用户、团队、密钥哈希和别名；alias（string）：对外模型名；apiBase（string）：上游根地址，末尾斜杠会被去掉再拼路径。
// 返回：无。状态码和正文写进调用方的响应。
// 调用：gateway/engine.go、gateway/wire.go
// 测试：无直接单测
func (s *Server) setChatHeaders(w http.ResponseWriter, p *auth.Principal, alias, apiBase string) {
	w.Header().Set("x-litellm-model-name", alias)
	w.Header().Set("x-litellm-model-api-base", apiBase)
	w.Header().Set("x-litellm-version", Version)
	if p.Key != nil {
		if p.Key.TPMLimit != nil {
			w.Header().Set("x-litellm-key-tpm-limit", strconv.Itoa(*p.Key.TPMLimit))
		}
		if p.Key.RPMLimit != nil {
			w.Header().Set("x-litellm-key-rpm-limit", strconv.Itoa(*p.Key.RPMLimit))
		}
		if p.Key.MaxBudget != nil {
			w.Header().Set("x-litellm-key-max-budget", catalog.Format(*p.Key.MaxBudget))
		}
		// 流式响应在落账前发送头，也必须包含同一 KeyID 下的历史待结算费用。
		hot, err := s.Live.HotKeySpend(p.KeyID)
		if err == nil {
			w.Header().Set("x-litellm-key-spend", catalog.Format(p.Key.Spend+hot))
		}
	}
}

// recordSpend 计算费用响应头并记录用量；配置磁盘队列时先同步落盘/PG，否则沿用Redis排队或直接PG。
// 异步轮询消费临时元数据后更新原请求，只有创建增加请求计数，首次结算增加费用。
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态和正文写在这里；p（*auth.Principal）：已经鉴权的调用方，含用户、团队、密钥哈希和别名；callID（string）：这一次调用的 id，用来把请求、响应和用量记在同一行；alias（string）：对外模型名；op（string）：操作名，例如 chat；usage（map[string]any）：用量对象。字段可能是 prompt_tokens，也可能是 input_tokens；start（time.Time）：时间范围的起点。零值表示不限制开始；cacheHit（bool）：为真时走缓存命中这一支。为假时保持原来的路径；status（int）：HTTP 状态码；depID（string）：部署 id。空串表示当前没有钉住的部署。
// 返回：无。状态码和正文写进调用方的响应。
// 调用：gateway/wire.go
// 测试：无直接单测
func (s *Server) recordSpend(w http.ResponseWriter, p *auth.Principal, callID, alias, op string, usage map[string]any, start time.Time, cacheHit bool, status int, depID string) {
	// 完整快照由本路径负责，即使存储失败也不能由外层生成内容不同的替代账单。
	markUsageRecorded(w)
	if usage == nil {
		usage = map[string]any{}
	}
	billed := catalog.NormalizeUsage(usage)
	tokenOnly := s.tokenOnlyBudget(callID)
	if tokenOnly {
		billed = billing.SearchTokenUsage(usage)
	}
	pt, ct := billed.PromptTokens, billed.CompletionTokens
	// The start instant decides the billing window. A window-priced model costs
	// twice as much inside its peak hours, so this is the difference between
	// charging the published rate and charging half of it.
	total, in, out, okc, charge := billing.CallCost(s, alias, depID, billed, start)
	// 软预算同样冻结价格，保留缓存TTL与分档核费，不能退回简化费率造成少扣。
	softActive := false
	if frozen, active, valid := s.softBudgetCharge(callID, usage); active {
		softActive = true
		charge, okc = frozen, valid
		total, in, out = charge.Total, charge.Input, charge.Output
	}
	// A cache hit is a request fact, but it is not a second upstream generation.
	// Keep the token metadata for observability while making the billable delta
	// explicitly zero. This must happen before both the Redis and PostgreSQL
	// persistence paths so the two paths cannot disagree.
	if cacheHit {
		total, in, out = 0, 0, 0
		charge = catalog.Charge{}
	}
	if status >= 400 {
		total, in, out = 0, 0, 0
		charge = catalog.Charge{}
	}
	var budgetMetadata map[string]any
	if softActive {
		budgetMetadata = map[string]any{"state": "soft", "billing_scope": "model_usage", "guarantee": "soft"}
	}
	if tokenOnly {
		budgetMetadata = map[string]any{"state": "token_only", "billing_scope": "tokens_only", "tool_costs": "excluded", "guarantee": "soft"}
	}

	// LiteLLM stores response_cost or 0.0. Unknown models still get a row, with spend 0.
	spend := 0.0
	if okc {
		spend = total
		w.Header().Set("x-litellm-response-cost", catalog.Format(total))
		w.Header().Set("x-litellm-response-cost-original", catalog.Format(total))
		w.Header().Set("x-litellm-response-cost-input", catalog.Format(in))
		w.Header().Set("x-litellm-response-cost-output", catalog.Format(out))
		if p != nil && p.Key != nil {
			shown := p.Key.Spend + total
			showSpend := true
			if s.Live != nil {
				// 轮换不改变 KeyID；不可读时省略累计值，避免向客户端伪报零热费用。
				hot, err := s.Live.HotKeySpend(p.KeyID)
				showSpend = err == nil
				shown += hot
			}
			if showSpend {
				w.Header().Set("x-litellm-key-spend", catalog.Format(shown))
			} else {
				w.Header().Del("x-litellm-key-spend")
			}
		}
	}
	hash := ""
	ownerType := ""
	teamID, userID, orgID, projectID := "", "", "", ""
	if p != nil {
		hash = p.Hash
		ownerType = p.OwnerType
		userID = p.UserID
		if p.Key != nil {
			teamID, projectID = p.BillingTeamID, deref(p.Key.ProjectID)
			// A key carries no organization of its own; its team's is the
			// billing scope above it, snapshotted onto the usage row.
			orgID = p.BillingOrgID
			if p.Key.UserID != nil {
				userID = *p.Key.UserID
			}
		} else if p.Kind == authz.KindSession && userID != "" && s.IAM != nil {
			ownerType, teamID, orgID = iam.OwnerSession, p.BillingTeamID, p.BillingOrgID
		}
	}
	end := time.Now()
	tokens := pt + ct
	if tokens == 0 {
		tokens = asInt(usage["total_tokens"])
	}
	ex := s.takeExchange(callID)
	callType := strings.TrimSpace(op)
	if callType == "" {
		callType = "chat"
	}
	rowStatus := "success"
	if status >= 400 {
		rowStatus = "error"
		// Failed gateway requests are observations, not billable completions.
		// Keep the response usage for diagnostics but never turn an upstream 4xx/5xx
		// into spend or route-usage.
		spend = 0
		tokens = 0
	}
	note := s.takeNote(callID)
	outbound := ""
	if note.Upstream != nil {
		outbound = note.Upstream.Endpoint
		// 所有协议统一按首个 HTTP 响应字节到达时间计算 TTFT。
		if !note.Upstream.FirstByteAt.IsZero() {
			ms := int(note.Upstream.FirstByteAt.Sub(start).Milliseconds())
			if ms >= 0 {
				note.TTFTMs = &ms
			}
		}
	}
	guardrail := s.takeGuardrail(callID)
	if !cacheHit && tokens > 0 && !note.SkipRouteUsage {
		s.noteUsage(depID, tokens)
	}
	keyID := ""
	if p != nil {
		keyID = p.KeyID
	}
	priceSnapshot := catalog.SnapshotUsage(charge, billed, okc)
	if tokenOnly {
		pricingStatus := "unpriced"
		if okc {
			pricingStatus = "priced"
		}
		rawSnapshot, _ := json.Marshal(catalog.PriceSnapshot{Window: charge.Window, Applied: charge.Applied, PricingStatus: pricingStatus, Usage: &billed})
		priceSnapshot = string(rawSnapshot)
	}
	row := live.SpendLog{
		Label: ex.label, InboundEndpoint: ex.inbound, OutboundEndpoint: outbound, ReasoningEffort: ex.reasoningEffort,
		RequestID: callID, CallType: callType, Model: alias, APIKey: hash,
		KeyID:  keyID,
		Prompt: pt, Completion: ct, Spend: spend, SpendValid: true,
		Start: start.UTC().Format(time.RFC3339Nano), End: end.UTC().Format(time.RFC3339Nano),
		CacheHit: cacheHit || note.CacheHit, Status: rowStatus, HTTPStatus: status, OwnerType: ownerType,
		TeamID: teamID, UserID: userID, OrgID: orgID, ProjectID: projectID,
		Messages: ex.messages, Response: ex.response, ProxyRequest: ex.proxy,
		UpstreamResponse: requestlog.DiagnosticJSON(note.Upstream, usage),
		Streamed:         note.Streamed, TTFTMs: note.TTFTMs, Provider: note.Provider, CacheKey: note.CacheKey,
		SessionID: note.SessionID, SessionSource: note.SessionSource, CachedTokens: cachedColumn(usage),
		Guardrail:           guardrail,
		PriceSnapshot:       budgetPriceSnapshot(priceSnapshot, budgetMetadata),
		BudgetReservationID: "",
	}
	// 快照保存连接名称而非仅协议名；同协议的多家供应商不会合并。
	row.Supplier, row.APIBase, row.UpstreamModel = note.Supplier, note.APIBase, note.UpstreamModel
	if dep, found := s.FindDeployment(depID); found && !note.UpstreamSelected {
		// 官方任务等旧调用方尚未附带快照时，解析命名凭证后保存基础地址。
		if resolved, err := s.AttachCredential(dep); err == nil {
			dep = resolved
		}
		row.Supplier, row.UpstreamModel = dep.MonitorIdentity()
		row.APIBase = dep.LogAPIBase()
	}
	if status >= 400 {
		// 失败正文是排障证据，不能受提示词保存开关影响。请求正文仍按原开关处理。
		row.Error = ex.rawResponse
		row.Response = ex.rawResponse
	}
	if note.SettlementID != "" {
		row.RequestID = note.SettlementID
	}
	if p != nil && p.Key != nil {
		row.KeyHash = p.Key.TokenHash
		row.KeyAlias = p.Key.Name
	}
	if teamID != "" && s.IAM != nil {
		if team, err := s.IAM.GetTeam(context.Background(), teamID); err == nil && team != nil {
			row.TeamAlias = team.Name
		}
	}
	if note.TaskRequestID != "" {
		// 生命周期直接事务持久化，避免 Redis 首个不可变事件吞掉后续状态和结算。
		// 原请求的归属是更新授权边界，费用仅在数据库锁内首次成功结算时累加。
		row.RequestID, row.Status = note.TaskRequestID, note.TaskStatus
		// 任务失败可能由 HTTP 200 的查询响应表达，不能只按 HTTP 状态判断。
		// 原始正文独立于提示词开关保存，供详情 error 和 response 回溯供应商结果。
		if row.Status == "failed" {
			row.Error = ex.rawResponse
			row.Response = ex.rawResponse
		}
		rec := usageFromSpend(row, spend, ex.messages, row.Response, ex.proxy, start, end)
		if s.IAM != nil {
			if err := s.IAM.RecordTaskUsage(context.Background(), rec, note.TaskInitial, note.TaskSettlement); err != nil {
				util.Error("task usage persistence failed request=%s err=%v", row.RequestID, err)
			}
		}
		return
	}
	// 磁盘模式先保存完整最终账单再同步结算，不加入新的Redis热队列；旧队列仍由Flush清算。
	if s.usageSpoolEnabled {
		s.persistSpend(row, spend, ex, start, end)
		return
	}
	if s.Live != nil && s.Live.EnqueueSpend(row) == nil {
		return
	}
	s.persistSpend(row, spend, ex, start, end)
}

// usageOf answers what the upstream reported, for the parts of the usage row
// that are not the charge: the cached-token column the console displays.
//
// The billable quantities come from catalog.NormalizeUsage, which is also what
// the charge is computed from. This used to be a second reader of the same
// object, and the two disagreed: the billing path resolved the Anthropic shape
// (a cache read beside a prompt count that excludes it) while the stored column
// read only prompt_tokens_details, so a row could be charged for 800 cached
// tokens and display 0.
//
// 参数 usage（map[string]any）：上游报出来的用量对象。
// 返回 *int（*int）：要写进 cached_tokens 列的命中数。上游没报这个数时为 nil，不写这一列。
// 调用：recordSpend。
// 测试：billing/cost_test.go
func cachedColumn(usage map[string]any) *int {
	if usage == nil {
		return nil
	}
	// Absent and zero are different here. Zero written into the column says the
	// upstream reported a cache and nothing hit it; nil says it did not report
	// one, and the console shows that as no figure rather than as a miss.
	if !reportsCachedTokens(usage) {
		return nil
	}
	n := catalog.NormalizeUsage(usage).CachedTokens
	return &n
}

// reportsCachedTokens answers whether the usage object carries a cached-prompt
// count at all, in any of the spellings the providers use.
// 参数 usage（map[string]any）：上游报出来的用量对象。
// 返回 bool（bool）：上游报了这个数时为真。
// 调用：cachedColumn。
// 测试：billing/cost_test.go
func reportsCachedTokens(usage map[string]any) bool {
	for _, key := range []string{"cached_tokens", "cache_read_input_tokens", "cache_read_tokens"} {
		if _, ok := usage[key]; ok {
			return true
		}
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		if details, ok := usage[key].(map[string]any); ok {
			if _, ok := details["cached_tokens"]; ok {
				return true
			}
		}
	}
	return false
}

// persistSpend 先独立持久化账单快照再立即结算；无 Redis 或入队失败的调用使用此路径。
// 参数 row（live.SpendLog）：从用量或目录读出的SpendLog；spend（float64）：这一行要累加的费用，单位是美元；ex（promptExchange）：persist花费使用的promptExchange；start（time.Time）：时间范围的起点。零值表示不限制开始；end（time.Time）：时间范围的终点。零值表示直到现在。
// 返回：无。启用磁盘队列时先落独立快照，结算失败由后台Flush/新实例恢复；所有持久介质都失败时明确记录未持久化。没配库时不写。
// 调用：仅在 spend.go 内使用
// 测试：log_completeness_test.go、TestPersistSpendKeepsFailureResponseWithoutPrompts。
func (s *Server) persistSpend(row live.SpendLog, spend float64, ex promptExchange, start, end time.Time) {
	if s.IAM == nil {
		return
	}
	// The event, its stored bodies, the daily roll-up and all five billing scopes
	// commit together and deduplicate by request_id.
	// 失败正文已经由 recordSpend 放入 row.Response，不能再用受提示词开关控制的
	// ex.response 覆盖，否则直写数据库和 Redis 队列会保存不同的排障信息。
	response := ex.response
	if row.Status == "error" || row.Status == "failed" || row.Status == "failure" || row.HTTPStatus >= 400 {
		response = row.Response
	}
	rec := usageFromSpend(row, spend, ex.messages, response, ex.proxy, start, end)
	if durable, err := s.persistUsageSnapshot(rec); err != nil {
		// 已准入后磁盘突然失效且PG也不可写时尽力使用Redis，不能只打印日志后丢弃完整载荷。
		if !durable && s.Live != nil {
			durable = s.Live.EnqueueSpend(row) == nil
		}
		util.Error("persist spend failed request=%s durable=%t: %v", rec.RequestID, durable, err)
	}
}

// sessionLogBinding marks a console call as that person's own log. When they belong to one team, the row is filed there so the team and its organization administrator can read it. More than one membership leaves the team blank: the caller still sees the row, and a guess would file it under the wrong team.
// 参数 ownerType（string）：会话日志Binding使用的归属类型。空串表示调用方没有提供这项；teamID（string）：团队 id。空串表示没有指定团队；orgID（string）：组织 id。空串表示不按组织过滤；memberships（[]iam.Membership）：会话日志Binding使用的Membership。
// 返回 string（string）：归属类型。入参为空时按个人密钥；string（string）：要写入用量行的团队 id。只属于一个团队且入参为空时用那个团队；string（string）：要写入用量行的组织 id。跟团队一起补上，已经有值时不改。
// 调用：仅在 spend.go 内使用
// 测试：spend_session_test.go
func sessionLogBinding(ownerType, teamID, orgID string, memberships []iam.Membership) (string, string, string) {
	if ownerType == "" {
		ownerType = iam.OwnerPersonal
	}
	if teamID == "" && len(memberships) == 1 {
		teamID = memberships[0].TeamID
		if orgID == "" {
			orgID = memberships[0].OrganizationID
		}
	}
	return ownerType, teamID, orgID
}

// usageFromSpend 把一条热花费和暂存的请求、响应正文收成可以写入 PostgreSQL 的用量行。
// 参数 row（live.SpendLog）：Redis 队列里的花费日志，含密钥、团队、模型和 token；spend（float64）：这条日志的美元费用；messages（string）：已打码的请求 JSON。没开正文保存时为空串；response（string）：已打码的响应 JSON。没开正文保存时为空串；proxy（string）：发给上游的请求 JSON。没开正文保存时为空串；start（time.Time）：请求开始时间；end（time.Time）：请求结束时间，用来算耗时。
// 返回 UsageRecord（iam.UsageRecord）：准备写入 usage_events 的一行。费用、正文和归属都来自上面的参数。
// 调用：persistSpend。
// 测试：无直接单测
func usageFromSpend(row live.SpendLog, spend float64, messages, response, proxy string, start, end time.Time) iam.UsageRecord {
	return iam.UsageRecord{
		BudgetReservationID: row.BudgetReservationID,
		InboundEndpoint:     row.InboundEndpoint, OutboundEndpoint: row.OutboundEndpoint,
		APIBase: row.APIBase, Supplier: row.Supplier, UpstreamModel: row.UpstreamModel, Streamed: row.Streamed,
		RequestID: row.RequestID, TS: start, KeyID: row.KeyID, OwnerType: row.OwnerType,
		UserID: row.UserID, TeamID: row.TeamID, ProjectID: row.ProjectID, OrganizationID: row.OrgID,
		Model: row.Model, CallType: row.CallType, Status: row.Status, HTTPStatus: row.HTTPStatus,
		PromptTokens: row.Prompt, CompletionTokens: row.Completion, Cost: spend,
		DurationMS:  int(end.Sub(start).Milliseconds()),
		RequestBody: messages, ResponseBody: response, Error: row.Error, ProxyRequest: proxy,
		UpstreamResponse: row.UpstreamResponse,
		EndedAt:          end, TTFTMs: row.TTFTMs, CacheHit: row.CacheHit,
		KeyHash: row.KeyHash, KeyAlias: row.KeyAlias, TeamAlias: row.TeamAlias,
		Provider: row.Provider, CachedTokens: row.CachedTokens,
		SessionID: row.SessionID, SessionSource: row.SessionSource, CacheKey: row.CacheKey, Guardrail: row.Guardrail,
		PriceSnapshot: row.PriceSnapshot, Label: row.Label, ReasoningEffort: row.ReasoningEffort,
	}
}

// deref 读取可选字符串。nil 指针当成空串，避免把没有填写的列写成 "<nil>"。
// 参数 s（*string）：用量行上的可选文本，例如团队别名。nil 表示这一列没有值。
// 返回 string（string）：指针指向的字符串。指针为 nil 时为空串。
// 调用：仅在 spend.go 内使用。
// 测试：无直接单测
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// promptExchange is the request and response saved on one spend log. Empty strings mean prompt storage was off.
type promptExchange struct {
	// 只保存请求明确指定的推理强度，不受提示词保存开关控制。
	reasoningEffort string
	// 入站路径独立于正文保存开关。
	label    string
	inbound  string
	messages string
	response string
	proxy    string
	// rawResponse 暂存上游原始响应；仅失败请求会持久化，因此不受提示词开关控制。
	rawResponse string
}

// promptsEnabled reports whether new spend logs should keep the request and response. The YAML flag wins when it is set. A database override can turn the same key on later.
// 参数：无。
// 返回 bool（bool）：新的花费日志要保存请求和响应正文时返回真。YAML 已设置时以 YAML 为准。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func (s *Server) promptsEnabled() bool {
	if s == nil || s.Cfg == nil {
		return false
	}
	if s.Cfg.GeneralSettings.StorePromptsInSpendLogs {
		return true
	}
	if s.Store == nil {
		return false
	}
	switch v := prefs.MergedGeneral(s)["store_prompts_in_spend_logs"].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

// rememberExchange 暂存本次调用的路径、备注、明确推理强度和正文，供 recordSpend 关联日志。
// 参数 callID 为日志关联标识，r 为入站请求，reqBody 和 respBody 分别为原始请求和响应字节。
// 返回：无。请求与成功响应受提示词开关控制；失败候选响应始终暂存到 RecordSpend 判定状态。
// 调用：gateway/wire.go
// 边界：非法编码或过长备注忽略；路径、备注和有长度限制的推理强度不受正文保存开关影响，不修改请求内容。
func (s *Server) rememberExchange(callID string, r *http.Request, reqBody, respBody []byte) {
	if s == nil || callID == "" {
		return
	}
	ex := promptExchange{rawResponse: string(respBody)}
	if r != nil && r.URL != nil {
		ex.inbound = r.URL.EscapedPath()
		// 备注由客户端声明，只作排查线索；限制长度并解码中文，不从请求正文提取秘密。
		if raw := r.Header.Get("X-Xhub-Label"); len(raw) <= 2048 {
			if label, err := url.QueryUnescape(raw); err == nil && len(label) <= 1024 {
				ex.label = label
			}
		}
	}
	// 只提取协议元数据；即使不保留正文，也能排查本次请求的强度设置。
	ex.reasoningEffort = requestlog.ReasoningEffort(ex.inbound, reqBody)
	if s.promptsEnabled() {
		ex.messages, ex.response, ex.proxy = requestlog.Encode(r, reqBody, respBody)
	}
	s.mu.Lock()
	if s.exchanges == nil {
		s.exchanges = map[string]promptExchange{}
	}
	s.exchanges[callID] = ex
	s.mu.Unlock()
}

// 取出并删掉这次调用暂存的请求和响应，供记用量时写入日志。
// 参数 callID（string）：这一次调用的 id，用来把请求、响应和用量记在同一行。
// 返回 promptExchange（promptExchange）：取出并删掉这次调用暂存的请求和响应，供记用量时写入日志。没有命中时为零值。
// 调用：仅在 spend.go 内使用
// 测试：无直接单测
func (s *Server) takeExchange(callID string) promptExchange {
	if s == nil {
		return promptExchange{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ex := s.exchanges[callID]
	delete(s.exchanges, callID)
	return ex
}

// writeCacheHit returns a cached body and records a cache-hit spend log.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态和正文写在这里；p（*auth.Principal）：已经鉴权的调用方，含用户、团队、密钥哈希和别名；callID（string）：这一次调用的 id，用来把请求、响应和用量记在同一行；alias（string）：对外模型名；ck（string）：写入缓存命中使用的ck。空串表示调用方没有提供这项；op（string）：操作名，例如 chat；hit（[]byte）：缓存命中的响应正文；start（time.Time）：时间范围的起点。零值表示不限制开始。
// 返回：无。状态码和正文写进调用方的响应。
// 调用：gateway/wire.go
// 测试：无直接单测
func (s *Server) writeCacheHit(w http.ResponseWriter, p *auth.Principal, callID, alias, ck, op string, hit []byte, start time.Time) {
	w.Header().Set("cache_hit", "true")
	w.Header().Set("x-litellm-cache-hit", "true")
	w.Header().Set("x-litellm-cache-key", ck)
	w.Header().Set("x-litellm-model-name", alias)
	w.Header().Set("x-litellm-version", Version)
	var parsed map[string]any
	var usage map[string]any
	if json.Unmarshal(hit, &parsed) == nil {
		usage, _ = parsed["usage"].(map[string]any)
	}
	// Always write the cache-hit request log, even when the cached response has
	// no usage object. Missing usage is different from a missing request fact;
	// recordSpend will keep the row and, because cacheHit is true, charge zero.
	s.recordSpend(w, p, callID, alias, op, usage, start, true, http.StatusOK, "")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-litellm-response-duration-ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.WriteHeader(200)
	_, _ = w.Write(hit)
}

// writeChatJSON 返回上游完整响应并记录用量，成功 JSON 对象及其真实权限范围写入响应缓存。
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态和正文写在这里；p（*auth.Principal）：已经鉴权的调用方，含用户、团队、密钥哈希和别名；callID（string）：这一次调用的 id，用来把请求、响应和用量记在同一行；alias（string）：对外模型名；ck（string）：写入对话JSON使用的ck。空串表示调用方没有提供这项；op（string）：操作名，例如 chat；provider（string）：供应商标识，例如 openai 或 volcengine；respBody（[]byte）：写入对话JSON要读的原始字节；status（int）：HTTP 状态码；start（time.Time）：时间范围的起点。零值表示不限制开始；depID（string）：部署 id。空串表示当前没有钉住的部署。
// 返回：无。状态码和正文写进调用方的响应。
// 调用：gateway/wire.go
// 测试：response_cache_test.go。
// models 为执行时通过权限检查的真实模型及组名；随正文缓存，下一次命中按当前权限复核。
func (s *Server) writeChatJSON(w http.ResponseWriter, p *auth.Principal, callID, alias, ck, op, provider string, respBody []byte, status int, start time.Time, depID string, models ...string) {
	if op == "audio_speech" {
		// Speech responses do not carry chat-token usage. Do not manufacture
		// 8/2/10 tokens: that corrupts both usage reports and billing.
		s.recordSpend(w, p, callID, alias, op, nil, start, false, status, depID)
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "audio/mpeg")
		}
		w.Header().Set("x-litellm-response-duration-ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
		w.WriteHeader(status)
		_, _ = w.Write(respBody)
		return
	}
	var parsed map[string]any
	if json.Unmarshal(respBody, &parsed) == nil && parsed != nil {
		if op == "chat" || op == "" || op == "completions" || op == "embeddings" || op == "messages" || op == "responses" || op == "moderations" || op == "videos" {
			parsed["model"] = alias
		}
		if op == "chat" || op == "" {
			if _, ok := parsed["system_fingerprint"]; !ok {
				parsed["system_fingerprint"] = nil
			}
			if _, ok := parsed["object"]; !ok {
				parsed["object"] = "chat.completion"
			}
			if _, ok := parsed["created"]; !ok {
				parsed["created"] = time.Now().UTC().Unix()
			}
		}
		if op == "embeddings" {
			if _, ok := parsed["object"]; !ok {
				parsed["object"] = "list"
			}
			if _, ok := parsed["data"]; !ok {
				parsed["data"] = []any{}
			}
		}
		if op == "completions" {
			if _, ok := parsed["object"]; !ok {
				parsed["object"] = "text_completion"
			}
		}
		usage, _ := parsed["usage"].(map[string]any)
		if usage == nil {
			if um, ok := parsed["usageMetadata"].(map[string]any); ok {
				usage = map[string]any{
					"prompt_tokens":     um["promptTokenCount"],
					"completion_tokens": um["candidatesTokenCount"],
					"total_tokens":      um["totalTokenCount"],
				}
			}
		}
		// Some providers omit usage (especially non-chat operations). Keep the
		// request log, but do not turn an unknown measurement into fake tokens.
		// Providers that do return usage are normalized below.
		if usage == nil {
			usage = map[string]any{}
		}
		s.recordSpend(w, p, callID, alias, op, usage, start, false, status, depID)
		respBody, _ = json.Marshal(parsed)
		// 成功 JSON 正文可复用；Responses 的 ID/历史属于当次执行，读写均不缓存。
		if ck != "" && op != "responses" && status >= 200 && status < 300 {
			s.Cache.Set(ck, respBody, models...)
		}
	} else {
		s.recordSpend(w, p, callID, alias, op, nil, start, false, status, depID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-litellm-response-duration-ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}
