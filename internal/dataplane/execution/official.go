// 本文件串联供应商原生创建与任务后续操作，保留原生载荷并按调用方/传输隔离任务及最终结算。
// 调用关系与边界：数据面执行编排；具体参数、返回及副作用见各方法说明。
package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	dprouting "github.com/sunqirui1987/xhub/internal/dataplane/routing"
	dptransport "github.com/sunqirui1987/xhub/internal/dataplane/transport"
	dpusage "github.com/sunqirui1987/xhub/internal/dataplane/usage"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	providerdeployment "github.com/sunqirui1987/xhub/internal/provider/deployment"
	"github.com/sunqirui1987/xhub/internal/router"
	"github.com/sunqirui1987/xhub/internal/util"
)

// ServeBypass 是供应商原生 API 的数据面入口，按已登记操作分派创建或后续任务操作。
// 参数 h 提供身份、路由、任务归属与记账能力；w、r 是客户端交互；hit 必须来自协议目录匹配，不能由客户端任意构造目标 URL。
// 返回值：无，响应和错误直接写入 w；网关在识别原生端点后调用。
// 先鉴权，最多读取 64 MiB 正文，解析 JSON 或 multipart 的路由字段；创建交给 serveBypassCreate，查询等操作交给 serveBypassFollow。
// 原生链保留供应商载荷，不进入统一对话护栏或响应缓存；错误与任务查询仍受身份、模型权限和预算约束。
// 副作用包括读取入站正文、出站 HTTP、任务归属缓存及用量日志；入站正文由 HTTP 服务生命周期关闭。
// 超限、非法载荷或未授权请求在访问上游前拒绝，出站响应正文由 ForwardOfficial 关闭。
func ServeBypass(h dpcontract.Bypass, w http.ResponseWriter, r *http.Request, hit providercontract.Hit) {
	start := time.Now()
	callID := util.CallID()
	defer h.ReleaseCall(callID)
	util.SetCallID(w, callID)
	p := h.RequireLLMPrincipal(w, r)
	if p == nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<20)+1))
	if err != nil || len(raw) > 64<<20 {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "could not read request body within 64 MiB")
		return
	}
	requestBody, err := dptransport.ParseBypassBody(raw, r.Header.Get("Content-Type"))
	if err != nil {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", err.Error())
		return
	}
	body := requestBody.Fields
	// 某些同步协议没有动作名，以登记的 ModelField 判定创建；任务 ID 则按路径/查询登记读取。
	if hit.Action.Name == "count_tokens" || hit.Action.Name == "create" || hit.Transport.ModelField != "" && hit.Action.Name == "" {
		serveBypassCreate(h, w, r, hit, p, body, raw, callID, start)
		return
	}
	id := hit.Names["id"]
	if id == "" {
		id = hit.Names["task_id"]
	}
	if id == "" {
		id = hit.Names["request_id"]
	}
	if hit.Action.TaskQuery != "" {
		id = strings.TrimSpace(r.URL.Query().Get(hit.Action.TaskQuery))
	}
	serveBypassFollow(h, w, r, hit, id, callID, start, p)
}

