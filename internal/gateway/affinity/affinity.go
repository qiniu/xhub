// Package affinity 管理跨请求的会话偏好、响应续接上下文及官方异步任务归属。
// 会话偏好可随配置和成功回退更新；响应与任务归属用于找回创建目标，不被普通动态选路替换。
// 阅读顺序：State/New 定义依赖，session.go 识别会话，route.go 构建与提交计划，cache.go 管理存储。
// 本包不负责鉴权、部署资格检查或费用计算，由网关及数据面执行链完成。
package affinity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/sunqirui1987/xhub/internal/auth"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	"github.com/sunqirui1987/xhub/internal/util"
)

// affinityTTL 为可丢弃路由偏好及无持久存储的测试状态保留一小时；生产续聊状态保存在数据库。
const affinityTTL = time.Hour

// responseContextKey 为统一 responses 的临时历史构造键，供 PlanRoute 读取、CommitRoute 发布。
// 参数 alias 为请求公开模型或组，caller 为鉴权范围，responseID 为响应 ID，endpoint 为协议入口。
// 返回与归属使用相同摘要、但命名空间为 history 的键，使隔离范围一致且不互相覆盖。
// 不读写缓存、不校验参数、不返回错误；有效期由缓存操作决定。
func responseContextKey(alias, caller, responseID, endpoint string) string {
	return strings.Replace(responsePinKey(alias, caller, responseID, endpoint), "session_route:v1:response:", "session_route:v1:history:", 1)
}

// responsePinKey 为父响应归属构造隔离键，供 PlanRoute/CommitRoute 使用。
// 参数 alias 为请求公开别名，caller 为鉴权范围，responseID 为响应标识，endpoint 为可选入口列表。
// 返回 response 命名空间下的完整 SHA256 摘要，不保留输入明文，不访问缓存或授予权限。
// JSON 数组保留字段边界和顺序，避免分隔符拼接歧义；字符串数组编码不会因业务输入失败。
// 不传 endpoint 与传入一个空 endpoint 的键不同，读写双方必须使用相同参数。
func responsePinKey(alias, caller, responseID string, endpoint ...string) string {
	raw, _ := json.Marshal(append([]string{alias, caller, responseID}, endpoint...))
	sum := sha256.Sum256(raw)
	return "session_route:v1:response:" + hex.EncodeToString(sum[:])
}

// sessionPinKey 为动态偏好构造隔离键，供 PlanRoute 读取及其 Claim/Save 回调比较更新。
// 参数 alias 为原请求别名，caller 为鉴权范围，sessionID 为会话 ID，endpoint 为可选入口列表。
// 返回 preference 命名空间下的完整 SHA256 摘要，不含输入明文，不读写缓存或校验空参数。
// JSON 数组保留字段边界；入口数量和顺序也参与摘要，读写双方必须一致。
// 这里只隔离缓存，不保证会话必须使用某部署；动态偏好每轮仍需通过数据面资格检查。
func sessionPinKey(alias, caller, sessionID string, endpoint ...string) string {
	raw, _ := json.Marshal(append([]string{alias, caller, sessionID}, endpoint...))
	sum := sha256.Sum256(raw)
	return "session_route:v1:preference:" + hex.EncodeToString(sum[:])
}

// sessionOwnerKey 为完整不透明历史构造独立于动态偏好的固定会话归属键。
// 参数为公开别名、鉴权范围、会话及入口；返回独立命名空间的哈希键，无缓存副作用。
// PlanRoute/CommitRoute 共用；普通请求更新偏好不能替旧密文重新签发连接归属。
func sessionOwnerKey(alias, caller, sessionID, endpoint string) string {
	return strings.Replace(sessionPinKey(alias, caller, sessionID, endpoint), "session_route:v1:preference:", "session_route:v1:owner:", 1)
}

// callerScope 从鉴权结果提取缓存范围，供 PlanRoute 构建 Caller。
// 参数 p 可为 nil；返回 key:<hash>（密钥优先）、user:<id>（无密钥时）或空字符串（无身份）。
// 密钥优先让同一用户的不同密钥拥有独立状态，前缀区分密钥和用户两类标识。
// 不使用客户端正文、团队 ID 或会话 ID 补足身份，不执行鉴权；无范围时不安装偏好回调。
func callerScope(p *auth.Principal) string {
	if p == nil {
		return ""
	}
	if p.Hash != "" {
		return "key:" + p.Hash
	}
	if p.UserID != "" {
		return "user:" + p.UserID
	}
	return ""
}

// asString 安全读取动态字符串，供 stableContent/contentSessionID 检查角色、类型和媒体字段。
// 参数 v 可为任意值；字符串原样返回，nil 或其他类型返回空字符串，不转换类型或修改输入。
// 不报错，空返回值无法区分缺失、类型错误和真实空串；严格会话信号改用 sessionSignal。
func asString(v any) string {
	s, _ := v.(string)
	return s
}

