// 本文件定义执行器消费的宿主接口、会话路由计划和日志备注；先读 RequestHost，再读 Adapted/Bypass 的增量能力。
// 调用关系与边界：数据面与网关之间的能力契约；具体参数、返回及副作用见各方法说明。
package contract

import (
	"net/http"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	"github.com/sunqirui1987/xhub/internal/config"
	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	"github.com/sunqirui1987/xhub/internal/router"
	"github.com/sunqirui1987/xhub/internal/util"
)

// SpendLog 定义一次调用的日志与计费边界，适配执行器和原生执行器共同使用。
// 实现由 gateway 提供；先按 callID 暂存交互及备注，再 RecordSpend 消费并清理，避免正文与费用串到另一请求。
// 接口只描述能力，不持有存储；实现可能写内存、Redis 队列或数据库，测试宿主只捕获断言所需事实。
type SpendLog interface {
	// RememberExchange 把这一次调用的请求头、请求体和响应体暂存到 callID 上。
	// 网关只在打开「在日志里保存请求内容」时才真正落库。
	//
	// 参数 callID：本次请求的 x-litellm-call-id。r：入站请求，用来抄方法和头。
	// 参数 reqBody：原始请求字节。respBody：准备写入日志的响应字节。查询没有正文时传 nil。
	// 返回：无。
	// 调用：Serve 的拒绝、缓存命中、流式成功、非流式成功；ServeBypass 的创建和查询。
	// 测试：gateway/prompt_log_test.go 覆盖 promptJSON 的落库形状。测试宿主按需要捕获交互或提供空实现，并不写真实数据库。
	RememberExchange(callID string, r *http.Request, reqBody, respBody []byte)

	// AnnotateCall 记下首字时间、供应商、缓存和部署。RecordSpend 会按 callID 取走这条备注。
	// 参数 callID：与 RememberExchange 相同。note：这一次要写进日志的事实。TTFTMs 为 nil 时该列留空。
	// 返回：无。
	// 调用：Serve 的缓存命中和成功路径；ServeBypass 在转发结束后。
	// 测试：bypass_logic_test.go 断言创建笔记里有供应商、TTFT 和部署 id。
	AnnotateCall(callID string, note CallNote)

	// ReleaseCall 释放本次 HTTP 调用未被记账消费的临时备注、交互和护栏事实。
	// 参数 callID 为本次调用 ID；无返回，不创建账单或删除已持久化日志。
	// 两类执行器在请求结束时调用；空 ID、不存在或已经消费的条目应安全忽略。
	ReleaseCall(callID string)

	// RecordSpend 写一条用量。它读取并清掉 callID 上的暂存，计算费用，更新响应头里的 cost。
	// 缓存命中或状态码大于等于 400 时，token 仍保留，扣费金额强制为 0。
	//
	// 参数 w：写 x-litellm-response-cost 等头。p：调用方，用来记用户、团队和密钥。
	// 参数 callID：取暂存。alias：对外模型名。op：call_type，聊天是 chat，Bypass 是「类型:动作」。
	// 参数 usage：prompt_tokens 和 completion_tokens。nil 当作空用量，费用为 0。
	// 参数 start：请求开始时间，用来算耗时。cacheHit：为 true 时不按 token 扣费。
	// 参数 status：写进日志的 HTTP 状态。depID：api_base|model，用来找部署上的单价。
	// 返回：无。有 Redis 时行进队列，否则当场写 PostgreSQL。
	// 调用：Serve、ServeBypass，以及 gateway WriteChatJSON、WriteCacheHit 内部。
	// 测试：prompt_log_test.go TestSpendLogRoundTripReturnsPromptPayload；bypass_logic_test.go 检查 call_type 和只扣一次。
	RecordSpend(w http.ResponseWriter, p *auth.Principal, callID, alias, op string, usage map[string]any, start time.Time, cacheHit bool, status int, depID string)
}