// serveBypassCreate 为原生创建或只读计数请求选择合法部署，转发原始协议并按动作记录用量。
// 参数 h、w、r、hit 为原生入口环境；principal 为已鉴权身份；body 是路由/计价字段投影，raw 是完整载荷；callID、start 标识本次调用与开始时间。
// 返回值：无，写入原生响应或网关错误；由 ServeBypass 的创建/计数分支调用，计数不预留或结算生成费用。
// 公开模型可来自正文或登记路径；路径固定模型必须唯一映射公开别名，歧义时要求显式选择。
// 每个候选都复核实际模型/组权限，附加服务端凭据，用登记传输替换模型并移除网关字段。
// 同步对话且非 background 才允许安全重放；异步创建遭遇网络中断、超时或 5xx 时可能已创建付费任务，因此不能盲目再创建。
// disable_fallbacks 只禁止跨目标回退；Required 父响应归属同时禁止普通重放。流写出后发生错误也立即终止。
// 成功时按调用方与传输隔离任务 ID，保存部署和创建时计费事实，改写队列查询 URL，并提交成功会话目标。
// 每次执行记录协议实测用量与出站诊断；资源清理由尝试上下文和响应正文关闭完成，远程任务生命周期由调用方管理。
func serveBypassCreate(h dpcontract.Bypass, w http.ResponseWriter, r *http.Request, hit providercontract.Hit, principal *auth.Principal, body map[string]any, raw []byte, callID string, start time.Time) {
	// 计数是原厂只读辅助操作，不预留生成预算、不提交响应归属、不提取计费用量。
	countOnly := hit.Action.Name == "count_tokens"
	requestBody, _ := dptransport.ParseBypassBody(raw, r.Header.Get("Content-Type"))
	field := hit.Transport.ModelField
	if field == "" {
		field = "model"
	}
	alias, _ := body[field].(string)
	alias = strings.TrimSpace(alias)
	if hit.Transport.Protocol == "gemini" || hit.Transport.Protocol == "vertex" {
		// Gemini 原厂正文没有 model；公开路径是路由别名，部署模型决定上游路径。
		alias = hit.Names["model"]
	}
	if alias == "" && hit.Action.Model != "" {
		// Fal 原厂正文通常没有 model，必须把固定路径型号映射回当前目录的公开别名。
		// 多个别名对应同一端点时不猜测调用方权限范围，要求显式指定 model。
		names := map[string]bool{}
		list, _ := dprouting.DropDisabled(eligible(h, hit))
		for _, dep := range list {
			names[dep.ModelName] = true
		}
		if len(names) > 1 {
			util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "multiple public aliases for this endpoint; model required")
			return
		}
		alias = hit.Transport.StripPrefix + "/" + hit.Action.Model
		for name := range names {
			alias = name
		}
	}
	if alias == "" {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", field+" required")
		return
	}
	plan := h.PlanRoute(r, alias, body, principal)
	w.Header().Set("X-Session-Id", plan.SessionID)
	if plan.SessionID != "" {
		h.AnnotateCall(callID, dpcontract.CallNote{SessionID: plan.SessionID, SessionSource: plan.SessionSource})
	}
	if plan.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", plan.Err.Error())
		return
	}
	if !h.AdmitRequest(w, r.URL.Path, principal, alias, dpusage.EstimateTokens(body)) {
		return
	}
	settings := h.RouteSettingsFor(principal)
	if settings.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "route template unavailable")
		return
	}
	baseSettings := settings
	settings = settings.ForModel(alias)
	if settings.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", settings.Err.Error())
		return
	}
	if err := router.ValidateStrategy(settings.Strategy()); err != nil {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", err.Error())
		return
	}
	list := eligible(h, hit)
	list, _ = dprouting.DropDisabled(list)
	state := h.RouteState()
	state.Allocations = settings.Policy.Shares()
	runtimePin := dprouting.RuntimePin(list, plan.Pinned)
	pool := settings.Schedule(list, alias, state, runtimePin)
	disableFallbacks, _ := body["disable_fallbacks"].(bool)
	// 原生异步任务的创建结果可能不明确，保持禁止重放；对话创建可以按模型回退。
	background, _ := body["background"].(bool)
	dialogueFallback := !background && (hit.Transport.Protocol == "openai-responses" || hit.Transport.Protocol == "openai-chat" || hit.Transport.Protocol == "anthropic-messages" || hit.Transport.Protocol == "gemini" || hit.Transport.Protocol == "vertex")
	fallbacks := dprouting.NewFallbackQueue(baseSettings, alias, plan.Required || disableFallbacks || !dialogueFallback)
	if len(pool) == 0 {
		pool, settings = fallbacks.Next("general", list, state)
	}
	pool, settings = fallbacks.SelectAffinity(&plan, pool, alias, list, state, settings)
	if plan.Required && len(pool) == 0 {
		diagnosis := dprouting.DiagnoseRoute(h.Models(), list, alias, hit.Transport.ID, settings, state, nil, false)
		dpdiagnostics.RecordEarlyFailure(w, &dpdiagnostics.UpstreamDiagnostics{Routing: diagnosis.Snapshot(settings, plan.Target.DeploymentID)})
		util.WriteTypedError(w, r.URL.Path, 409, "unavailable", "response deployment unavailable")
		return
	}
	if settings.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "fallback route unavailable")
		return
	}
	if len(pool) == 0 {
		diagnosis := dprouting.DiagnoseRoute(h.Models(), list, alias, hit.Transport.ID, settings, state, nil, false)
		dpdiagnostics.RecordEarlyFailure(w, &dpdiagnostics.UpstreamDiagnostics{Routing: diagnosis.Snapshot(settings, "")})
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "model not found: "+alias)
		return
	}
	var dep config.ModelEntry
	var respBody []byte
	var response dptransport.ForwardResponse
	var status int
	var selectedID string
	tried := false
	originalHit := hit
	for len(pool) > 0 {
		kind := "general"
		for _, rawDep := range pool {
			// 组内尝试仍保留成员身份，错误分类及后续回退使用最后尝试成员。
			fallbacks.UseDeployment(rawDep.ModelName)
			if !plan.Required {
				plan.Target = fallbacks.Target(rawDep)
			}
			// 回退目标可能是另一个组；组别名与真实成员都必须通过权限和预算检查。
			if fallbacks.PoolAlias != alias && fallbacks.PoolAlias != rawDep.ModelName && !h.EnforceIdentityLimits(w, r.URL.Path, principal, fallbacks.PoolAlias, dpusage.EstimateTokens(body)) {
				return
			}
			// 同步对话先用完整 max_attempts 重试同一供应商，不能因未配置跨模型回退而放弃重试。
			// disable_fallbacks 只控制跨模型队列；异步创建和固定响应归属不扩大重放范围。
			replayDialogue := dialogueFallback && !plan.Required
			if rawDep.ModelName != alias && !h.EnforceIdentityLimits(w, r.URL.Path, principal, rawDep.ModelName, dpusage.EstimateTokens(body)) {
				return
			}
			candidate, err := h.AttachCredential(rawDep)
			if err != nil {
				continue
			}
			// 原生入口与统一入口使用相同的连接归属检查，连接更新不能继承旧密文或游标。
			if !dprouting.BindConnection(&plan, candidate) {
				dpdiagnostics.RecordEarlyFailure(w, &dpdiagnostics.UpstreamDiagnostics{Routing: map[string]any{"required_deployment_id": plan.Target.DeploymentID, "required_deployment_reason": "connection_identity_changed_or_missing"}})
				util.WriteTypedError(w, r.URL.Path, 409, "unavailable", "response deployment unavailable")
				return
			}
			candidateHit, err := providerdeployment.ResolveHit(originalHit, candidate)
			if err != nil {
				continue
			}
			hit = candidateHit
			base, key := dptransport.BypassAuth(candidate)
			if base == "" || key == "" {
				continue
			}
			upstreamModel := providercontract.OfficialID(candidateHit.Transport.StripPrefix, candidate.ParamString("model", ""))
			payload, err := requestBody.Payload(candidateHit.Transport.ModelField, upstreamModel, candidateHit.Action.Model != "" || candidateHit.Transport.Protocol == "gemini" || candidateHit.Transport.Protocol == "vertex")
			if err != nil {
				util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", err.Error())
				return
			}
			url := dptransport.WithQuery(dptransport.BypassDeploymentURL(base, candidateHit), r)
			for attempt := 0; attempt < settings.Retries(); attempt++ {
				if tried {
					fresh, err := h.ResolveRequest(r)
					if err != nil || fresh == nil || !fresh.CanInfer() {
						util.WriteTypedError(w, r.URL.Path, 401, "invalid_api_key", "invalid api key")
						return
					}
					principal = fresh
					if !h.EnforceIdentityLimits(w, r.URL.Path, principal, alias, dpusage.EstimateTokens(body)) {
						return
					}
					if rawDep.ModelName != alias && !h.EnforceIdentityLimits(w, r.URL.Path, principal, rawDep.ModelName, dpusage.EstimateTokens(body)) {
						return
					}
				}
				if budget, ok := h.(dpcontract.BudgetHost); ok && !countOnly {
					var allowed bool
					payload, allowed = budget.PrepareBudgetAttempt(w, r, principal, callID, candidate, candidateHit.Transport.Protocol, payload, start)
					if !allowed {
						return
					}
					if !budget.MarkBudgetAttemptSubmitted(w, r, callID) {
						return
					}
				}
				tried = true
				ctx, cancel := context.WithTimeout(r.Context(), time.Duration(settings.TimeoutSeconds()*float64(time.Second)))
				response, err = dptransport.ForwardOfficial(h.HTTPClient(), r.WithContext(ctx), candidateHit.Action.Method, url, key, payload, r.Header, candidateHit.Transport, w)
				respBody, status = response.Body, response.StatusCode
				cancel()
				dep = candidate
				selectedID = router.CooldownID(rawDep)
				if err != nil {
					if response.Diagnostics == nil {
						response.Diagnostics = &dpdiagnostics.UpstreamDiagnostics{}
					}
					phase := "upstream_request"
					if response.Diagnostics.StatusCode != 0 {
						phase = "response_transfer"
					}
					response.Diagnostics.Failure = dpdiagnostics.ObserveFailure(r.Context(), phase, err, time.Since(start))
					// 原生入口同样区分客户端取消与供应商超时：只检查入站上下文，
					// 不能检查上面已释放的尝试上下文；取消记499并停止重试，不污染共享冷却。
					if r.Context().Err() != nil {
						response.Diagnostics.Failure.Kind = "request_canceled"
						h.RememberExchange(callID, r, raw, []byte("request connection canceled; initiator unknown"))
						note := dataplaneNote(hit, dep, plan.SessionID, time.Since(start))
						applyStreamTiming(&note, response, start)
						note.Upstream = response.Diagnostics
						h.AnnotateCall(callID, note)
						h.RecordSpend(w, principal, callID, alias, hit.Transport.ID+":"+hit.Action.Name, nil, start, false, 499, selectedID)
						return
					}
					if response.Streamed {
						h.RememberExchange(callID, r, raw, []byte(dpdiagnostics.SafeErr(err)))
						note := dataplaneNote(hit, dep, plan.SessionID, time.Since(start))
						applyStreamTiming(&note, response, start)
						note.Upstream = response.Diagnostics
						h.AnnotateCall(callID, note)
						h.RecordSpend(w, principal, callID, alias, hit.Transport.ID+":"+hit.Action.Name, nil, start, false, 502, selectedID)
						return
					}
					util.Error("bypass forward path=%s err=%s", r.URL.Path, dpdiagnostics.SafeErr(err))
					h.NoteFailure(router.CooldownID(rawDep), settings)
					if replayDialogue {
						continue
					}
					// 创建响应丢失时，上游可能已受理付费任务；结果不明确的创建请求不能重放。
					h.RememberExchange(callID, r, raw, []byte(dpdiagnostics.SafeErr(err)))
					note := dataplaneNote(hit, dep, plan.SessionID, time.Since(start))
					applyStreamTiming(&note, response, start)
					note.Upstream = response.Diagnostics
					h.AnnotateCall(callID, note)
					h.RecordSpend(w, principal, callID, alias, hit.Transport.ID+":"+hit.Action.Name, nil, start, false, http.StatusBadGateway, selectedID)
					util.WriteTypedError(w, r.URL.Path, 502, "upstream_error", "upstream request failed")
					return
				}
				if status >= 500 || status == http.StatusTooManyRequests {
					h.NoteFailure(router.CooldownID(rawDep), settings)
					// 服务端可能在受理付费任务后才返回错误，不能因此创建第二个任务。
					if status >= 500 && !replayDialogue {
						goto forwarded
					}
					if r.Context().Err() != nil {
						return
					}
					continue
				}
				errorKind := dprouting.FallbackKind(status, respBody)
				if errorKind != "" && !fallbacks.Disabled && len(baseSettings.ModelFallbacks[fallbacks.Current].Targets(errorKind)) > 0 {
					kind = errorKind
					goto nextFallbackModel
				}
				hit = candidateHit
				goto forwarded
			}
		}
	nextFallbackModel:
		pool, settings = fallbacks.Next(kind, list, state)
		if settings.Err != nil {
			util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "fallback route unavailable")
			return
		}
	}
	if !tried {
		util.WriteTypedError(w, r.URL.Path, 401, "upstream_auth", "This model has no upstream API key configured.")
		return
	}