// affinityPin 是本机缓存条目，统一保存序列化载荷及绝对到期时间；由 affinityMu 保护。
type affinityPin struct {
	// 缓存序列化值；会话存 RouteTarget JSON、响应存归属/历史 JSON，任务存运行部署标识。
	deployment string
	// 本机绝对过期时刻；读取到期即清除，成功比较写入时续期。
	until time.Time
}

// affinityGet 宽松读取缓存，主要供 OfficialDeployment/OfficialContext 恢复任务信息。
// 参数 key 为完整键；返回载荷，nil 接收者、空键或无有效记录返回空字符串。
// 优先 GetString 读取共享存储，未取得值（未命中或故障）再读本机副本；不区分两种原因，
// 因而不能用于严格的父响应续接，后者使用 cacheRead。共享读取不持锁、不在此设置超时。
// 本机读取持 affinityMu，过期时删除，读取不续期。
func (s *State) affinityGet(key string) string {
	if s == nil || key == "" {
		return ""
	}
	if s.redis != nil {
		if v, ok := s.redis.GetString(context.Background(), key); ok {
			return v
		}
	}
	s.affinityMu.Lock()
	defer s.affinityMu.Unlock()
	pin, ok := s.affinity[key]
	if !ok || time.Now().After(pin.until) {
		delete(s.affinity, key)
		return ""
	}
	return pin.deployment
}

// affinitySet 以一小时 TTL 无条件写缓存，供无 Redis 时的 cacheWrite 及内部夹具使用。
// 参数 key 为完整键，deployment 实际可为归属、历史等任意非空载荷，不限于部署 ID。
// 无返回值；委托 affinitySetFor，nil 接收者、空键或空载荷不写，也不删除旧值。
// 不做比较更新；动态偏好使用 cacheCompare，避免覆盖并发赢家。
func (s *State) affinitySet(key, deployment string) {
	s.affinitySetFor(key, deployment, affinityTTL)
}

// officialPinTTL 为官方任务部署和计费上下文保留七天，覆盖创建后的多次轮询。
const officialPinTTL = 7 * 24 * time.Hour

// PinOfficial 保存异步任务创建时的运行部署，供 official 执行器后续找回原部署。
// 参数 taskID 在生产中是执行器按调用方、协议及任务 ID 构造的作用域，deploymentID 是运行部署 ID。
// 本方法只添加 official_task 命名空间，不自行隔离租户；调用方必须提供有效任务作用域。
// 无返回值；七天 TTL 写共享存储（如有）及本机，同键重复写会覆盖及续期。
// nil 接收者或空部署不写；不单独拒绝空 taskID。共享写入不报告错误，返回不代表持久化成功。
// 任务授权和部署可用性由执行器检查；这里不重新调度任务。
func (s *State) PinOfficial(taskID, deploymentID string) {
	s.affinitySetFor("official_task:v1:"+taskID, deploymentID, officialPinTTL)
}

// OfficialDeployment 按任务作用域取回运行部署，供 official 执行器查询、轮询及取消使用。
// 参数 taskID 必须与 PinOfficial 完全相同；返回部署 ID，无记录、过期或 nil 接收者返回空串。
// 通过 affinityGet 共享优先、本机降级，不续期、不执行鉴权、不重新选路。
// 非空作用域缺失时记日志，可能由未知任务、过期或存储故障导致。
func (s *State) OfficialDeployment(taskID string) string {
	dep := s.affinityGet("official_task:v1:" + taskID)
	if dep == "" && taskID != "" {
		// 保留归属缺失的诊断；GetString 无错误返回，不能在此确定具体缺失原因。
		util.Debug("official task has no pinned deployment task=%s", taskID)
	}
	return dep
}

// affinitySetFor 无条件保存载荷和有效期，供 affinitySet/PinOfficial/PinOfficialContext 使用。
// 参数 key 为完整键，deployment 为非空载荷，ttl 为明确指定的有效期；无返回值。
// nil 接收者、空键或空载荷不写且不删除旧值。ttl 原样传给 Redis，本机到期时刻为 now+ttl；
// 零或负值不会改用默认有效期，本机记录立即到期。
// 有 Redis 时先尝试共享写入，再无条件保存本机副本；SetString 不报告错误。
// 网络访问不持本机锁；本机写入持 affinityMu，清理过期和超容量记录，同键覆盖重新计时。
// 不可替代偏好原子比较，也不可替代有 Redis 时必须报告发布错误的 cacheWrite。
func (s *State) affinitySetFor(key, deployment string, ttl time.Duration) {
	if s == nil || key == "" || deployment == "" {
		return
	}
	if s.redis != nil {
		s.redis.SetString(context.Background(), key, deployment, ttl)
	}
	s.affinityMu.Lock()
	defer s.affinityMu.Unlock()
	s.pruneLocal(time.Now())
	s.affinity[key] = affinityPin{deployment: deployment, until: time.Now().Add(ttl)}
}

