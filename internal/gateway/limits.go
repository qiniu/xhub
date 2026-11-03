// limits.go is the gate in front of dataplane.Serve. dataPlane is the wrapper
// the catalog handlers call. Budget, RPM, and TPM checks live in this file
// because they need the key, the team, and Redis. The send loop does not.

package gateway

import (
	"context"
	"errors"
	"maps"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/authz"
	"github.com/sunqirui1987/xhub/internal/config"
	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	dpexecution "github.com/sunqirui1987/xhub/internal/dataplane/execution"
	modelaccess "github.com/sunqirui1987/xhub/internal/gateway/models"
	"github.com/sunqirui1987/xhub/internal/iam"
	"github.com/sunqirui1987/xhub/internal/live"
	credentials "github.com/sunqirui1987/xhub/internal/llm/credentials"
	providerdeployment "github.com/sunqirui1987/xhub/internal/provider/deployment"
	"github.com/sunqirui1987/xhub/internal/util"
)

var logTraceOnceLimits sync.Once

// dataPlane hands this inference call to dataplane.Serve. Identity, budget, and the upstream loop are not reimplemented in this wrapper.
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；r（*http.Request）：入站 HTTP 请求，用来读路径、头和正文；op（string）：操作名或 call_type，写入用量行并选择协议。
// 调用：gateway/ingress.go、gateway/wire.go
// 测试：dial_log_test.go、guardrail_block_test.go
// 返回：无。推理的状态码和正文由数据面写进 w，这里不再包一层。
func (s *Server) dataPlane(w http.ResponseWriter, r *http.Request, op string) {
	logTraceOnceLimits.Do(func() { util.Trace("enter gateway.dataPlane") })
	util.Debug("process %s %s step=dataplane op=%s", r.Method, r.URL.Path, op)
	dpexecution.Serve(s, w, r, op)
}

// withCredential fills deployment parameters from the credential store using the deployment's credential name. With no name the deployment is returned unchanged.
var (
	errCredentialUnavailable = errors.New("credential_unavailable")
	errCredentialInvalid     = errors.New("credential_invalid")
)

// withCredential 读取当前连接目录并校验模型能力，再补上 api_key 和 api_base。
// 没有连接时使用部署的内联目录；元数据独立复制，不写入上游参数。
// 参数 dep（config.ModelEntry）：含上游模型、执行传输、连接名或内联连接的部署。
// 返回：填好 api_key 和 api_base 的副本。凭证库不可用或凭证无效时返回错误。
// 调用：gateway/wire.go
// 测试：credential_catalog_test.go、regression/supplier_catalog_test.go。
func (s *Server) withCredential(dep config.ModelEntry) (config.ModelEntry, error) {
	name := dep.ParamString("litellm_credential_name", "")
	var values map[string]any
	catalogID, _ := dep.ModelInfo["catalog_id"].(string)
	if name != "" {
		if s.Store == nil {
			return dep, errCredentialUnavailable
		}
		rec, err := s.Store.GetKV("credentials", name)
		if err != nil {
			util.Error("credential lookup failed name=%s err=%v", name, err)
			return dep, errCredentialUnavailable
		} else {
			meta, _ := rec["credential_info"].(map[string]any)
			catalogID, _ = meta["catalog_id"].(string)
			values, _ = rec["credential_values"].(map[string]any)
			if values == nil {
				util.Error("credential invalid name=%s reason=missing credential_values", name)
				return dep, errCredentialInvalid
			}
		}
	}
	// 每次请求读取当前目录，防止连接编辑后沿用旧能力；元数据不进入上游参数。
	if err := providerdeployment.ValidateCatalogBinding(catalogID, dep); err != nil {
		return dep, errCredentialInvalid
	}
	out := dep
	out.ModelInfo = maps.Clone(dep.ModelInfo)
	if out.ModelInfo == nil {
		out.ModelInfo = map[string]any{}
	}
	out.ModelInfo["catalog_id"] = catalogID
	out.LiteLLMParams = credentials.Hydrate(dep.LiteLLMParams, values)
	return out, nil
}