forwarded:
	// 转发结束后才提交任务事实与交互：HTTP 成功、任务创建、会话偏好和可计费用量是不同事实。
	if status == 0 {
		h.RememberExchange(callID, r, raw, []byte("upstream request failed"))
		h.RecordSpend(w, principal, callID, alias, hit.Transport.ID+":"+hit.Action.Name, nil, start, false, http.StatusBadGateway, selectedID)
		util.WriteTypedError(w, r.URL.Path, 502, "upstream_error", "upstream request failed")
		return
	}
	var doc map[string]any
	_ = json.Unmarshal(respBody, &doc)
	depID := selectedID
	taskRequestID := ""
	if taskID := providercontract.ReadTaskID(doc, hit.Transport.TaskID); taskID != "" && status >= 200 && status < 300 && doc["error"] == nil && doc["detail"] == nil {
		scope := officialTaskScope(principal, hit.Transport.ID, taskID)
		if billing := hit.Transport.Billing; billing != nil {
			// 创建请求身份随调用方隔离的任务钉保存，后续查询更新同一日志。
			taskRequestID = callID
			contextBody := body
			if hit.Action.Model != "" {
				contextBody = make(map[string]any, len(body)+1)
				for k, v := range body {
					contextBody[k] = v
				}
				contextBody["model"] = hit.Action.Model
			}
			facts := billing.Context(contextBody)
			facts.StartedAt = start
			facts.RequestID = callID
			h.PinOfficialContext(scope, facts)
		}
		h.PinOfficial(scope, selectedID)
	}
	respBody = rewriteQueueURLs(hit, doc, respBody)
	if !countOnly && status >= 200 && status < 300 {
		responseID, _ := doc["id"].(string)
		if response.Streamed {
			responseID = response.ResponseID
		}
		h.CommitRoute(plan, router.DeploymentID(dep), responseID)
	}
	h.RememberExchange(callID, r, raw, respBody)
	note := dataplaneNote(hit, dep, plan.SessionID, time.Since(start))
	if taskRequestID != "" {
		note.TaskRequestID, note.TaskInitial = taskRequestID, true
		note.TaskStatus = officialTaskStatus(status, doc, true)
	}
	applyStreamTiming(&note, response, start)
	note.Upstream = response.Diagnostics
	h.AnnotateCall(callID, note)
	var usage map[string]any
	if !countOnly && status >= 200 && status < 300 && hit.Transport.ResponseUsage != nil {
		usage = hit.Transport.ResponseUsage(doc, body)
		if response.Streamed {
			usage = response.Usage
		}
	}
	h.RecordSpend(w, principal, callID, alias, hit.Transport.ID+":"+hit.Action.Name, usage, start, false, status, depID)
	response.Body = respBody
	dptransport.WriteThrough(w, response)
}