// PinOfficialContext 保存创建时计费事实，供 official 执行器轮询后结算使用。
// 参数 scope 是已隔离的任务作用域，应与任务部署作用域一致；facts 是创建时间和计价依据等快照。
// 不计算费用、不按轮询时的新配置重建事实；无返回值，编码成功后以七天 TTL 写共享存储及本机。
// 编码失败不写，空 scope 不单独拒绝，nil 接收者由 affinitySetFor 忽略。
// 同键可覆盖，共享写入不报告错误；“创建后保持不变”是调用方约定，不是写一次约束。
func (s *State) PinOfficialContext(scope string, facts providercontract.TaskContext) {
	raw, err := json.Marshal(facts)
	if err == nil {
		s.affinitySetFor("official_context:v1:"+scope, string(raw), officialPinTTL)
	}
}

// OfficialContext 恢复创建时计费快照，供 official 执行器轮询结算及用量记录使用。
// 参数 scope 必须与 PinOfficialContext 一致；返回 TaskContext，不更新费用或缓存有效期。
// 通过 affinityGet 共享优先、本机降级；缺失或 nil 接收者得到零值。JSON 错误被忽略，
// 语法损坏通常得到零值，字段类型错误可能留下部分解析字段；调用方须结合 Known 等字段判断可用性。
func (s *State) OfficialContext(scope string) providercontract.TaskContext {
	var facts providercontract.TaskContext
	_ = json.Unmarshal([]byte(s.affinityGet("official_context:v1:"+scope)), &facts)
	return facts
}

// Redis 是会话归属所需的最小共享存储接口，不暴露预算或计费队列。
type Redis interface {
	// ReadAffinity 供 cacheRead 严格读取，参数为带超时的上下文及完整键。
	// 返回载荷或真实存储错误，不存在/过期返回空串和 nil，不写入、不续期。
	// 必须区分故障与未命中，避免父响应续接在故障时重新选路。
	ReadAffinity(context.Context, string) (string, error)
	// CompareAffinity 供 cacheCompare 原子比较写入，参数依次为上下文、键、完整旧值、新值和 TTL。
	// 当前值等于旧值才写入并续期，缺失按空串比较；比较和写入必须原子完成。
	// 返回实际缓存值和错误，不匹配返回当前赢家和 nil，不写或续期。
	CompareAffinity(context.Context, string, string, string, time.Duration) (string, error)
	// WriteAffinity 供 cacheWrite 发布历史及归属，参数为上下文、完整键、载荷和 TTL。
	// 无条件写入并设置有效期，返回真实错误，以便历史失败时停止发布归属。
	// 不承诺多个键的事务原子性，不强制键只写一次。
	WriteAffinity(context.Context, string, string, time.Duration) error
	// GetString 供 affinityGet 读取官方任务，参数为上下文及完整键。
	// 返回字符串和是否取得值，未命中或故障均可能为 false，不暴露错误原因。
	// 不能用于要求严格归属的父响应续接。
	GetString(context.Context, string) (string, bool)
	// SetString 供 affinitySetFor 保存任务，参数为上下文、完整键、载荷和 TTL。
	// 无条件写入，无返回值，无法据此确认共享写入成功；State 另行保存本机副本。
	// 不用于偏好原子比较或需要错误反馈的响应发布。
	SetString(context.Context, string, string, time.Duration)
}

// State 持有持久会话存储、可选共享缓存和本机TTL缓存；零值仅用于纯内存测试。
// 实例可服务多个请求，本机 map 由 affinityMu 保护；使用后不应复制包含锁的 State。
// 网络访问及上游执行不持本机锁，不与网关配置锁交叉，不启动后台清理协程。
// 本机偏好受容量限制且重启会丢失；生产会话历史通过PostgreSQL跨实例共享并长期保存。
type State struct {
	// durable 保存续聊必需状态，生产注入 PostgreSQL；与 Redis 路由偏好独立。
	durable ConversationStore
	// 可选共享存储；nil 使用进程内缓存，普通故障可降级，响应归属故障拒绝续接。
	redis Redis
	// 本机 TTL 缓存，存放偏好、响应上下文及任务归属；写入受容量限制，非持久化数据库。
	affinity map[string]affinityPin
	// 仅保护本机缓存的互斥锁，不跨上游调用持锁，也不与网关配置锁交叉。
	affinityMu sync.Mutex
}

// New 为网关组装创建独立状态，参数 redis 为共享缓存实现，nil 表示仅使用本机。
// 返回非 nil 的 *State，本机 map 首次写入时初始化，不同 New 调用的本机缓存和锁各自独立。
// 不连接或探测 Redis、不启动任务，不保证注入存储可用；故障由实际读写处理。
func New(redis Redis) *State { return &State{redis: redis} }