// enforceIdentityLimits 为数据面校验主体状态、模型权限和预算。
// 参数 w/path 为错误响应及请求路径，p 为已鉴权主体，alias 为公开模型名，est 为估算请求 token 数。
// 返回是否允许推理；拒绝时已写出错误响应。预算合并已落库与尚未落库消费，指明耗尽层级。
// 空预算仅取消当前层上限，无团队时不限额，有团队时继续沿额度树检查；不会免除记账或模型权限。
// 调用场景：gateway/wire.go 的会话及密钥推理入口；重复复核不消费速率窗口，预算检查失败不会调用上游。
// 测试：TestTeamlessIdentityAdmission 和真实 HTTP 预算/速率回归。
func (s *Server) enforceIdentityLimits(w http.ResponseWriter, path string, p *auth.Principal, alias string, est int) bool {
	return s.checkIdentityLimits(w, path, p, alias, est, false)
}

// checkIdentityLimits 校验身份、模型及最新预算；initial 表示本次入站首次准入。
// 其余参数与 enforceIdentityLimits 相同，返回是否放行；首次共用额度树生成速率计划并只占一次分钟容量。
// 执行器复核目标时 initial=false，不保存跨请求快照或重复计数；拒绝已写入响应。
func (s *Server) checkIdentityLimits(w http.ResponseWriter, path string, p *auth.Principal, alias string, est int, initial bool) bool {
	if s.IAM == nil {
		util.WriteTypedError(w, path, http.StatusServiceUnavailable, "authz_unavailable", "authorization is temporarily unavailable")
		return false
	}
	ctx := context.Background()
	if p.Kind == authz.KindSession {
		user, err := s.IAM.GetUser(ctx, p.UserID)
		if err != nil {
			util.WriteTypedError(w, path, 401, "invalid_request_error", "user not found")
			return false
		}
		if !user.Active() {
			util.WriteTypedError(w, path, 401, "invalid_request_error", "user not found or blocked")
			return false
		}
		if overBudget(user.MaxBudget, user.Spend, s.hotSpendRef("user", user.ID, userPeriod(user))) {
			util.WriteTypedError(w, path, http.StatusForbidden, "budget_exceeded", "User budget has been exceeded")
			return false
		}
		// A session has no key row, so the chain that resolves a template for a
		// key does not run for it. The console's playground is a session caller,
		// and it would otherwise always route by the platform default no matter
		// what its team selected.
		if err := s.resolveSessionRouteTemplate(ctx, p); err != nil {
			util.WriteTypedError(w, path, 503, "unavailable", "template binding unavailable")
			return false
		}
	}
	if p.Key != nil {
		if err := s.keyBudgetOK(ctx, p); err != nil {
			budgetRefusal(w, path, err)
			return false
		}
	}
	// 所有入口沿同一额度树校验，共享主体不能消费兄弟的保留额度。
	kind, id := "user", p.UserID
	if p.Key != nil {
		kind, id = "key", p.KeyID
	}
	var team, org, scope string
	var err error
	var plan live.RatePlan
	if initial {
		team, org, scope, plan, err = s.IAM.QuotaAndRates(ctx, kind, id, s.hotSpendRef)
	} else {
		team, org, scope, err = s.IAM.QuotaPath(ctx, kind, id, s.hotSpendRef)
	}
	if err != nil {
		budgetRefusal(w, path, err)
		return false
	}
	p.BillingTeamID, p.BillingOrgID = team, org
	// 预算仅作为消费上限：无团队且个人预算留空时不限额，有团队时仍继承额度树约束。
	// 会话和个人密钥共用 QuotaPath，有限预算（含零）照常拒绝，消费仍记入个人。
	if scope != "" {
		names := map[string]string{"user": "User", "key": "Key", "team": "Team", "org": "Organization"}
		budgetRefusal(w, path, errBudget{scope: names[scope]})
		return false
	}
	if team != "" {
		t, err := s.IAM.GetTeam(ctx, team)
		if err != nil || t.Status != iam.StatusActive {
			budgetRefusal(w, path, errKeyUnusable)
			return false
		}
		o, err := s.IAM.GetOrg(ctx, org)
		if err != nil || o.Status != iam.StatusActive {
			budgetRefusal(w, path, errKeyUnusable)
			return false
		}
	}
	if alias != "" && !modelaccess.AllowsModel(s, ctx, p, p.TeamID, alias) {
		util.WriteTypedError(w, path, 401, "invalid_request_error", "model not in allowed model list")
		return false
	}
	return !initial || s.admitRateLimits(w, path, plan, est)
}