// RequestHost 定义两类执行器共有的身份、路由、凭据和 HTTP 能力。
// gateway 实现此接口，使数据面不依赖具体 Server、身份库或缓存布局；方法的拒绝响应与返回值约定见各项说明。
// 执行器必须先鉴权、再校验每个实际目标；读到偏好或能力匹配都不等于获得调用权限。
type RequestHost interface {
	// RequireLLMPrincipal 解析可以发起推理的身份。失败时响应已经写成 401，并返回 nil。
	// 参数 w：写 401。r：从 Authorization 或会话 cookie 取身份。
	// 返回：调用方。nil 表示未授权。
	// 调用：Serve 开头。测试：failure_log_test.go 的 logHost 固定返回一个会话身份。
	RequireLLMPrincipal(w http.ResponseWriter, r *http.Request) *auth.Principal
	// ResolveRequest 在重试前再解析一次身份，确认密钥没有在这次请求中途失效。
	// 参数 r：同一入站请求。
	// 返回：新的身份。密钥被撤销或过期时返回错误，Serve 中止重试。
	// 调用：Serve 在 num_retries 大于 0 且第一次尝试之后。测试夹具返回固定身份。
	ResolveRequest(r *http.Request) (*auth.Principal, error)
	// EnforceIdentityLimits 校验当前模型/组允许名单、预算链，拒绝时由实现写好响应。
	// 参数 w/path 指定写入器和错误包络，p 为已鉴权主体，alias 为本次实际检查的公开目标，est 为输入文本的启发式 token 估算，并非严格上界。
	// 返回 true 表示继续，false 表示执行器立即终止；首次请求、重试和缓存中记录的实际目标都需要检查，不更新限流热计数。
	EnforceIdentityLimits(w http.ResponseWriter, path string, p *auth.Principal, alias string, est int) bool
	// AdmitRequest 首次校验身份、模型和预算，并对本次入站请求执行一次 RPM/TPM 准入。
	// 参数为响应、路径、主体、公开目标及估算 token；返回是否放行，拒绝已写响应。
	// 成功占用分钟容量；后续目标切换、缓存目标和重试使用只读 EnforceIdentityLimits。
	AdmitRequest(w http.ResponseWriter, path string, p *auth.Principal, alias string, est int) bool
	// GatewayConfig 返回进程配置。Serve 用它的模型表；路由策略和重试次数改读 RouteSettingsFor。
	// 参数：无。
	// 返回 *config.Config（*config.Config）：当前进程配置，含模型表和路由策略，不会复制。
	// 调用：dataplane/execution/official.go、dataplane/execution/serve.go、gateway/wire.go
	// 测试：bypass_logic_test.go、failure_log_test.go
	GatewayConfig() *config.Config
	// RouteSettingsFor 返回这一次请求生效的路由设置：策略、重试次数、超时和冷却阈值。
	//
	// 调用方选中的模板由身份链在预算检查时解析出来，所以这里不遍历、不额外查询。
	// 没有任何一层选中模板时返回平台默认那一份，行为和这个功能存在之前一样。
	//
	// 参数 p（*auth.Principal）：已经解析的调用方，含预算链填入的模板选择。
	// 返回 router.RouteSettings（router.RouteSettings）：这次请求生效的设置和来源。
	// 调用：dataplane/execution/serve.go
	// 测试：route_settings_test.go
	RouteSettingsFor(p *auth.Principal) router.RouteSettings
	// RouteState 返回这一刻的冷却、延迟、用量和正在处理的请求数。没有 Redis 时只有 Busy。
	// 参数：无。
	// 返回 router.State（router.State）：这一刻的冷却、延迟、用量和并发，交给路由器排序。
	// 调用：dataplane/execution/official.go、dataplane/execution/serve.go、gateway/wire.go
	// 测试：bypass_logic_test.go、failure_log_test.go
	RouteState() router.State
	// PlanRoute 在选部署之前解析会话，并读取动态部署偏好或固定父响应归属。
	// 参数 r：读会话头。alias：对外模型名。body：读标准 session_id、父响应和稳定 prompt。p：用来隔离租户。
	// 返回：RoutePlan。Pinned 非空时两类执行器验证当前资格后优先复用；Required 只允许父响应原目标。
	// 调用：Serve。测试：gateway/affinity/affinity_test.go。
	PlanRoute(r *http.Request, alias string, body map[string]any, p *auth.Principal) RoutePlan
	// CommitRoute 把这次真正用到的部署记到会话上，供下一轮继续打到同一上游。
	// 参数 plan：PlanRoute 的结果。deploymentID：稳定部署 ID。responseID：响应里的 id，可空。
	// 返回：无。
	// 调用：Serve 在流式或非流式成功之后。测试：affinity_test.go TestConcurrentClaimAndLateCommit。
	CommitRoute(plan RoutePlan, deploymentID, responseID string)
	// AttachCredential 按部署上的凭据名补上 api_key 和 api_base。没有凭据名时原样返回。
	// 参数 dep：路由选出的一行模型。
	// 返回：填好密钥的副本。凭证库不可用或凭证无效时返回错误，Serve 跳过该部署。
	// 调用：Serve 每个部署一次；ServeBypass 在创建和查询时。测试夹具原样返回 dep。
	AttachCredential(dep config.ModelEntry) (config.ModelEntry, error)
	// HTTPClient 返回用于访问上游的共享客户端；无参数，返回指针应按只读配置使用。
	// 执行器复制客户端并应用本次请求模板的上下文超时，避免改写全局 Timeout 或让启动时超时覆盖请求策略。
	// 方法本身不发送请求，执行器负责响应正文关闭和取消每次尝试上下文。
	HTTPClient() *http.Client
	// NoteFailure 按本次请求生效的策略记录一次部署失败，可能触发冷却。
	// 参数 id 为 CooldownID 运行身份，settings 为此次解析的模板/模型设置；无返回。
	// 执行器在网络、429/5xx 或协议失败时调用；宿主委托 runtime.RecordFailure，不能替换为进程全局阈值。
	NoteFailure(id string, settings router.RouteSettings)
}

