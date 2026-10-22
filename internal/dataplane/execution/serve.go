// 本文件串联统一入口的鉴权、预算、护栏、缓存、调度、重试、协议转换及最终用量记录。
// 调用关系与边界：数据面执行编排；具体参数、返回及副作用见各方法说明。
package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	dpdialogue "github.com/sunqirui1987/xhub/internal/dataplane/dialogue"
	dprouting "github.com/sunqirui1987/xhub/internal/dataplane/routing"
	dpstream "github.com/sunqirui1987/xhub/internal/dataplane/stream"
	dptransport "github.com/sunqirui1987/xhub/internal/dataplane/transport"
	dpusage "github.com/sunqirui1987/xhub/internal/dataplane/usage"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	providercapability "github.com/sunqirui1987/xhub/internal/provider/capability"
	providerdeployment "github.com/sunqirui1987/xhub/internal/provider/deployment"
	"github.com/sunqirui1987/xhub/internal/router"
	"github.com/sunqirui1987/xhub/internal/util"
)

var (
	// 已收到 429/5xx 的内部哨兵；最终对外状态统一为 502，原始状态和正文另行记录。
	errUpstreamStatus = errors.New("upstream_error")
	// 流读取正常结束但未输出内容时不能提交成功归属，仍可在未写出前尝试下一候选。
	errEmptyUpstream = errors.New("empty upstream stream")
)