// resolveSessionRouteTemplate 为控制台推理解析会话的唯一团队及继承模板。
// 参数 ctx 为读取上下文、p 为会话主体；返回身份链读取错误，成功补齐主体归属。
// 调用：enforceIdentityLimits；零或多团队不猜测归属，不消耗速率窗口。
// 存储失败必须让调用方停止推理，防止错误地使用另一套路由规则。
func (s *Server) resolveSessionRouteTemplate(ctx context.Context, p *auth.Principal) error {
	return s.resolvePreviewIdentity(ctx, p)
}

// selectRouteTemplate 选择最窄的非空模板；参数 p 为主体、id 为范围选择、source 为来源。
// 返回无；预算链和只读预览共用，空白继续继承，已有选择不覆盖，无外部副作用。
func selectRouteTemplate(p *auth.Principal, id *string, source string) {
	if p.RouteTemplateID != "" || id == nil {
		return
	}
	ids := make([]string, 3)
	for i, scope := range []string{"key", "team", "organization"} {
		if scope == source {
			ids[i] = *id
		}
	}
	selected, selectedSource := iam.SelectRouteTemplate(ids...)
	if selected != "" {
		p.RouteTemplateID, p.RouteTemplateSource = selected, selectedSource
	}
}

// resolvePreviewIdentity 只读补齐预览主体的团队、组织及继承模板，与推理共用模板选择规则。
// 参数 ctx 为读取上下文、p 为鉴权主体；返回身份链读取错误，零或多团队会话使用默认。
// 调用：路由预览；不检查预算、不消耗速率窗口，密钥与用户使用相同的当前团队。
func (s *Server) resolvePreviewIdentity(ctx context.Context, p *auth.Principal) error {
	if s.IAM == nil {
		return errors.New("identity store unavailable")
	}
	p.RouteTemplateID, p.RouteTemplateSource, p.OrgID = "", "", ""
	if p.Kind == authz.KindSession {
		p.TeamID = ""
		memberships, err := s.IAM.MemberTeams(ctx, p.UserID)
		if err != nil {
			return err
		}
		if len(memberships) > 1 {
			return iam.ErrMultipleTeams
		}
		if len(memberships) == 0 {
			return nil
		}
		p.TeamID = memberships[0].TeamID
	} else if p.KeyID != "" {
		k, err := s.IAM.GetKey(ctx, p.KeyID)
		if err != nil {
			return err
		}
		p.TeamID = k.TeamID
		selectRouteTemplate(p, k.RouteTemplateID, "key")
	}
	if p.TeamID == "" {
		return nil
	}
	team, err := s.IAM.GetTeam(ctx, p.TeamID)
	if err != nil {
		return err
	}
	p.OrgID = team.OrganizationID
	selectRouteTemplate(p, team.RouteTemplateID, "team")
	org, err := s.IAM.GetOrg(ctx, p.OrgID)
	if err != nil {
		return err
	}
	selectRouteTemplate(p, org.RouteTemplateID, "organization")
	return nil
}