// Adapted 是统一推理适配入口所需的宿主能力，由 execution.Serve 消费。
// 在 RequestHost 上增加护栏、响应缓存和响应写入；同时嵌入 SpendLog 统一结算。
// 不提供原生任务归属，防止统一对话执行器意外耦合供应商任务生命周期。
type Adapted interface {
	// NativeResponsesAllowed 根据可信用户读取默认关闭的灰度快照；配置读取失败返回 false。
	NativeResponsesAllowed(userID string) bool
	// 嵌入 RequestHost 的公共能力；执行器通过同一宿主消费，不能省略其中的鉴权或记账约定。
	RequestHost

	// GuardrailBlocks 在支持的统一对话请求发送前执行护栏，也允许脱敏规则原地修改正文。
	// 参数 callID 关联本轮调用，body 为已恢复授权历史的请求对象；返回 blocked 和说明 msg。
	// Serve 对 chat/responses/messages/gemini/vertex/空操作调用，blocked 时由 Serve 写拒绝响应并记账，本方法不表示已经写出 HTTP。
	// 允许时仍须重新序列化 body，让缓存、日志和编码器消费同一份脱敏后输入。
	// native 可选参数来自可信灰度选择，要求未知护栏内容失败关闭。
	GuardrailBlocks(callID string, body map[string]any, native ...bool) (bool, string)

	// ResponseCache 返回进程内 LRU 响应缓存，正文及实际授权模型/组存放在同一条目，不访问 Redis。
	// 参数：无。
	// 返回 *util.DualCache（*util.DualCache）：响应缓存。未命中时 Get 返回空。
	// 调用：dataplane/execution/serve.go、gateway/wire.go
	// 测试：bypass_logic_test.go、failure_log_test.go
	ResponseCache() *util.DualCache

	// IncBusy 增加一次实际 HTTP 尝试的在途计数。
	// 参数 id 为部署当前 CooldownID 运行身份；无返回，Serve 发送前调用，RouteState 的 Busy 使用同一身份。
	// 每次调用必须在成功、拨号失败、读取失败和转换失败等路径配对 DecBusy；不是稳定 deployment_id 的计数。
	IncBusy(id string)

	// DecBusy 释放实际 HTTP 尝试占用的在途计数。
	// 参数 id 与对应 IncBusy 相同，为 CooldownID 运行身份；无返回，执行器在每次尝试结束时调用。
	// 不能只在成功路径释放，否则失败部署会一直表现为忙碌。
	DecBusy(id string)

	// NoteLatency 记下从这个请求开始到响应头的毫秒数。
	// 参数 id：部署 id。ms：从请求开始到响应头的毫秒数。返回：无。
	// 调用：Serve 在收到上游响应头之后。
	// 测试：无直接单测
	NoteLatency(id string, ms float64)

	// SetChatHeaders 给聊天响应写上调用 id、对外模型名和本次上游地址。
	// 参数 w：响应。p：调用方，用来决定哪些头可以暴露。alias：对外模型名。apiBase：本次上游根地址。
	// 返回：无。
	// 调用：Serve 非流式成功，以及流式开始写出之前。
	// 测试：无直接单测
	SetChatHeaders(w http.ResponseWriter, p *auth.Principal, alias, apiBase string)

	// WriteCacheHit 把缓存正文写回调用方，并记一条扣费为 0 的用量。token 数仍保留。
	//
	// 参数 w、p、callID、alias：与 RecordSpend 相同。ck：缓存键。op：操作名。
	// 参数 hit：缓存的响应字节。start：请求开始时间。
	// 返回：无。响应已写完。
	// 调用：Serve 在 ResponseCache.Get 命中时。测试：TestServeLogsCacheHitAndStreamMetrics。
	WriteCacheHit(w http.ResponseWriter, p *auth.Principal, callID, alias, ck, op string, hit []byte, start time.Time)

	// WriteChatJSON 输出同步响应并执行用量记录和允许的响应缓存写入。
	// 参数 w/p/callID/alias 指定交互与调用方，ck/op/provider 为缓存、操作及供应商标记，respBody/status 为最终正文及 HTTP 状态，start 为开始时间，depID 为运行部署身份。
	// models 为实际执行涉及的模型或组范围，缓存命中时须逐项复核权限；返回值无，Serve 同步结束路径调用。
	// 非成功状态不得当作成功响应缓存；与流式路径自行写字节和 RecordSpend 的职责不同。
	WriteChatJSON(w http.ResponseWriter, p *auth.Principal, callID, alias, ck, op, provider string, respBody []byte, status int, start time.Time, depID string, models ...string)

	// 嵌入 SpendLog 的公共能力；执行器通过同一宿主消费，不能省略其中的鉴权或记账约定。
	SpendLog
}