// serveBypassFollow 查询、取消或读取已创建原生任务，始终回到创建该任务的部署。
// 参数 h、w、r、hit 是当前交互；id 是登记字段取出的任务 ID；callID、start 属于本次查询；principal 用于归属隔离与权限检查。
// 返回值：无；ServeBypass 对非创建操作调用，转发一次上游结果或写错误响应。
// 带 ID 时先读调用方/传输范围内的归属，再验证当前部署仍存在且端点匹配；缺失或跨身份查询返回 404。
// 无 ID 的列表操作仅在凭据与根地址唯一时可执行，避免猜测多个账户中应查询哪一个。
// 恢复创建时 RequestID、StartedAt 与计价维度；每次轮询保留独立调用 ID，有正实测用量的最终结果另用稳定 SettlementID 去重结算。
// SkipRouteUsage 避免反复轮询污染路由用量；记账持久层负责结算去重，本方法不以丢弃轮询日志代替去重。
// 副作用包括一次上游 HTTP、调用日志和可能的最终结算；不会自动迁移任务到其他部署或重新创建任务。
func serveBypassFollow(h dpcontract.Bypass, w http.ResponseWriter, r *http.Request, hit providercontract.Hit, id, callID string, start time.Time, principal *auth.Principal) {
	var dep config.ModelEntry
	var ok bool
	scopedID := officialTaskScope(principal, hit.Transport.ID, id)
	var depID string
	if id != "" {
		// 任务属于创建时账户，普通模型偏好或回退不能迁移它；不存在与越权都返回同一 404。
		pinned := h.OfficialDeployment(scopedID)
		if pinned == "" {
			util.WriteTypedError(w, r.URL.Path, 404, "not_found", "unknown task")
			return
		}
		dep, ok = h.FindDeployment(pinned)
		depID = pinned
		if !ok || !sameEndpoint(dep, hit) {
			util.WriteTypedError(w, r.URL.Path, 404, "not_found", "unknown task")
			return
		}
	} else {
		var names []string
		dep, names, ok = oneUpstream(h, hit)
		if !ok {
			if len(names) == 0 {
				util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "model not found")
				return
			}
			util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "model required: "+strings.Join(names, ", "))
			return
		}
	}
	if depID == "" {
		depID = router.CooldownID(dep)
	}
	if !h.AdmitRequest(w, r.URL.Path, principal, dep.ModelName, 0) {
		return
	}
	settings := h.RouteSettingsFor(principal)
	if settings.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "route template unavailable")
		return
	}
	settings = settings.ForModel(dep.ModelName)
	if settings.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", settings.Err.Error())
		return
	}
	dep, err := h.AttachCredential(dep)
	if err != nil {
		util.WriteTypedError(w, r.URL.Path, 401, "upstream_auth", "This model has no upstream API key configured.")
		return
	}
	hit, err = providerdeployment.ResolveHit(hit, dep)
	if err != nil {
		util.WriteTypedError(w, r.URL.Path, 409, "unavailable", err.Error())
		return
	}
	base, key := dptransport.BypassAuth(dep)
	if base == "" || key == "" {
		util.WriteTypedError(w, r.URL.Path, 401, "upstream_auth", "This model has no upstream API key configured.")
		return
	}
	url := dptransport.WithQuery(dptransport.BypassDeploymentURL(base, hit), r)
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(settings.TimeoutSeconds()*float64(time.Second)))
	defer cancel()
	response, err := dptransport.ForwardOfficial(h.HTTPClient(), r.WithContext(ctx), r.Method, url, key, nil, r.Header, hit.Transport, nil)
	respBody, status := response.Body, response.StatusCode
	if err != nil {
		h.RememberExchange(callID, r, nil, []byte(dpdiagnostics.SafeErr(err)))
		note := dataplaneNote(hit, dep, "", time.Since(start))
		facts := h.OfficialContext(scopedID)
		errorStart := start
		if id != "" && hit.Transport.Billing != nil && (hit.Action.Name == "get" || hit.Action.Name == "status") {
			note.TaskRequestID, note.TaskStatus = facts.RequestID, "failed"
			note.SkipRouteUsage = true
			if !facts.StartedAt.IsZero() {
				errorStart = facts.StartedAt
			}
		}
		applyStreamTiming(&note, response, start)
		note.Upstream = response.Diagnostics
		h.AnnotateCall(callID, note)
		h.RecordSpend(w, principal, callID, dep.ModelName, hit.Transport.ID+":"+hit.Action.Name, nil, errorStart, false, http.StatusBadGateway, depID)
		util.WriteTypedError(w, r.URL.Path, 502, "upstream_error", "upstream request failed")
		return
	}
	var doc map[string]any
	_ = json.Unmarshal(respBody, &doc)
	usage := usageFromOfficial(doc)
	billingStart := start
	if billing := hit.Transport.Billing; billing != nil {
		facts := h.OfficialContext(scopedID)
		usage = billing.Usage(doc, facts)
		if usage != nil && facts.Model != "" {
			usage["pricing_model"] = hit.Transport.StripPrefix + "/" + facts.Model
		}
		if positiveOfficialUsage(usage) && !facts.StartedAt.IsZero() {
			billingStart = facts.StartedAt
		}
	}
	// 轮询日志保留独立 callID；有正实测用量且没有进行中/失败事实时才建立稳定结算标识。
	// 进行中报告的进度统计不能当最终费用；实际去重由记账持久层完成。
	settlementID := ""
	if id != "" && status >= 200 && status < 300 && positiveOfficialUsage(usage) && !officialPending(doc) && officialTaskStatus(status, doc, false) != "failed" {
		// 每次完成查询都允许重试持久化；新任务由原日志事务标记去重，旧任务保留稳定结算 ID 兼容。
		settlementID = officialSettlementID(scopedID, depID)
	} else {
		usage = nil
	}
	respBody = rewriteQueueURLs(hit, doc, respBody)
	plan := h.PlanRoute(r, dep.ModelName, nil, principal)
	h.RememberExchange(callID, r, nil, respBody)
	note := dataplaneNote(hit, dep, plan.SessionID, time.Since(start))
	if id != "" && hit.Transport.Billing != nil && (hit.Action.Name == "get" || hit.Action.Name == "status") {
		facts := h.OfficialContext(scopedID)
		note.TaskRequestID = facts.RequestID
		note.TaskStatus = officialTaskStatus(status, doc, false)
		note.TaskSettlement = settlementID != ""
		// FAL 结果端点直接返回产物而不带 status；计量适配器确认完成后才允许结算。
		if note.TaskSettlement {
			note.TaskStatus = "completed"
		}
		if !facts.StartedAt.IsZero() {
			billingStart = facts.StartedAt
		}
	}
	note.SettlementID = settlementID
	note.SkipRouteUsage = true
	applyStreamTiming(&note, response, start)
	note.Upstream = response.Diagnostics
	h.AnnotateCall(callID, note)
	h.RecordSpend(w, principal, callID, dep.ModelName, hit.Transport.ID+":"+hit.Action.Name, usage, billingStart, false, status, depID)
	response.Body = respBody
	dptransport.WriteThrough(w, response)
}