// keyBudgetOK 沿密钥归属链检查实时额度；密钥检查状态及独立上限，enforceIdentityLimits 统一检查额度树及个人唯一团队。
// 无团队时保留密钥选择的路由模板，否则使用平台默认；指定的父级丢失仍返回错误，不能跳过额度。
// 同时记录团队所属组织及路由模板；按密钥、团队、组织顺序选择第一个明确配置，复用额度查询避免重复读取。
// 参数 ctx（context.Context）：上下文，取消或超时时停止后续工作；p（*auth.Principal）：已经解析的调用方，含用户、团队和密钥。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：仅在 limits.go 内使用
// 测试：limits_personal_test.go、regression/teamless_keys_test.go。
func (s *Server) keyBudgetOK(ctx context.Context, p *auth.Principal) error {
	k, err := s.IAM.GetKey(ctx, p.KeyID)
	if err != nil {
		return errKeyGone
	}
	p.Key = k
	p.TeamID = k.TeamID
	if k.Status != iam.StatusActive {
		return errKeyUnusable
	}
	if k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt) {
		return errKeyUnusable
	}
	// The key is the narrowest scope, so its selection is tried first and the
	// levels below only fill in when it names none.
	p.RouteTemplateID, p.RouteTemplateSource = "", ""
	selectRouteTemplate(p, k.RouteTemplateID, "key")
	if k.UserID != nil {
		owner, err := s.IAM.GetUser(ctx, *k.UserID)
		if err != nil {
			return errKeyGone
		}
		if !owner.Active() {
			return errKeyUnusable
		}
		if overBudget(owner.MaxBudget, owner.Spend, s.hotSpendRef("user", owner.ID, userPeriod(owner))) {
			return errBudget{scope: "User"}
		}
	}
	keyHot := 0.0
	// 无限额 Key 不依赖自身累计费用；父级限制仍由额度树检查。
	if k.MaxBudget != nil {
		keyHot = s.hotSpendRef("key", k.ID)
	}
	if math.IsNaN(keyHot) {
		return iam.ErrQuotaUnavailable
	}
	if overBudget(k.MaxBudget, k.Spend, keyHot) {
		return errBudget{scope: "Key"}
	}
	if k.ProjectID != nil && *k.ProjectID != "" {
		project, err := s.IAM.GetProject(ctx, *k.ProjectID)
		if err != nil {
			return errKeyGone
		}
		if overBudget(project.MaxBudget, project.Spend, s.hotSpendRef("project", project.ID)) {
			return errBudget{scope: "Project"}
		}
	}
	if k.TeamID == "" {
		if k.OwnerType != iam.OwnerPersonal || k.UserID == nil || k.ProjectID != nil {
			return errKeyUnusable
		}
		return nil
	}
	team, err := s.IAM.GetTeam(ctx, k.TeamID)
	if err != nil {
		return errKeyGone
	}
	if overBudget(team.MaxBudget, team.Spend, s.hotSpendRef("team", team.ID)) {
		return errBudget{scope: "Team"}
	}
	p.OrgID = team.OrganizationID
	// The team is the next scope up. A key that named no template leaves the
	// field empty, so this only takes effect when the key named none.
	selectRouteTemplate(p, team.RouteTemplateID, "team")
	org, err := s.IAM.GetOrg(ctx, team.OrganizationID)
	if err != nil {
		return errKeyGone
	}
	if overBudget(org.MaxBudget, org.Spend, s.hotSpendRef("org", org.ID, organizationPeriod(org))) {
		return errBudget{scope: "Organization"}
	}
	// And the organization is the widest scope in the chain.
	selectRouteTemplate(p, org.RouteTemplateID, "organization")
	return nil
}

// errKeyGone and errKeyUnusable separate "the credential no longer resolves" from
// "a scope is over budget", because only the first is the caller's problem.
var (
	errKeyGone     = errors.New("key not found")
	errKeyUnusable = errors.New("key blocked or expired")
)