// Bypass 是原生 API 转发入口所需的宿主能力，由 execution.ServeBypass 消费。
// 在 RequestHost 上增加部署查找、任务归属及创建时计价事实，并通过 SpendLog 记录每次交互。
// 原生载荷直接按登记传输发送，不经过统一护栏或响应缓存；任务键必须已隔离调用方与传输。
type Bypass interface {
	// 嵌入 RequestHost 的公共能力；执行器通过同一宿主消费，不能省略其中的鉴权或记账约定。
	RequestHost

	// Models 返回当前原生调用可读取的全部部署目录；无参数，返回切片由实现持有，执行器只读使用。
	// ServeBypass 进一步校验停用、端点、策略、权限和凭据，返回目录不代表任意条目都可执行。
	Models() []config.ModelEntry

	// FindDeployment 按钉住的部署 id 找回那条模型配置。进程里已经没有这行时返回假。
	// 参数 id：PinOfficial 存下来的值。
	// 返回：找到时 ok 为 true。进程里已经没有这行时 ok 为 false，查询回 404。
	// 调用：serveBypassFollow。测试：bypass_logic_test.go。
	FindDeployment(id string) (config.ModelEntry, bool)

	// PinOfficial 保存原生任务到创建部署的归属。
	// 参数 taskID 实际为调用方与登记传输隔离后的任务范围键，deploymentID 为当前 CooldownID 运行身份；无返回。
	// 创建取得有效任务 ID 后调用，网关实现为临时缓存并设七天 TTL；不得把裸供应商任务 ID 当作全局缓存键。
	PinOfficial(taskID, deploymentID string)

	// OfficialDeployment 读取原生任务创建时的部署归属。
	// 参数 taskID 为已隔离调用方与传输的范围键；返回运行部署 ID，缺失或过期返回空串。
	// 后续操作调用，执行器再校验部署存在且端点匹配，空值返回 404，不自动猜测或迁移账户。
	OfficialDeployment(taskID string) string
	// PinOfficialContext 保存原生任务创建时的请求身份、开始时间及计价事实。
	// 参数 taskID 为已隔离的任务范围键，facts 为 TaskContext；无返回，创建成功后与部署归属使用同一范围保存。
	// 后续轮询恢复这些事实，不能以轮询的空正文重新推断原任务规格；持久性及 TTL 由宿主实现负责。
	PinOfficialContext(taskID string, facts providercontract.TaskContext)
	// OfficialContext 读取原生任务创建时的计价与生命周期事实。
	// 参数 taskID 为已隔离的任务范围键；返回 TaskContext，缺失返回零值。
	// 查询和最终结算调用；不得把本轮 callID 覆写成原请求 ID，原请求关联与轮询日志应分别保存。
	OfficialContext(taskID string) providercontract.TaskContext

	// 嵌入 SpendLog 的公共能力；执行器通过同一宿主消费，不能省略其中的鉴权或记账约定。
	SpendLog
}