// officialTaskStatus 把原生 HTTP 状态与任务正文归一为日志中的生命周期状态。
// 参数 status 为 HTTP 状态；doc 为已解析响应；initial 区分首次创建和后续轮询。返回 failed、completed、executing 或 polling。
// 创建/查询记账调用；HTTP 错误、error/detail 和明确失败状态优先，成功终态映射 completed。
// 缺少可识别状态时，创建仍记 executing，查询记 polling；只分类事实，不更新远程任务或存储。
func officialTaskStatus(status int, doc map[string]any, initial bool) string {
	if status >= 400 || doc["error"] != nil || doc["detail"] != nil {
		return "failed"
	}
	state, _ := doc["status"].(string)
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "succeeded", "completed", "success":
		return "completed"
	case "failed", "expired", "cancelled", "canceled", "error":
		return "failed"
	}
	if initial {
		return "executing"
	}
	return "polling"
}

// dataplaneNote 生成原生调用的用量备注，统一入口、供应商、实际上游模型、会话及计时字段。
// 参数 hit 为登记操作；dep 为实际执行部署；sessionID 为网关会话；elapsed 为本次耗时。返回独立 CallNote 值。
// 创建和查询在记账前调用；同步耗时作为默认计时，真正流式计时随后由 applyStreamTiming 覆盖。
// 函数不写日志、不访问网络，返回的备注必须通过 AnnotateCall 与同一 callID 关联。
func dataplaneNote(hit providercontract.Hit, dep config.ModelEntry, sessionID string, elapsed time.Duration) dpcontract.CallNote {
	name := dep.ParamString("custom_llm_provider", "")
	if name == "" {
		name = hit.Transport.StripPrefix
	}
	if name == "" {
		name = hit.Transport.ID
	}
	return dpcontract.CallNote{
		TTFTMs:    dpusage.TtftMillis(elapsed),
		Provider:  name,
		SessionID: sessionID,
	}
}