// Serve 执行一次统一推理请求，将调用方协议适配成部署登记的上游协议，并把结果转换回来。
// 参数 h 提供身份、路由、凭据、缓存、护栏与记账能力；w 接收 HTTP 响应；r 提供上下文、头和只读取一次的 JSON 正文；op 是目录操作名，如 chat、responses、embedding。
// 返回值：无；所有成功、拒绝和失败结果直接写入 w。网关统一推理入口调用本方法。
//
// 执行顺序有业务约束：先鉴权并建立会话计划；Responses 恢复授权历史后检查预算与护栏；
// 随后执行并发钩子和扩展，解析请求级模板与协议，检查缓存，再选择兼容部署和动态偏好。
// 每轮发送前补齐服务端凭据并检查实际目标权限；失败时按模板尝试同池部署或跨模型回退。
// 成功路径记录真实出站诊断、会话归属、响应正文和用量；释放并发名额、正文和尝试上下文。
//
// 会话偏好只影响顺序，不能绕过当前能力、冷却、权限和预算。Required 父响应归属禁止换目标。
// 流式响应一旦写出便不能重试，否则客户端会混合两次回复且可能重复计费；断流只记失败。
// Responses 在发布完成事件前保存已验证历史，使客户端收到终态后立即续接也能找到上下文。
// 响应缓存隔离租户、会话、查询及完整路由配置，命中后复核实际模型权限；Responses 不读此缓存。
// 副作用包括上游 HTTP 请求、流式写出、进程/Redis 路由状态、会话缓存和用量记账。
func Serve(h dpcontract.Adapted, w http.ResponseWriter, r *http.Request, op string) {
	dpdiagnostics.TraceHop(r.URL.Path)
	util.Trace("process path=%s step=start op=%s", r.URL.Path, op)
	start := time.Now()
	callID := util.CallID()
	defer h.ReleaseCall(callID)
	util.SetCallID(w, callID)
	// 第一阶段：先取得可信身份，再读取客户端模型和会话信号；这些信号不能自行授予权限。
	p := h.RequireLLMPrincipal(w, r)
	if p == nil {
		util.Debug("process path=%s step=auth ok=false", r.URL.Path)
		return
	}
	util.Debug("process path=%s step=auth ok=true kind=%s", r.URL.Path, p.Kind)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		util.Error("process path=%s step=body reason=invalid body", r.URL.Path)
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "invalid body")
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		util.Error("process path=%s step=body reason=invalid json", r.URL.Path)
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "invalid json")
		return
	}
	alias, _ := body["model"].(string)
	if alias == "" {
		util.Error("process path=%s step=model reason=required", r.URL.Path)
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "model required")
		return
	}
	// 会话计划先于调度生成；这里只读偏好或父响应归属，最终候选仍要按当前配置重新验证。
	if op == "responses" {
		// 统一入口的完整客户端历史允许跨模型迁移；不把未知来源的账户密文当作访问旧会话的凭据。
		// 仅改本次副本；previous_response_id 仍走严格归属并在下方恢复服务器保存的原始推理。
		body = llmdialogue.PortableResponsesObject(body, "input")
		w.Header().Set("X-XHub-History-Mode", "portable")
	}
	plan := h.PlanRoute(r, alias, body, p)
	w.Header().Set("X-Session-Id", plan.SessionID)
	h.AnnotateCall(callID, dpcontract.CallNote{SessionID: plan.SessionID, SessionSource: plan.SessionSource})
	if plan.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", plan.Err.Error())
		return
	}
	// 恢复经过授权的历史后才检查输入额度和护栏，避免只检查本轮短输入而漏掉真实出站上下文。
	if op == "responses" {
		body, err = dpdialogue.ResponsesRequestBody(body, plan.History)
		if err != nil {
			util.WriteTypedError(w, r.URL.Path, 400, "unsupported_capability", err.Error())
			return
		}
	}
	est := dpusage.EstimateTokens(body)
	if !h.AdmitRequest(w, r.URL.Path, p, alias, est) {
		util.Debug("process path=%s step=limits refused model=%s", r.URL.Path, alias)
		return
	}
	if op == "chat" || op == "responses" || op == "messages" || op == "gemini" || op == "vertex" || op == "" {
		if blocked, msg := h.GuardrailBlocks(callID, body); blocked {
			util.Error("process path=%s step=guardrail blocked model=%s", r.URL.Path, alias)
			// 护栏拒绝也属于一次真实请求：保存交互并记零费用失败行，控制台才能定位拦截原因。
			h.RememberExchange(callID, r, raw, nil)
			h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusBadRequest, "")
			util.WriteTypedError(w, r.URL.Path, 400, "guardrail_failed", msg)
			return
		}
		// 脱敏可能原地修改 body，必须重编码，让缓存键、交互日志和出站编码器看到同一份输入。
		raw, err = json.Marshal(body)
		if err != nil {
			util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", "invalid guarded request")
			return
		}
	}

	cfg := h.GatewayConfig()
	// 第二阶段：读取身份链选中的请求级路由模板，再应用模型默认设置。
	// 策略、重试、超时与冷却都从这个快照读取，不能混入进程 cfg.RouterSettings。
	// 保留未套用模型的 baseRouteCfg，跨模型回退时重新解析目标的默认设置。
	routeCfg := h.RouteSettingsFor(p)
	if routeCfg.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "route template unavailable")
		return
	}
	baseRouteCfg := routeCfg
	routeCfg = routeCfg.ForModel(alias)
	if routeCfg.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", routeCfg.Err.Error())
		return
	}
	tenant := p.Hash
	if tenant == "" {
		tenant = "user:" + p.UserID
	}
	var dialogue llmdialogue.Dialogue
	if dpdialogue.CallerProtocol(op) != "" {
		if op == "responses" && len(plan.MessagesHistory) > 0 {
			dialogue, err = llmdialogue.ParseResponsesWithMessagesHistory(dpdialogue.DialogueRequestBody(body), plan.MessagesHistory)
		} else {
			dialogue, err = llmdialogue.ParseDialogue(dpdialogue.CallerProtocol(op), dpdialogue.DialogueRequestBody(body))
		}
		if err != nil {
			util.WriteTypedError(w, r.URL.Path, 400, "unsupported_capability", err.Error())
			return
		}
	}
	scope := map[string]any{
		"models": cfg.ModelList, "router": routeCfg.Settings,
		"model_defaults": routeCfg.ModelDefaults,
		"session":        plan.SessionID, "query": r.URL.RawQuery,
	}
	// 路由文档完整参与缓存隔离；空配置和非空配置使用同一键结构。
	scope["routing_groups"] = routeCfg.RoutingGroups
	scope["fallbacks"] = routeCfg.ModelFallbacks
	cacheScope, err := json.Marshal(scope)
	if err != nil {
		util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "invalid route configuration")
		return
	}
	ck := util.CacheKey(tenant, op, alias, string(raw), string(cacheScope))
	stream, _ := body["stream"].(bool)
	// 路由与回退配置已经进入缓存键，不再用配置是否存在决定缓存读取。
	// Responses 的响应 ID 必须对应本次执行和上下文，不能复用已过期的响应缓存 ID。
	if op != "responses" && !stream {
		if hit, ok := h.ResponseCache().Get(ck); ok {
			// 缓存记录实际执行模型和组；复用成功正文前仍检查当前权限和预算，
			// 与首次调用检查同一范围，不能靠禁用整个回退功能来代替授权。
			checked := map[string]bool{alias: true}
			for _, model := range hit.Models {
				if model != "" && !checked[model] {
					if !h.EnforceIdentityLimits(w, r.URL.Path, p, model, est) {
						return
					}
					checked[model] = true
				}
			}
			elapsed := time.Since(start)
			pt, ct := dpusage.BodyUsage(hit.Body)
			util.Debug("process path=%s step=cache hit=true model=%s", r.URL.Path, alias)
			dpdiagnostics.LogMetrics(r.URL.Path, alias, true, pt, ct, elapsed, elapsed)
			h.RememberExchange(callID, r, raw, hit.Body)
			h.AnnotateCall(callID, dpcontract.CallNote{SessionID: plan.SessionID, CacheKey: ck, CacheHit: true})
			h.WriteCacheHit(w, p, callID, alias, ck, op, hit.Body, start)
			return
		}
		util.Trace("process path=%s step=cache hit=false model=%s", r.URL.Path, alias)
	}

	if err := router.ValidateStrategy(routeCfg.Strategy()); err != nil {
		util.Error("process path=%s step=strategy model=%s", r.URL.Path, alias)
		util.WriteTypedError(w, r.URL.Path, 400, "invalid_request", err.Error())
		return
	}
	// 第三阶段：先过滤停用和协议不兼容项，再调度；偏好不能把已剔除的部署塞回候选。
	// 权重仅取自解析后的模板，旧部署权重不会参与任何策略。
	models := cfg.ModelList
	models, _ = dprouting.DropDisabled(models)
	// 实际请求与预览使用同一兼容性判断，策略只处理已经兼容的候选。
	if dpdialogue.CallerProtocol(op) != "" {
		models, _ = providerdeployment.Candidates(models, providercapability.EndpointForOp(op), &dialogue)
	} else {
		models, _ = providerdeployment.Candidates(models, providercapability.EndpointForOp(op), nil)
	}
	state := h.RouteState()
	state.Allocations = routeCfg.Policy.Shares()
	// 诊断保留匹配、兼容与可用三个阶段；只读计算不推进调度状态，不改变既有回退顺序。
	diagnosis := dprouting.DiagnoseRoute(cfg.ModelList, models, alias, providercapability.EndpointForOp(op), routeCfg, state, &dialogue, dpdialogue.CallerProtocol(op) != "")
	diagnosis.Log(r.URL.Path, callID, alias, routeCfg)
	runtimePin := dprouting.RuntimePin(models, plan.Pinned)
	pool := routeCfg.Schedule(models, alias, state, runtimePin)
	disableFallbacks, _ := body["disable_fallbacks"].(bool)
	fallbacks := dprouting.NewFallbackQueue(baseRouteCfg, alias, plan.Required || disableFallbacks)
	if len(pool) == 0 {
		pool, routeCfg = fallbacks.Next("general", models, state)
		for target := range fallbacks.Seen {
			if target == alias {
				continue
			}
			settings := baseRouteCfg.ForModel(target)
			if settings.Err == nil {
				targetState := state
				targetState.Allocations = settings.Policy.Shares()
				dprouting.DiagnoseRoute(cfg.ModelList, models, target, providercapability.EndpointForOp(op), settings, targetState, &dialogue, dpdialogue.CallerProtocol(op) != "").Log(r.URL.Path, callID, target, settings)
			}
		}
	}
	pool, routeCfg = fallbacks.SelectAffinity(&plan, pool, alias, models, state, routeCfg)
	if plan.Required && len(pool) == 0 {
		util.WriteTypedError(w, r.URL.Path, 409, "continuation_unavailable", "original deployment is unavailable")
		return
	}
	if routeCfg.Err != nil {
		util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "fallback route unavailable")
		return
	}
	util.Debug("process path=%s step=route model=%s deployments=%d stream=%t call_id=%q endpoint=%q resolved_model=%q fallback_visited=%d", r.URL.Path, alias, len(pool), stream, callID, providercapability.EndpointForOp(op), fallbacks.PoolAlias, len(fallbacks.Seen)-1)
	if len(pool) == 0 {
		status, kind, message := diagnosis.Failure(alias)
		util.Error("process path=%s step=route call_id=%q model=%q reason=%q message=%q fallback_visited=%d", r.URL.Path, callID, alias, kind, message, len(fallbacks.Seen)-1)
		util.WriteTypedError(w, r.URL.Path, status, kind, message)
		return
	}
	// 第四阶段：外层遍历模型回退图，中层遍历部署池，内层按该目标模板重试。
	// Retries 至少返回 1，省略重试字段也会执行首次尝试；这不是额外重试次数。

	var lastErr error
	var lastStatus int
	var lastResponse []byte
	var lastProvider string
	triedHTTP := false
	missingCredential := false
	// stopCanceled 在调用方上下文结束时记录一次零费用取消并终止执行。
	// 参数保留已读取正文、用量、部署和首字时间；返回是否已处理取消。
	// 每次出站及传输错误分支调用，不能检查已主动释放的 attempt context，否则正常完成也会被误判。
	// 499 只表示调用方中止，不增加共享部署失败数；已验证的成功终态仍走原有成功结算路径。
	stopCanceled := func(response []byte, usage map[string]any, deployment string, ttft time.Duration) bool {
		if r.Context().Err() == nil {
			return false
		}
		if len(response) == 0 {
			response = []byte("client request canceled")
		}
		h.RememberExchange(callID, r, raw, response)
		h.AnnotateCall(callID, dpcontract.CallNote{Streamed: stream, TTFTMs: dpusage.TtftMillis(ttft), Provider: lastProvider, CacheKey: ck, SessionID: plan.SessionID})
		h.RecordSpend(w, p, callID, alias, op, usage, start, false, 499, deployment)
		util.Info("process path=%s step=cancel status=499 call_id=%q", r.URL.Path, callID)
		return true
	}
	// 分开保存“尚未发 HTTP”“凭据缺失”“构造失败”和“上游失败”的事实，
	// 使最终错误能指出配置或协议问题，避免一律归因为缺少密钥。

	for len(pool) > 0 {
		kind := "general"
		for _, rawDep := range pool {
			if stopCanceled(nil, nil, "", 0) {
				return
			}
			// 组耗尽后采用最后尝试成员的回退链，保留原组名的初始权限检查。
			fallbacks.UseDeployment(rawDep.ModelName)
			if !plan.Required {
				plan.Target = fallbacks.Target(rawDep)
			}
			// 回退目标可能是另一个组；组别名与真实成员都必须通过权限和预算检查。
			if fallbacks.PoolAlias != alias && fallbacks.PoolAlias != rawDep.ModelName && !h.EnforceIdentityLimits(w, r.URL.Path, p, fallbacks.PoolAlias, est) {
				return
			}
			if triedHTTP {
				// 重新解析的是调用方身份，确认密钥未在两次出站之间撤销；不是重新附加供应商凭据。
				// 身份与预算必须在继续执行前复核，不能沿用首次鉴权作为整个重试链的许可。
				p2, err := h.ResolveRequest(r)
				if err != nil || p2 == nil || !p2.CanInfer() {
					util.WriteTypedError(w, r.URL.Path, 401, "invalid_api_key", "Authentication Error, No api key passed in.")
					return
				}
				p = p2
				if !h.EnforceIdentityLimits(w, r.URL.Path, p, alias, est) {
					return
				}
			}
			if rawDep.ModelName != alias && !h.EnforceIdentityLimits(w, r.URL.Path, p, rawDep.ModelName, est) {
				return
			}
			dep, credentialErr := h.AttachCredential(rawDep)
			if credentialErr != nil {
				missingCredential = true
				util.Debug("skip deployment path=%s model=%s reason=credential_unavailable", r.URL.Path, alias)
				continue
			}
			// 固定历史必须比对首次外发时的实际连接，不能只靠可编辑的稳定部署 ID。
			if !dprouting.BindConnection(&plan, dep) {
				util.WriteTypedError(w, r.URL.Path, 409, "unavailable", "response deployment unavailable")
				return
			}
			upstreamModel := dep.ParamString("model", alias)
			supplier := dep.ParamString("custom_llm_provider", "")
			realModel := upstreamModel
			lastProvider = supplier
			var built dptransport.Upstream
			var err error
			apiBase := dep.ParamString("api_base", "")
			did := router.CooldownID(rawDep)
			// 在执行时保存已解析凭证的连接身份，最终失败可能没有 depID，且配置可能随后改变。
			connection, upstreamModel := dep.MonitorIdentity()
			h.AnnotateCall(callID, dpcontract.CallNote{Supplier: connection, APIBase: dep.LogAPIBase(), UpstreamModel: upstreamModel, UpstreamSelected: true})
			if dpdialogue.CallerProtocol(op) != "" {
				built, err = dpdialogue.BuildDialogue(dep, dialogue)
				if errors.Is(err, dpdialogue.ErrDialogueCredentials) {
					missingCredential = true
					util.Debug("skip deployment path=%s provider=%s model=%s reason=missing api key or api base", r.URL.Path, supplier, realModel)
					continue
				}
				if err != nil {
					lastErr = err
					continue
				}
			} else {
				built, err = dpdialogue.BuildRegisteredOperation(dep, body, op)
				if errors.Is(err, dpdialogue.ErrDialogueCredentials) {
					missingCredential = true
					continue
				}
				if err != nil {
					lastErr = err
					continue
				}
			}

			for try := 0; try < routeCfg.Retries(); try++ {
				if stopCanceled(nil, nil, did, 0) {
					return
				}
				if try > 0 {
					fresh, err := h.ResolveRequest(r)
					if err != nil || fresh == nil || !fresh.CanInfer() {
						util.WriteTypedError(w, r.URL.Path, 401, "invalid_api_key", "invalid api key")
						return
					}
					p = fresh
					if rawDep.ModelName != alias && !h.EnforceIdentityLimits(w, r.URL.Path, p, rawDep.ModelName, est) {
						return
					}
					if !h.EnforceIdentityLimits(w, r.URL.Path, p, alias, est) {
						return
					}
				}
				// 每次尝试重新设置，回退到原生搜索部署时不能遗留旧降级提示。
				w.Header().Del("X-XHub-Dropped-Capabilities")
				if len(built.DroppedCapabilities) > 0 {
					w.Header().Set("X-XHub-Dropped-Capabilities", strings.Join(built.DroppedCapabilities, ","))
					util.Info("process step=capability_dropped call_id=%q deployment_id=%q capability=web_search reason=deployment_unsupported", callID, router.DeploymentID(rawDep))
				}
				if budget, ok := h.(dpcontract.BudgetHost); ok {
					protocol := ""
					if transport, exists := providerdeployment.Execution(dep); exists {
						protocol = transport.Protocol
					}
					var allowed bool
					built.Body, allowed = budget.PrepareBudgetAttempt(w, r, p, callID, dep, protocol, built.Body, start)
					if !allowed {
						return
					}
				}
				triedHTTP = true
				util.Trace("process path=%s step=attempt n=%d provider=%s model=%s base_host=%s", r.URL.Path, try+1, supplier, realModel, dpdiagnostics.BaseHost(apiBase))
				h.IncBusy(did)
				// 每次尝试拥有独立 deadline；复制客户端并清零 Timeout，让当前模板控制完整读取时限。
				// cancelAttempt、正文 Close 和 DecBusy 必须在每条退出路径配对，不能只清理成功路径。
				attemptCtx, cancelAttempt := context.WithTimeout(r.Context(), time.Duration(routeCfg.TimeoutSeconds()*float64(time.Second)))
				req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, built.URL, bytes.NewReader(built.Body))
				if err != nil {
					cancelAttempt()
					h.DecBusy(did)
					lastErr = err
					util.Error("upstream request path=%s provider=%s model=%s err=%s", r.URL.Path, supplier, realModel, dpdiagnostics.SafeErr(err))
					continue
				}
				for key, values := range built.Header {
					req.Header[key] = values
				}
				client := *h.HTTPClient()
				client.Timeout = 0
				// 明确地址不跟随重定向，防止凭据被转发到未配置的连接。
				client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
				req, firstByte := dpdiagnostics.TraceFirstByte(req)
				if budget, ok := h.(dpcontract.BudgetHost); ok && !budget.MarkBudgetAttemptSubmitted(w, r, callID) {
					cancelAttempt()
					h.DecBusy(did)
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					// 即使未收到响应，也保存本次尝试的实际出站路径，便于失败日志定位。
					h.AnnotateCall(callID, dpcontract.CallNote{Upstream: &dpdiagnostics.UpstreamDiagnostics{Endpoint: req.URL.EscapedPath(), DroppedCapabilities: built.DroppedCapabilities}})
					cancelAttempt()
					h.DecBusy(did)
					lastErr = err
					if stopCanceled(nil, nil, did, 0) {
						return
					}
					h.NoteFailure(router.CooldownID(rawDep), routeCfg)
					util.Error("upstream dial path=%s provider=%s model=%s base_host=%s err=%s", r.URL.Path, supplier, realModel, dpdiagnostics.BaseHost(apiBase), dpdiagnostics.SafeErr(err))
					continue
				}
				// 先观察原始响应，再执行错误重试和协议转换。
				resp.Request = req
				diagnostic := dpdiagnostics.ObserveUpstream(resp)
				diagnostic.DroppedCapabilities = built.DroppedCapabilities
				if !firstByte.IsZero() {
					diagnostic.FirstByteAt = *firstByte
				}
				h.AnnotateCall(callID, dpcontract.CallNote{Upstream: diagnostic})
				if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
					// 重试前保存完整错误正文；若所有候选都失败，最终日志必须能还原最后一次上游响应。
					lastResponse, _ = io.ReadAll(resp.Body)
					resp.Body.Close()
					cancelAttempt()
					h.DecBusy(did)
					h.NoteFailure(router.CooldownID(rawDep), routeCfg)
					lastStatus = resp.StatusCode
					lastErr = errUpstreamStatus
					util.Error("upstream status path=%s provider=%s model=%s base_host=%s status=%d", r.URL.Path, supplier, realModel, dpdiagnostics.BaseHost(apiBase), resp.StatusCode)
					continue
				}

				// 专用 4xx 在分类后切换模型；普通 4xx 保留原状态与正文。
				if resp.StatusCode >= 400 {
					errorBody, readErr := io.ReadAll(resp.Body)
					resp.Body.Close()
					resp.Body = io.NopCloser(bytes.NewReader(errorBody))
					errorKind := dprouting.FallbackKind(resp.StatusCode, errorBody)
					if readErr == nil && errorKind != "" && !fallbacks.Disabled && len(baseRouteCfg.ModelFallbacks[fallbacks.Current].Targets(errorKind)) > 0 {
						cancelAttempt()
						h.DecBusy(did)
						lastStatus = resp.StatusCode
						lastErr = errUpstreamStatus
						kind = errorKind
						goto nextFallbackModel
					}
				}
				h.NoteLatency(did, float64(time.Since(start).Milliseconds()))
				h.SetChatHeaders(w, p, alias, apiBase)
				if stream {
					var wrote bool
					var usage map[string]any
					var ttft time.Duration
					var streamed []byte
					var streamErr error
					preparationFailed := false
					if dpdialogue.CallerProtocol(op) != "" && resp.StatusCode < 400 {
						execution, _ := providerdeployment.Execution(dep)
						// 终态发送前提交归属与历史，使收到完成事件的客户端能立即续接。
						// commit 参数 result 为验证后的回复，无返回值；Responses 历史及 Chat/Messages 推理归属均须先于终态可见。
						commit := func(result llmdialogue.DialogueResult) {
							if op == "responses" {
								plan.History = dpdialogue.ResponseHistory(dialogue, body, result)
							}
							if op == "responses" || op == "messages" || op == "chat" {
								h.CommitRoute(plan, router.DeploymentID(rawDep), result.ID)
							}
						}
						if op == "responses" && execution.Protocol == "openai-responses" {
							wrote, usage, ttft, streamed, streamErr = dpdialogue.PipePortableResponses(w, resp, start, alias, commit)
						} else if op == "responses" && execution.Protocol == "anthropic-messages" && plan.PrepareMessages != nil {
							// 引用必须在 output_item.done 之前持久化；工具增量可以先发送，完成项不能先于归属。
							prepare := func(result llmdialogue.DialogueResult) (llmdialogue.DialogueResult, error) {
								prepared, err := plan.PrepareMessages(plan.Target, result)
								if err == nil && plan.CommitMessagesHistory != nil {
									plan.History = dpdialogue.ResponseHistory(dialogue, body, prepared)
									err = plan.CommitMessagesHistory(plan.Target, prepared.ID, plan.History)
								}
								preparationFailed = err != nil
								return prepared, err
							}
							wrote, usage, ttft, streamed, streamErr = dpdialogue.PipeDialogueWithPreparation(w, resp, start, alias, execution.Protocol, dpdialogue.CallerProtocol(op), dialogue.ToolBindings, prepare)
						} else {
							wrote, usage, ttft, streamed, streamErr = dpdialogue.PipeDialogueWithTools(w, resp, start, alias, execution.Protocol, dpdialogue.CallerProtocol(op), dialogue.ToolBindings, commit)
						}
					} else {
						wrote, usage, ttft, streamed, streamErr = dpstream.PipeStream(w, resp, start)
					}
					h.DecBusy(did)
					cancelAttempt()
					if streamErr != nil {
						lastErr = streamErr
						if stopCanceled(streamed, usage, did, ttft) {
							return
						}
						h.NoteFailure(did, routeCfg)
						util.Error("upstream stream path=%s provider=%s model=%s base_host=%s err=%s", r.URL.Path, supplier, realModel, dpdiagnostics.BaseHost(apiBase), dpdiagnostics.SafeErr(streamErr))
						if wrote || preparationFailed {
							// 客户端已收到部分回复，不能重试或换模型；记录已有用量和 502 事实后结束。
							h.RememberExchange(callID, r, raw, streamed)
							h.AnnotateCall(callID, dpcontract.CallNote{Streamed: true, TTFTMs: dpusage.TtftMillis(ttft), Provider: supplier, CacheKey: ck, SessionID: plan.SessionID})
							h.RecordSpend(w, p, callID, alias, op, usage, start, false, http.StatusBadGateway, did)
							if !wrote {
								util.WriteTypedError(w, r.URL.Path, http.StatusBadGateway, "history_unavailable", dpdiagnostics.SafeErr(streamErr))
							}
							return
						}
						continue
					}
					if wrote {
						if dpdialogue.CallerProtocol(op) != "" {
							usage = llmdialogue.NormalizeDialogueUsage(usage)
						} else {
							usage = dpusage.CompleteUsage(usage, body, streamed)
						}
						pt, ct := dpusage.UsageCounts(usage)
						dpdiagnostics.LogMetrics(r.URL.Path, alias, false, pt, ct, ttft, time.Since(start))
						depID := did
						h.RememberExchange(callID, r, raw, streamed)
						h.AnnotateCall(callID, dpcontract.CallNote{
							Streamed: true, TTFTMs: dpusage.TtftMillis(ttft), Provider: supplier, CacheKey: ck,
							SessionID: plan.SessionID,
						})
						if resp.StatusCode < 400 && op != "responses" {
							h.CommitRoute(plan, router.DeploymentID(rawDep), dpstream.ResponseID(streamed))
						}
						h.RecordSpend(w, p, callID, alias, op, usage, start, false, resp.StatusCode, depID)
						return
					}
					lastErr = errEmptyUpstream
					util.Error("upstream stream path=%s provider=%s model=%s base_host=%s err=%s", r.URL.Path, supplier, realModel, dpdiagnostics.BaseHost(apiBase), dpdiagnostics.SafeErr(errEmptyUpstream))
					continue
				}
				messagesHistoryCommitted := false
				respBody, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				cancelAttempt()
				h.DecBusy(did)
				if readErr != nil {
					lastErr = readErr
					if stopCanceled(respBody, nil, did, 0) {
						return
					}
					h.NoteFailure(did, routeCfg)
					continue
				}
				if dpdialogue.CallerProtocol(op) != "" && resp.StatusCode < 400 {
					execution, _ := providerdeployment.Execution(dep)
					result, conversionErr := llmdialogue.ParseDialogueResult(execution.Protocol, respBody)
					if conversionErr != nil {
						lastErr = conversionErr
						// HTTP 200 只说明传输成功；无法解析的供应商正文仍是部署失败，
						// 必须进入与网络错误、5xx 和流式转换错误相同的冷却链。
						h.NoteFailure(did, routeCfg)
						continue
					}
					result.ToolBindings = dialogue.ToolBindings
					if op == "responses" && execution.Protocol == "anthropic-messages" && plan.PrepareMessages != nil {
						prepared, prepareErr := plan.PrepareMessages(plan.Target, result)
						if prepareErr == nil && plan.CommitMessagesHistory != nil {
							plan.History = dpdialogue.ResponseHistory(dialogue, body, prepared)
							prepareErr = plan.CommitMessagesHistory(plan.Target, prepared.ID, plan.History)
						}
						if prepareErr != nil {
							// 上游已完整完成，本地历史失败不能再次付费生成；保留真实 usage 和失败原因，预占待核对。
							detail := dpdiagnostics.SafeErr(prepareErr)
							h.RememberExchange(callID, r, raw, []byte(detail))
							h.AnnotateCall(callID, dpcontract.CallNote{Provider: supplier, SessionID: plan.SessionID})
							h.RecordSpend(w, p, callID, alias, op, llmdialogue.NormalizeDialogueUsage(result.Usage), start, false, http.StatusBadGateway, did)
							util.WriteTypedError(w, r.URL.Path, http.StatusBadGateway, "history_unavailable", detail)
							return
						}
						result = prepared
						messagesHistoryCommitted = plan.CommitMessagesHistory != nil
					}
					respBody, conversionErr = llmdialogue.EncodeDialogueResult(result, dpdialogue.CallerProtocol(op), alias)
					if conversionErr == nil && op == "responses" {
						// 编码后统一清理，覆盖原生和 Chat 桥接生成的 reasoning ID；原始结果仍供服务器续接。
						var public map[string]any
						conversionErr = json.Unmarshal(respBody, &public)
						if conversionErr == nil {
							respBody, conversionErr = json.Marshal(llmdialogue.PortableResponsesObject(public, "output"))
						}
					}
					if conversionErr != nil {
						lastErr = conversionErr
						// 编码失败同样不能提交路由归属；记录部署失败后再尝试候选项。
						h.NoteFailure(did, routeCfg)
						continue
					}
					if op == "responses" {
						plan.History = dpdialogue.ResponseHistory(dialogue, body, result)
					}
				}
				elapsed := time.Since(start)
				pt, ct := dpusage.BodyUsage(respBody)
				util.Info("process path=%s step=upstream status=%d provider=%s model=%s", r.URL.Path, resp.StatusCode, supplier, realModel)
				dpdiagnostics.LogMetrics(r.URL.Path, alias, false, pt, ct, elapsed, elapsed)
				depID := did
				h.RememberExchange(callID, r, raw, respBody)
				h.AnnotateCall(callID, dpcontract.CallNote{
					TTFTMs: dpusage.TtftMillis(elapsed), Provider: supplier, CacheKey: ck,
					SessionID: plan.SessionID,
				})
				if resp.StatusCode < 400 && !messagesHistoryCommitted {
					h.CommitRoute(plan, router.DeploymentID(rawDep), dpstream.ResponseID(respBody))
				}
				h.WriteChatJSON(w, p, callID, alias, ck, op, "", respBody, resp.StatusCode, start, depID, fallbacks.PoolAlias, rawDep.ModelName)
				return
			}
		}

	nextFallbackModel:
		pool, routeCfg = fallbacks.Next(kind, models, state)
		if routeCfg.Err != nil {
			util.WriteTypedError(w, r.URL.Path, 503, "unavailable", "fallback route unavailable")
			return
		}
	}

	// 最终失败仍写交互与零费用用量行，使控制台能查到请求，而非只留下进程日志。
	// 未发出 HTTP 时区分凭据和编码错误；已经尝试 HTTP 时保留最后上游状态/正文或安全错误。
	if !triedHTTP {
		switch {
		case missingCredential:
			util.Error("dataplane path=%s status=401 code=authentication_error provider=%s", r.URL.Path, lastProvider)
			h.RememberExchange(callID, r, raw, []byte("This model has no upstream API key configured."))
			h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusUnauthorized, "")
			util.WriteTypedError(w, r.URL.Path, 401, "authentication_error", "This model has no upstream API key configured.")
		case lastErr != nil:
			// 部署存在但不能构造所需协议请求，报告安全编码错误才能指出应修复的配置或能力。
			util.Error("dataplane path=%s status=400 code=provider_not_implemented provider=%s err=%s",
				r.URL.Path, lastProvider, dpdiagnostics.SafeErr(lastErr))
			detail := dpdiagnostics.SafeErr(lastErr)
			h.RememberExchange(callID, r, raw, []byte("The request could not be encoded for "+lastProvider+": "+detail))
			// RecordSpend 必须在写响应前执行，确保失败行携带最终 HTTP 状态和错误正文。
			h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusBadRequest, "")
			util.WriteTypedError(w, r.URL.Path, 400, "execution_error",
				"The request could not be encoded for "+lastProvider+": "+detail)
		default:
			util.Error("dataplane path=%s status=400 code=provider_not_implemented", r.URL.Path)
			h.RememberExchange(callID, r, raw, []byte("execution_error"))
			h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusBadRequest, "")
			util.WriteTypedError(w, r.URL.Path, 400, "execution_error", "execution_error")
		}
		return
	}
	// 供应商流内错误已有结构，不能再次压成 upstream_error 字符串；HTTP 200 仅代表流握手成功。
	var upstreamFailure *dpdiagnostics.UpstreamError
	if errors.As(lastErr, &upstreamFailure) {
		envelope := dpdiagnostics.ErrorEnvelope(upstreamFailure)
		if dpdialogue.CallerProtocol(op) == "anthropic-messages" {
			envelope["type"] = "error"
		}
		rawError, _ := json.Marshal(envelope)
		h.RememberExchange(callID, r, raw, rawError)
		h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusBadGateway, "")
		util.WriteJSON(w, http.StatusBadGateway, envelope)
		return
	}

	if lastStatus > 0 {
		util.Error("dataplane path=%s status=502 code=upstream_error detail=upstream %d", r.URL.Path, lastStatus)
		if len(lastResponse) == 0 {
			lastResponse = []byte("upstream " + strconv.Itoa(lastStatus))
		}
		h.RememberExchange(callID, r, raw, lastResponse)
		h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusBadGateway, "")
		util.WriteTypedError(w, r.URL.Path, 502, "upstream_error", "upstream "+strconv.Itoa(lastStatus))
		return
	}
	if lastErr != nil {
		detail := dpdiagnostics.SafeErr(lastErr)
		util.Error("dataplane path=%s status=502 code=upstream_error detail=%s", r.URL.Path, detail)
		h.RememberExchange(callID, r, raw, []byte(detail))
		h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusBadGateway, "")
		util.WriteTypedError(w, r.URL.Path, 502, "upstream_error", detail)
		return
	}
	util.Error("dataplane path=%s status=502 code=upstream_error detail=all deployments failed", r.URL.Path)
	h.RememberExchange(callID, r, raw, []byte("all deployments failed"))
	h.RecordSpend(w, p, callID, alias, op, nil, start, false, http.StatusBadGateway, "")
	util.WriteTypedError(w, r.URL.Path, 502, "upstream_error", "all deployments failed")
}