// errBudget names the exhausted scope. The message mirrors LiteLLM's wording,
// which keeps the "<Scope> budget has been exceeded" text the console shows.
type errBudget struct{ scope string }

// 实现 error 接口，返回写进日志或 HTTP 错误体的文本。
// 参数：无。
// 返回 string（string）：error 接口的文本，给日志和 HTTP 错误体使用。
// 调用：预算检查在某个范围用尽时返回它，budgetRefusal 读取这段文本。
// 测试：无直接单测
func (e errBudget) Error() string { return e.scope + " budget has been exceeded" }

// budgetRefusal 将额度链失败写为客户端错误响应。
// 参数 w（http.ResponseWriter）：调用方的 HTTP 响应，状态码和正文写在这里；path（string）：预算Refusal要定位的路径。可能是 URL，也可能是字段路径；err（error）：非 nil 的失败原因。
// 返回：无。预算用尽时写 403 和 JSON 错误。
// 调用：身份额度链检查及 budget.go 的预算准入；不修改账单。
// 测试：TestBudgetRefusalStatus 与真实后台额度回归。
func budgetRefusal(w http.ResponseWriter, path string, err error) {
	if errors.Is(err, iam.ErrQuotaUnavailable) {
		util.WriteTypedError(w, path, 503, "quota_unavailable", "pending quota usage is temporarily unavailable")
		return
	}
	var over errBudget
	if errors.As(err, &over) {
		util.WriteTypedError(w, path, http.StatusForbidden, "budget_exceeded", over.Error())
		return
	}
	util.WriteTypedError(w, path, 401, "invalid_request_error", err.Error())
}

// overBudget reports a scope that has reached its ceiling. Without a ceiling the scope is unlimited, and stored plus hot spend is what the caller has used.
// 参数 ceiling（*float64）：over预算使用的float64；spent（float64）：over预算使用的小数。0 表示没有费用或尚未计价；hot（float64）：over预算使用的小数。0 表示没有费用或尚未计价。
// 返回 bool（bool）：这个主体的花费已经达到上限时返回真。没有上限时视为不限。
// 调用：仅在 limits.go 内使用
// 测试：无直接单测
func overBudget(ceiling *float64, spent, hot float64) bool {
	return ceiling != nil && spent+hot >= *ceiling
}

// 读取这个主体还没刷进数据库的热花费。没有 Redis 时为 0。
// 参数 kind（string）：分类名，用来选择限额主体、日志类型或官方端点；id（string）：热花费引用使用的主键。空串表示调用方没有指定记录。
// 可选 since 为组织当前周期起点；其他层级或未设置周期继续使用累计花费。
// 返回金额；Key 使用稳定行 ID，读取失败以 NaN 传给额度树并转为 503，不能按零费用放行。
// 调用：仅在 limits.go 内使用
// 测试：无直接单测
func (s *Server) hotSpendRef(kind, id string, since ...time.Time) float64 {
	if s.Live == nil {
		return 0
	}
	if kind == "key" {
		amount, err := s.Live.HotKeySpend(id)
		if err != nil {
			return math.NaN()
		}
		return amount
	}
	if kind == "user" && len(since) > 0 && !since[0].IsZero() {
		return s.Live.HotUserSpendSince(id, since[0])
	}
	if kind == "org" && len(since) > 0 && !since[0].IsZero() {
		return s.Live.HotSpendSince(id, since[0])
	}
	return s.Live.HotSpend(live.SpendRef(kind, id))
}