// sameEndpoint 检查当前部署是否仍匹配任务所要求的原生端点及传输。
// 参数 dep 为目录中的当前部署，hit 为本次登记路径；返回兼容时为 true。
// serveBypassFollow 恢复任务后调用，防止配置变化将旧任务送到错误协议；只读能力注册，不访问上游。
func sameEndpoint(dep config.ModelEntry, hit providercontract.Hit) bool {
	return providerdeployment.Includes(dep, hit.Transport.ID)
}

// eligible 从主机模型目录筛出支持本次原生端点的部署。
// 参数 h 提供当前模型表，hit 指定能力端点；返回能力兼容的候选切片。
// 原生创建与唯一上游查找调用；之后仍需过滤停用、授权、冷却和凭据，能力兼容本身不是执行许可。
// 只读目录，不发请求、不提交会话归属；空切片表示没有兼容部署。
func eligible(h dpcontract.Bypass, hit providercontract.Hit) []config.ModelEntry {
	var out []config.ModelEntry
	for _, m := range h.Models() {
		// 使用本次入口权限与动作校验；标准计数不隐式打开 Bypass，跨协议计数不入池。
		allowed := providerdeployment.Includes(m, hit.Transport.ID)
		if hit.Action.Name == "count_tokens" {
			_, resolveErr := providerdeployment.ResolveHit(hit, m)
			allowed = resolveErr == nil
		}
		if allowed && (hit.Action.Model == "" ||
			providercontract.OfficialID(hit.Transport.StripPrefix, m.ParamString("model", m.ModelName)) == hit.Action.Model) {
			out = append(out, m)
		}
	}
	return out
}