// RoutePlan 保存选部署之前生成的请求私有会话计划，由 affinity 产生、两类执行器消费。
// 普通会话偏好执行前重新验证；Required 父响应归属只能回原目标，失效必须拒绝。
// Claim/Save 协调缓存版本而非锁住上游请求；History 只能来自已授权的临时续接上下文，不授予额外权限。
type RoutePlan struct {
	// MessagesHistory 只保存 affinity 在当前身份、别名、会话与连接范围验证过的原生助手历史。
	// Responses 客户端引用仅是查询标识，不是授权；执行器解析完整历史时使用，不能由请求正文直接填充。
	MessagesHistory map[string]llmdialogue.MessagesReplay
	// PrepareMessages 在完整原生 Messages 回复发布前保存签名历史并生成 Responses 引用。
	// 参数是已绑定真实连接的目标及已校验回复；返回公开投影或持久化错误。失败不得发布可重放完成项。
	PrepareMessages func(RouteTarget, llmdialogue.DialogueResult) (llmdialogue.DialogueResult, error)
	// CommitMessagesHistory 在发布 Messages 桥接终态前确认父响应历史与归属持久化。
	// 参数为实际目标、响应 ID 和公开完整历史；失败必须中止本轮，不重复付费调用。
	CommitMessagesHistory func(RouteTarget, string, []llmdialogue.Turn) error
	// SessionID 的识别来源，不是供应商来源或路由策略。由 PlanRoute 生成，经 CallNote 写入日志。
	// explicit：标准 X-Session-Id/正文 session_id；parent：授权父响应继承；
	// cache_key：prompt_cache_key 哈希；prompt：稳定首轮内容指纹；generated：随机 UUID。
	// cache_key/prompt 在日志中标为推断；不参与权限判断。空值表示未记录来源；本字段通过日志载体序列化。
	SessionSource string
	// 本轮读取或实际执行的部署、模型池及回退路径；执行前重新验证当前配置中的资格。
	Target RouteTarget
	// 无偏好时原子申请候选并返回并发赢家；nil 表示不协调。只协调缓存，不锁上游执行。
	Claim func(RouteTarget) RouteTarget
	// 成功后比较更新本轮读取的版本并续期；nil 表示不写偏好，迟到结果不能覆盖已变化的记录。
	Save func(RouteTarget)
	// 调用方、请求模型和 responses 入口隔离的一小时临时上下文；执行器追加本轮输入和成功结果，不是原厂长期 store。
	History []llmdialogue.Turn
	// 能力目录中的入口标识，作为缓存范围隔离不同协议；不是供应商地址。
	Endpoint string
	// 父响应已确定原部署时为 true；只允许该部署，不能进入普通跨目标回退。
	Required bool
	// 身份冲突、非法信号或父响应/上下文不可用等计划错误，执行器必须在上游调用前拒绝。
	Err error
	// 客户端请求的公开模型或路由组；回退时保持原值，用作动态偏好范围。
	Alias string
	// 明确、继承、推断或生成的会话标识，用于响应头和授权范围内日志归组；不是响应 ID，不授予历史读取权限。
	SessionID string
	// 鉴权生成的 key:<hash> 或 user:<id>，用于隔离调用方；客户端不能指定，空值不建立偏好。
	Caller string
	// 候选稳定 deployment_id，执行器从当前目录转换为运行 ID；普通请求优先复用，Required 请求只允许该目标。
	Pinned string
}

// RouteStep 描述从公开模型或组到下一目标的一条回退边。
// 由请求回退队列记录，保存进会话偏好后每轮依当前模板重新验证；JSON 字段用于缓存载体。
// 它记录实际路径，不证明目标已授权，也不允许绕过当前禁用开关。
type RouteStep struct {
	// 回退失败源的公开模型或组；没有显式组链时可以是实际成员名。
	From string `json:"from"`
	// 对应错误类别回退列表中的目标公开模型或组，发送前仍检查权限、能力与预算。
	To string `json:"to"`
	// 回退类别：general（429/5xx/可重放网络错误）、context（上下文超限）、content（上游内容拒绝）。
	Kind string `json:"kind"`
}