// admitRateLimits 原子检查并记录已生成的分钟计划；参数为响应、路径、计划和 token 估算。
// 返回是否放行；首次准入调用，超限 429 不计数，存储失败 503，不调用上游。
func (s *Server) admitRateLimits(w http.ResponseWriter, path string, plan live.RatePlan, est int) bool {
	var err error
	var rejected string
	if s.Live != nil {
		rejected, err = s.Live.AdmitRatePlan(plan, est, time.Now())
	} else {
		rejected = s.admitLocalRatePlan(plan, est, time.Now())
	}
	if err != nil {
		util.WriteTypedError(w, path, 503, "rate_limit_unavailable", "rate limiting is temporarily unavailable")
		return false
	}
	if rejected != "" {
		// 保存本次已用于判定的配置，不再次查询或消耗窗口；没有计数快照时不伪造使用量。
		scopes := make([]map[string]any, 0, len(plan.Path))
		for _, index := range plan.Path {
			n := plan.Nodes[index]
			scopes = append(scopes, map[string]any{"scope": n.Kind, "scope_id": n.ID, "rpm_limit": n.RPM, "tpm_limit": n.TPM})
		}
		dpdiagnostics.RecordEarlyFailure(w, &dpdiagnostics.UpstreamDiagnostics{RateLimit: map[string]any{"rejected": rejected, "effective_path": scopes, "window": "UTC calendar minute", "counter_snapshot": "not captured"}})
		util.WriteTypedError(w, path, 429, "rate_limit", rejected+" exceeded")
		return false
	}
	return true
}

// admitLocalRates 用互斥锁原子校验并记录全部层；参数为范围、预估和时钟，返回拒绝字段或空串。
// 调用：无 Redis 的推理入口；固定 UTC 分钟桶，先全量校验再计数，拒绝不占兄弟容量。
func (s *Server) admitLocalRates(scopes []live.RateScope, est int, now time.Time) string {
	plan := live.RatePlan{}
	for i, scope := range scopes {
		plan.Nodes = append(plan.Nodes, live.RateNode{RateScope: scope, Parent: -1})
		plan.Path = append(plan.Path, i)
	}
	return s.admitLocalRatePlan(plan, est, now)
}

// admitLocalRatePlan 原子校验父级分配和分钟用量；参数计划、估算、时钟，返回拒绝字段。
// 调用：无 Redis 推理入口；固定分配保留未用容量，先全量检查再更新调用路径，旧桶及时清理。
func (s *Server) admitLocalRatePlan(plan live.RatePlan, est int, now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rpmHits == nil {
		s.rpmHits = map[string][]time.Time{}
	}
	if s.tpmHits == nil {
		s.tpmHits = map[string][]tokHit{}
	}
	start := now.Truncate(time.Minute)
	est = max(0, est)
	for id, hits := range s.rpmHits {
		if len(hits) == 0 || hits[len(hits)-1].Before(start) {
			delete(s.rpmHits, id)
			delete(s.tpmHits, id)
		}
	}
	rpm, tpm := make([]int64, len(plan.Nodes)), make([]int64, len(plan.Nodes))
	for i, n := range plan.Nodes {
		rpm[i] = int64(len(s.rpmHits[n.ID]))
		for _, h := range s.tpmHits[n.ID] {
			tpm[i] += int64(h.n)
		}
	}
	if rejected := live.CheckRatePlan(plan, rpm, tpm, est); rejected != "" {
		return rejected
	}
	for _, i := range plan.Path {
		id := plan.Nodes[i].ID
		s.rpmHits[id] = append(s.rpmHits[id], now)
		s.tpmHits[id] = append(s.tpmHits[id], tokHit{t: now, n: est})
	}
	return ""
}

// organizationPeriod 返回组织最近预算起点；参数 org 为数据库快照，未充值返回零时刻。
// keyBudgetOK 调用，与额度树准入保持相同周期过滤，不写入存储。
func organizationPeriod(org *iam.Organization) time.Time {
	if org.BudgetPeriodStart != nil {
		return *org.BudgetPeriodStart
	}
	return time.Time{}
}

// userPeriod 返回个人预算起点；供身份准入过滤旧周期热账单，无周期时返回零值，不修改用户。
func userPeriod(u *iam.User) time.Time {
	if u != nil && u.BudgetPeriodStart != nil {
		return *u.BudgetPeriodStart
	}
	return time.Time{}
}