// oneUpstream 为不带任务 ID 的原生列表操作寻找唯一可确定的上游账户。
// 参数 h、hit 指定当前部署目录与端点；返回候选部署、可报告的公开模型名列表及是否唯一。
// serveBypassFollow 调用；过滤停用部署并补齐凭据后，以根地址与密钥组合去重，不以模型名数量判断账户数量。
// 凭据失败的候选跳过；没有或多个账户时 ok 为 false，由调用方返回明确错误。
// 仅查询服务端配置/凭据，不访问上游；不得把返回模型名列表当作供应商密钥或地址输出。
func oneUpstream(h dpcontract.Bypass, hit providercontract.Hit) (config.ModelEntry, []string, bool) {
	list, _ := dprouting.DropDisabled(eligible(h, hit))
	seen := map[string]config.ModelEntry{}
	var names []string
	for _, m := range list {
		names = append(names, m.ModelName)
		attached, err := h.AttachCredential(m)
		if err != nil {
			continue
		}
		base, key := dptransport.BypassAuth(attached)
		id := base + "|" + key
		if _, ok := seen[id]; !ok {
			seen[id] = attached
		}
	}
	if len(seen) == 1 {
		for _, dep := range seen {
			return dep, nil, true
		}
	}
	return config.ModelEntry{}, names, false
}

// officialTaskScope 构造原生任务归属的租户隔离键，避免供应商任务 ID 碰撞或跨用户读取。
// 参数 p 为已鉴权身份，transport 为登记传输 ID，id 为原生任务 ID；返回 v2: 前缀的 SHA-256 键。
// 创建保存与后续读取必须使用同一函数；身份优先使用密钥哈希，否则使用用户 ID。
// JSON 数组编码保留字段边界后再哈希，不靠字符串拼接分隔符猜边界；函数无网络或存储副作用。
func officialTaskScope(p *auth.Principal, transport, id string) string {
	caller := ""
	if p != nil {
		if p.Hash != "" {
			caller = "key:" + p.Hash
		} else {
			caller = "user:" + p.UserID
		}
	}
	raw, _ := json.Marshal([]string{caller, transport, id})
	sum := sha256.Sum256(raw)
	return "v2:" + hex.EncodeToString(sum[:])
}