// RouteTarget 描述可被会话偏好保存的部署、实际公开模型池及回退路径。
// 普通偏好按稳定 DeploymentID 重新定位，固定续接另校验 ConnectionID；Revision 用于比较更新防止迟到覆盖。
// Path 需由生产方复制，不应在不同请求间共享可变切片；归属是临时缓存快照，不是长期数据库记录。
type RouteTarget struct {
	// 部署目录的稳定 ID，地址或定价变化不改变它；每轮从当前目录恢复执行配置，删除后失效。
	DeploymentID string `json:"deployment_id"`
	// 创建响应时已补齐凭据的连接指纹；固定续接必须匹配，缺失时拒绝并要求新会话。
	// 仅保存哈希，不包含地址或密钥明文；普通偏好仍按稳定部署 ID 重新选路。
	ConnectionID string `json:"connection_id,omitempty"`
	// 实际部署所属公开模型；组调用和回退时可以不同于客户端请求别名，上游型号另行保存。
	Model string `json:"model"`
	// 实际选中部署的公开模型池或显式组，决定当前策略、权重及回退源。
	Pool string `json:"pool"`
	// 原请求别名到实际模型池的有序回退边，每轮按当前模板验证；直接命中时为空，不能作为授权。
	Path []RouteStep `json:"path,omitempty"`
	// 每次缓存写入生成的随机版本，避免比较更新的 ABA 问题；不是时间戳或排序编号。
	Revision string `json:"revision"`
}

// CallNote 汇集一次调用的计时、会话、缓存、上游诊断与异步任务结算事实。
// 执行器用 AnnotateCall 按本次 callID 暂存，RecordSpend 随后取走并清理；字段为空表示本次未取得该事实。
// 轮询的 callID、创建请求的 TaskRequestID 与最终费用的 SettlementID 分别承担交互隔离、生命周期归属和结算去重。
type CallNote struct {
	// 上游连接快照由执行器在尝试调用时设置，重试以最后一次选择为准；仅保存公开身份。
	Supplier, APIBase, UpstreamModel string
	// 区分未选择部署与字段缺失的部署，防止后续业务附注覆盖执行快照。
	UpstreamSelected bool
	// 是否实际流式执行，供性能统计区分首字延迟与非流完整响应耗时。
	Streamed bool
	// 传输观察器产生的完整原始响应头和用量诊断，并未在此处脱敏；消费方负责过滤后输出，nil 表示未取得观察事实。
	Upstream *dpdiagnostics.UpstreamDiagnostics
	// 异步任务创建时的原请求 ID，轮询仍使用自己的 callID，供更新原任务日志。
	TaskRequestID string
	// 上游任务生命周期状态，用于更新创建日志，不决定同步重试策略。
	TaskStatus string
	// 是否为异步任务创建事件，创建成功时实际费用可能尚未确定。
	TaskInitial bool
	// 查询取得成功实测结果时为 true，通知账单原子结算原任务并防止重复收费。
	TaskSettlement bool
	// 按已隔离任务与运行部署构造的稳定最终费用标识，供持久层去重；与原请求 TaskRequestID 及本次轮询 callID 不同。
	SettlementID string
	// 查询不计入同步路由 TPM 时为 true，避免反复轮询放大 token 用量。
	SkipRouteUsage bool
	// 流请求的首字节或非流请求的完整响应耗时，单位毫秒；nil 表示没有测量。
	TTFTMs *int
	// 执行供应商/适配器标识，区别于具体供应商连接 ID。
	Provider string
	// 网关响应缓存键，与会话指纹、原生 prompt_cache_key 及偏好缓存键相互独立。
	CacheKey string
	// 是否返回网关响应缓存；命中仍记录用量但推理费用为 0，不提交上游成功偏好。
	CacheHit bool
	// 明确、继承、推断或生成的会话标识，用于响应头和授权范围内日志归组；不是响应 ID，不授予历史读取权限。
	SessionID string
	// SessionID 的识别来源，不是供应商来源或路由策略。由 PlanRoute 生成，经 CallNote 写入日志。
	// explicit：标准 X-Session-Id/正文 session_id；parent：授权父响应继承；
	// cache_key：prompt_cache_key 哈希；prompt：稳定首轮内容指纹；generated：随机 UUID。
	// cache_key/prompt 在日志中标为推断；不参与权限判断。空值表示未记录来源；本字段通过日志载体序列化。
	SessionSource string
}