// officialSettlementID 为同一个原生任务在同一部署上的最终费用构造稳定去重标识。
// 参数 scopedID 为已隔离身份的任务键，depID 为部署运行身份；返回 official-settlement: 前缀的哈希。
// 最终查询有正实测用量时调用；重复或并发轮询得到同一标识，持久层可重试失败结算并防止重复扣费。
// 它不替代每轮 callID，也不在本函数中执行去重或扣费。
func officialSettlementID(scopedID, depID string) string {
	raw, _ := json.Marshal([]string{scopedID, depID})
	sum := sha256.Sum256(raw)
	return "official-settlement:" + hex.EncodeToString(sum[:])
}

// positiveOfficialUsage 判断原生用量是否存在可用于最终结算的正实测维度。
// 参数 usage 为已提取的用量对象，可为 nil；返回任一受支持维度大于零时为 true。
// 查询结算调用；检查输入/输出与缓存 token、图像、时长、搜索等维度，缺失或全零为 false。
// 只检查事实，不估算费用、不修改映射；未知扩展维度不会自动变成计费依据。
func positiveOfficialUsage(usage map[string]any) bool {
	u := catalog.NormalizeUsage(usage)
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.CachedTokens > 0 ||
		u.CacheWriteTokens > 0 || u.Images > 0 || u.Seconds > 0 || u.Searches > 0
}

// officialPending 识别不能按完成结果结算的明确原生任务状态。
// 参数 doc 为任务正文；返回已识别的进行中或失败状态是否应视作非成功结果。
// 创建与查询计价调用；明确成功终态为 false，未知或缺失状态同样为 false，不把所有未知响应都假定未完成。
// 本函数只读状态，不检查 HTTP 状态或推断用量，调用方必须结合协议测量结果处理。
func officialPending(doc map[string]any) bool {
	status, _ := doc["status"].(string)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_queue", "in_progress", "queued", "pending", "running", "processing", "submitted", "failed", "error", "cancelled", "canceled":
		return true
	}
	return false
}

// usageFromOfficial 从原生 JSON 正文提取协议报告的用量字段。
// 参数 doc 为解析后的响应；返回用量映射，未报告时为 nil。
// 原生执行在记账时调用，复用 usage 包的普通、Responses 与 Gemini 包络规则；不执行估算或价格计算。
// 返回对象可能引用 doc 内部映射，调用方需在请求范围内使用，不能并发改写共享正文。
func usageFromOfficial(doc map[string]any) map[string]any {
	usage, _ := doc["usage"].(map[string]any)
	if len(usage) == 0 {
		return nil
	}
	return usage
}

// rewriteQueueURLs 把供应商队列响应中的查询/结果链接改为网关原生路径，使后续操作继续受鉴权与任务归属约束。
// 参数 hit 为登记操作；doc 为已解析响应；raw 为原始字节。返回改写后的 JSON，不能改写或编码失败时保留 raw。
// 原生成功创建/查询后调用；仅处理已登记的相对操作路径，不采信供应商 URL 的主机名作为出站目标。
// cancel_url 被置空以避免暴露不可代理操作；会原地修改 doc，但不发送请求或更新任务状态。
// 无需改写时原始字节保持不变，避免透传链无意义地重编码未知扩展字段。
func rewriteQueueURLs(hit providercontract.Hit, doc map[string]any, raw []byte) []byte {
	if !hit.Transport.QueueURLs || doc == nil {
		return raw
	}
	id := providercontract.ReadTaskID(doc, hit.Transport.TaskID)
	if id == "" {
		id = hit.Names["request_id"]
	}
	if id == "" {
		return raw
	}
	names := map[string]string{"request_id": url.PathEscape(id)}
	changed := false
	for _, a := range hit.Transport.Actions {
		field := ""
		switch a.Name {
		case "get":
			field = "response_url"
		case "status":
			field = "status_url"
		}
		if field != "" {
			if _, exists := doc[field]; exists {
				doc[field] = providercontract.Expand(a.PublicPath, names)
				changed = true
			}
		}
	}
	if _, exists := doc["cancel_url"]; exists {
		doc["cancel_url"] = ""
		changed = true
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

// applyStreamTiming 将原生转发器实测的流标志及首数据耗时写入调用备注。
// 参数 note 为可修改备注指针，response 为转发结果，start 为请求起点；返回值无。
// 原生执行记账前调用；非流式保留原计时，流式无数据时清空默认同步 TTFT，确有首数据时才设置毫秒值。
// 原地修改 note，不访问网络；HTTP 首响应字节与首 SSE 数据是不同事实，不能把空流的总耗时伪装成首 token 延迟。
func applyStreamTiming(note *dpcontract.CallNote, response dptransport.ForwardResponse, start time.Time) {
	note.Streamed = response.Streamed
	if response.Streamed {
		note.TTFTMs = nil
		if !response.FirstByteAt.IsZero() {
			note.TTFTMs = dpusage.TtftMillis(response.FirstByteAt.Sub(start))
		}
	}
}
