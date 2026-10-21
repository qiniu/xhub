package affinity

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sunqirui1987/xhub/internal/auth"
	dpcontract "github.com/sunqirui1987/xhub/internal/dataplane/contract"
	"github.com/sunqirui1987/xhub/internal/gateway/family"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	providercapability "github.com/sunqirui1987/xhub/internal/provider/capability"
	providerregistry "github.com/sunqirui1987/xhub/internal/provider/registry"
	"github.com/sunqirui1987/xhub/internal/util"
)

// PlanRoute 为当前请求构建会话路由计划，供统一适配入口和原生透传入口在选择部署前调用。
// 本函数负责识别会话、恢复父响应归属和读取动态偏好；最终部署是否仍满足当前配置、
// 权限、能力及预算等条件，由后续数据面执行链验证，本函数本身不发送上游请求。
//
// 参数：
//   - r：当前 HTTP 请求，用于识别协议入口和读取标准 X-Session-Id 头；可为 nil，
//     r 或 r.URL 为空时 Endpoint 保持空字符串，但仍会解析可用的头和正文信号。
//   - alias：客户端请求的公开模型名或路由组名；即使后续回退到其他模型，缓存隔离范围
//     仍使用这个原始别名，避免不同公开模型之间互相继承偏好或响应历史。
//   - body：已解析的请求正文，可为 nil；这里只读取会话信号，不修改用户的正文。
//   - p：鉴权后的调用方，可为 nil；优先以密钥哈希、其次以用户 ID 隔离缓存，
//     客户端传入的会话 ID 不能代替鉴权身份。
//
// 返回值为当前请求私有的 RoutePlan，调用方必须先检查 Err，再使用其余字段：
//   - SessionID / SessionSource：会话标识及来源，供响应头、日志归组及偏好缓存使用。
//     有 previous_response_id 时继承父响应会话（明确 ID 必须与其一致）；否则依次使用
//     网关明确 ID、Claude Code 原生会话、prompt_cache_key 指纹、稳定首轮内容指纹，最后生成随机 ID。
//   - Target / Pinned / Required：目标快照、稳定部署 ID 和是否必须使用原部署。
//     普通请求读取的是可重新选择的动态偏好；父响应续接读取的是固定归属，不能把
//     响应游标交给其他部署，也不能让当前动态偏好覆盖父响应的目标。
//   - History：仅 responses 入口续接时恢复的临时历史，供执行器合并本轮输入。
//   - Claim / Save：选路前申请首次偏好、成功后比较更新偏好的回调；无调用方时为 nil。
//
// 失败策略：非法或冲突的会话信号、不可用的父响应归属、缺失或损坏的续接历史都写入
// Err 并立即返回，执行器应在调用上游前拒绝；普通动态偏好的缓存故障只记录日志并
// 允许本机降级。错误返回的计划可能已有部分字段，不能把这些字段当作可执行的计划。
//
// 副作用与边界：读取本机缓存可能清理过期记录，Claim / Save 被调用时会写入缓存并
// 设置一小时 TTL；本函数不发布新的响应归属（由成功后的 CommitRoute 完成）。
// s 必须非 nil，可使用 State 零值；随机 ID 的熵源失败会由 newSessionID 触发 panic。
// 两个回调共享本轮读取的缓存值，按单个请求的执行顺序调用，不应并发调用同一计划
// 的回调；不同请求之间通过缓存的原子比较协调，不跨上游执行持锁。
func (s *State) PlanRoute(r *http.Request, alias string, body map[string]any, p *auth.Principal) dpcontract.RoutePlan {
	// 1. 确定协议入口，作为缓存隔离范围的一部分。先用统一推理路径推断能力入口，
	// 再以注册表匹配到的传输入口覆盖，兼容原生透传路径；Endpoint 是能力标识，
	// 不是供应商地址，同一会话在不同协议入口不会共用偏好或父响应记录。
	endpoint := ""
	if r != nil && r.URL != nil {
		endpoint = providercapability.EndpointForOp(family.InferenceOp(r.URL.Path))
		if hit, ok := providerregistry.Match(r.Method, r.URL.Path); ok {
			endpoint = hit.Transport.EndpointID
		}
	}
	plan := dpcontract.RoutePlan{Alias: alias, Caller: callerScope(p), Endpoint: endpoint}
	// 2. 优先校验明确的会话信号：标准头的多个值与正文 session_id 必须一致。
	// 字段存在但为空、类型错误、超长或包含非法字符都属于错误，不能悄悄回退
	// 为推断会话；先校验也保证后续继承父响应时不会忽略客户端的身份冲突。
	explicit, err := explicitSession(r, body)
	if err != nil {
		plan.Err = err
		return plan
	}
	if explicit != "" {
		plan.SessionID, plan.SessionSource = explicit, "explicit"
	}
	if plan.SessionID == "" && (endpoint == "messages" || endpoint == "bypass:anthropic-messages") {
		// Claude 工具续轮可能改写首条用户消息的 cache_control 等字段；客户端会话信号不受这些变化影响。
		// 会话 ID 仍与可信 Caller、公开模型和入口一起隔离，不能代替密文原连接归属证明。
		id, err := claudeSessionID(r, body)
		if err != nil {
			plan.Err = err
			return plan
		}
		if id != "" {
			plan.SessionID, plan.SessionSource = id, "explicit"
		}
	}
	// 3. previous_response_id 表示续接已存在的响应，优先级高于普通会话推断。
	// 这里检查“字段是否存在”，所以空值或非字符串会被拒绝，而非当成新请求。
	if raw, exists := body["previous_response_id"]; exists {
		prev, err := sessionSignal(raw)
		if err != nil {
			plan.Err = fmt.Errorf("invalid previous_response_id: %w", err)
			return plan
		}
		// 归属键同时隔离公开别名、鉴权调用方、响应 ID 和协议入口，防止跨范围续接。
		// false 禁止在共享存储故障时改读本机副本：无法确认归属就拒绝续接，
		// 不能将故障当成缓存未命中后重新选路；未配置 Redis 时仍使用本机存储。
		ownerRaw, _, err := s.cacheRead(responsePinKey(alias, plan.Caller, prev, endpoint), false)
		var owner responseOwner
		// 缓存不存在、过期、读取失败、JSON 损坏或归属字段不完整均无法安全续接。
		// 对外统一要求提交完整历史，不凭响应 ID 猜测部署或暴露其他范围的归属。
		if err != nil || json.Unmarshal([]byte(ownerRaw), &owner) != nil || owner.Target.DeploymentID == "" || owner.Session == "" {
			plan.Err = fmt.Errorf("response ownership is unavailable; submit full history")
			return plan
		}
		if explicit != "" && explicit != owner.Session {
			plan.Err = fmt.Errorf("session_id conflicts with previous_response_id")
			return plan
		}
		// 即使明确 ID 与父会话相同，来源也记为 parent，表示本轮实际继承父响应。
		// Required 将“偏好这个部署”升级为“只能使用这个部署”；完整 Target 还
		// 保留模型池和回退路径，供执行器按当前配置重新验证该固定目标的资格。
		plan.SessionID, plan.SessionSource, plan.Pinned, plan.Required = owner.Session, "parent", owner.Target.DeploymentID, true
		plan.Target = owner.Target
		if endpoint == "responses" {
			// 统一 responses 续接还需要网关保存的临时历史，仅知道部署不够。
			// 历史与归属使用相同的隔离范围；同样禁止共享故障时本机降级，且拒绝
			// 空历史，避免在上下文丢失后把增量输入当成完整对话发送。其他入口
			// 在此只恢复固定归属，不读取这份 responses 历史。
			historyRaw, _, err := s.cacheRead(responseContextKey(alias, plan.Caller, prev, endpoint), false)
			if err != nil || json.Unmarshal([]byte(historyRaw), &plan.History) != nil || len(plan.History) == 0 {
				plan.Err = fmt.Errorf("response context is unavailable or expired; submit full history")
				return plan
			}
		}
	}
	// 4. 没有明确或继承的会话时才推断；已有会话不会再校验低优先级的
	// prompt_cache_key。只有推断所需字段缺失时才能继续尝试下一来源，
	// 存在但非法的 prompt_cache_key 必须报错，不能隐式改用内容指纹。
	if plan.SessionID == "" {
		if raw, exists := body["prompt_cache_key"]; exists {
			key, err := sessionSignal(raw)
			if err != nil {
				plan.Err = fmt.Errorf("invalid prompt_cache_key: %w", err)
				return plan
			}
			// 对原始缓存键做带命名空间的 SHA256 指纹，避免会话 ID 泄露其明文。
			plan.SessionID, plan.SessionSource = fingerprint("cache:v1:", key), "cache_key"
		} else if id := contentSessionID(body); id != "" {
			// 内容指纹使用开头指令和首条用户内容，追加轮次不改变会话；
			// 不能完整识别的内容不强行推断，避免把不同会话错误合并。
			plan.SessionID, plan.SessionSource = id, "prompt"
		} else {
			// 缺乏可靠信号时每个请求生成独立 ID，不把无信号请求聚成同一会话。
			plan.SessionID, plan.SessionSource = newSessionID(), "generated"
		}
	}
	// 5. 会话 ID 可用于本轮标识，但没有鉴权范围就不建立动态偏好，也不安装
	// 写入回调，避免不同匿名请求仅凭客户端可指定的 ID 共用路由状态。
	if plan.Caller == "" {
		return plan
	}
	// 网关随机引用带有独立、不可重绑定的原响应归属；在普通会话偏好之前核实。
	if err := s.prepareMessagesHistory(&plan, body); err != nil {
		plan.Err = err
		return plan
	}
	key := sessionPinKey(alias, plan.Caller, plan.SessionID, endpoint)
	// 加密推理依赖生成它的供应商连接，不能按普通会话偏好切换账户或降级读本机缓存。
	opaque := requiresHistoryOwnership(body, endpoint == "messages")
	// expected 是本轮读取的完整序列化值（含 Revision），而非仅部署 ID；
	// local 指定本轮后续比较写入使用本机还是共享存储。普通偏好允许降级，
	// 所以读取错误只记录日志，不设置 plan.Err，也不影响已恢复的父响应归属。
	expected, local, err := s.cacheRead(key, !opaque)
	if err != nil {
		if opaque {
			plan.Err = fmt.Errorf("encrypted history ownership is unavailable; start a new conversation")
			return plan
		}
		util.Debug("session affinity cache degraded: %v", err)
	}
	if !plan.Required {
		// 普通偏好是可丢弃的提示，不按父响应归属的严格策略拒绝请求；这里
		// 忽略反序列化错误，不可用的目标仍交由后续选路验证，不能充当权限凭据。
		_ = json.Unmarshal([]byte(expected), &plan.Target)
	}
	if !plan.Required {
		// Pinned 保存稳定部署 ID，执行器再映射到当前目录中的运行配置。
		// Required 计划跳过这两步，确保动态偏好不能覆盖父响应的 Target/Pinned。
		plan.Pinned = plan.Target.DeploymentID
	}
	if opaque && !plan.Required {
		// 完整密文的证明必须来自首次成功响应，而非可以被普通请求改写的动态路由偏好。
		// 否则连接更新后发一次普通请求，就可能把旧密文错误地绑定到新账户。
		ownerRaw, _, ownerErr := s.cacheRead(sessionOwnerKey(alias, plan.Caller, plan.SessionID, endpoint), false)
		var owner dpcontract.RouteTarget
		if ownerErr != nil || json.Unmarshal([]byte(ownerRaw), &owner) != nil || owner.DeploymentID == "" {
			plan.Err = fmt.Errorf("encrypted history requires an owned session or previous_response_id")
			return plan
		}
		plan.Target, plan.Pinned, plan.Required = owner, owner.DeploymentID, true
	}
	// 6. 安装请求级回调：expected/local 被闭包捕获，Claim 更新它们后 Save
	// 使用同一份基准。后续新请求会重新读取存储，Redis 恢复后以共享值为准，
	// 不把故障期间的本机旧偏好自动回灌；当前计划仍沿用它选定的存储分支。
	// Claim 由 SelectAffinity 在普通请求发送前调用；Required 续接跳过 Claim。
	plan.Claim = func(candidate dpcontract.RouteTarget) dpcontract.RouteTarget {
		if expected == "" {
			// 首次无偏好时给候选生成随机版本，只在缓存仍为空时原子写入。
			// 同一会话并发首次到达时，仅一个候选能写入，其余请求得到已存在
			// 的赢家；这是短暂缓存协调，不是围绕整个上游调用的互斥锁。
			candidate.Revision = newSessionID()
			// RouteTarget 仅含可 JSON 编码的字段，此处序列化无需业务错误分支。
			raw, _ := json.Marshal(candidate)
			current, err := s.cacheCompare(key, "", string(raw), local)
			if err != nil {
				util.Debug("session affinity claim degraded: %v", err)
				local = true
				// 共享申请失败后，仅在本机执行同样的原子申请；Save 随后也
				// 使用本机分支。此时收敛范围限于当前进程，不保证跨实例一致。
				current, _ = s.cacheCompare(key, "", string(raw), true)
			}
			// 保存实际赢家的完整值，使后续成功提交只更新这一版偏好。
			expected = current
		}
		// 已有偏好时不重新申请，直接返回本轮快照；返回值并不保证仍有
		// 资格或解析完整，SelectAffinity 必须验证后再决定是否使用该目标。
		var winner dpcontract.RouteTarget
		_ = json.Unmarshal([]byte(expected), &winner)
		return winner
	}
	// Save 由成功后的 CommitRoute 调用，用实际成功目标替换本轮基准；
	// 即使请求经过回退，也只提交最终成功目标，失败尝试不在这里提交。
	plan.Save = func(candidate dpcontract.RouteTarget) {
		// 每次写入都换 Revision，防止部署 A -> B -> A 后旧请求把第二个 A
		// 误认成自己读过的版本（ABA 问题）；Revision 不用于时间排序。
		candidate.Revision = newSessionID()
		raw, _ := json.Marshal(candidate)
		// 仅当完整旧值仍等于 expected 时写入并续期，否则保留当前赢家。
		// 比较不匹配是正常并发结果；存储故障仅记日志，不撤销已经成功的
		// 上游结果，也不在这里重试覆盖或发布本机替代值。Save 不刷新
		// expected，通常每轮成功只提交一次，不可当作可反复覆盖的 setter。
		if _, err := s.cacheCompare(key, expected, string(raw), local); err != nil {
			util.Debug("session affinity commit degraded: %v", err)
		}
	}
	return plan
}

// responseOwner 是响应创建时的固定部署和会话；续接不使用动态偏好代替它。
type responseOwner struct {
	// 本轮读取或实际执行的部署、模型池及回退路径；执行前重新验证当前配置中的资格。
	Target dpcontract.RouteTarget `json:"target"`
	// 响应创建时的会话 ID，续接继承且与明确 ID 校验一致，不随动态偏好变化。
	Session string `json:"session"`
}

// CommitRoute 在上游成功后提交实际目标并按需发布响应归属，供两类数据面执行器调用。
// 参数 plan 必须是本轮有效计划，执行器已将实际模型池、型号及回退路径写入 plan.Target；
// deploymentID 为成功部署的稳定 ID，responseID 为成功响应标识，空串表示不发布响应归属。
// responses 执行器还须将本轮输入和成功输出合并进 plan.History；本方法不构造历史。
// 无返回值；空 deploymentID 或 Caller 直接返回，不写偏好、历史或归属。
// 先复制 Target、填成功部署，缺失 Pool/Model 用原 alias 补足，再通过 Save 比较更新偏好。
// 偏好比较失败不撤销成功结果，响应归属仍记录实际连接指纹，与动态偏好是否更新成功独立。
// Responses 首次成功目标另存为固定会话归属，后续普通选路只更新偏好，不能替旧密文更换账户。
// 有 responseID 和 SessionID 时发布归属；responses 且 History 非空时先编码并写历史，
// 历史失败即停止，避免发布已知缺少上下文的归属；History 为空时仍发布归属，但续接会因缺历史拒绝。
// 历史和归属是非事务的一小时记录，不是原厂长期 store；归属写入错误在此被忽略。
// 调用方应每轮成功提交一次，失败尝试或仅命中响应缓存时不调用；s 必须非 nil。
func (s *State) CommitRoute(plan dpcontract.RoutePlan, deploymentID, responseID string) {
	_ = s.commitRoute(plan, deploymentID, responseID)
}

// commitRoute 保存实际目标、完整上下文与固定归属，并返回影响父响应续接的保存错误。
// 参数与公开 CommitRoute 相同；Messages 桥接准备回调必须检查错误后才能发布终态。
// 动态偏好允许失败；历史先于响应归属写入，保存失败返回错误；持久记录不依赖缓存TTL。
func (s *State) commitRoute(plan dpcontract.RoutePlan, deploymentID, responseID string) error {
	if deploymentID == "" || plan.Caller == "" {
		return nil
	}
	// 复制计划目标，只替换实际成功部署；成功后的回退路径应已由执行器补全。
	target := plan.Target
	target.DeploymentID = deploymentID
	if target.Pool == "" {
		target.Pool = plan.Alias
	}
	if target.Model == "" {
		target.Model = plan.Alias
	}
	// 动态偏好更新和响应归属发布相互独立，比较失败不把成功响应改成错误。
	if plan.Save != nil {
		plan.Save(target)
	}
	if responseID != "" && plan.SessionID != "" {
		// 统一及原生 Responses 的入口 ID 不同；固定归属与父响应一样按实际入口隔离发布。
		// 首次成功目标只允许相同连接续期，不能由后续普通选路覆盖。
		key := sessionOwnerKey(plan.Alias, plan.Caller, plan.SessionID, plan.Endpoint)
		targetRaw, _ := json.Marshal(target)
		// 先迁移仍有效的旧缓存归属，防止升级后首次成功请求覆盖原连接证明。
		if s.durable != nil {
			if _, _, err := s.cacheRead(key, false); err != nil {
				return fmt.Errorf("conversation ownership could not be loaded")
			}
		}
		current, err := s.cacheCompare(key, "", string(targetRaw), false)
		if err != nil && s.durable != nil {
			return fmt.Errorf("conversation ownership could not be saved")
		}
		if err == nil && current != string(targetRaw) {
			var owner dpcontract.RouteTarget
			if json.Unmarshal([]byte(current), &owner) == nil {
				if owner.DeploymentID == target.DeploymentID && owner.ConnectionID == target.ConnectionID {
					if _, err := s.cacheCompare(key, current, current, false); err != nil && s.durable != nil {
						return fmt.Errorf("conversation ownership could not be saved")
					}
				} else {
					// 同一会话曾在不同连接成功，完整历史无法再仅凭会话 ID 确认出处。
					// 保留不可用归属，禁止后续普通请求或迟到成功重新绑定；明确父响应仍有独立快照。
					owner.ConnectionID = ""
					invalid, _ := json.Marshal(owner)
					if _, err := s.cacheCompare(key, current, string(invalid), false); err != nil && s.durable != nil {
						return fmt.Errorf("conversation ownership could not be saved")
					}
				}
			}
		}
		// 先发布历史，再发布可被后续请求引用的归属；不是跨键事务，但不能反过来。
		if plan.Endpoint == "responses" && len(plan.History) > 0 {
			raw, err := json.Marshal(plan.History)
			if err != nil {
				return fmt.Errorf("response history could not be encoded")
			}
			if err := s.cacheWrite(responseContextKey(plan.Alias, plan.Caller, responseID, plan.Endpoint), string(raw)); err != nil {
				return fmt.Errorf("response history could not be saved")
			}
		}
		raw, _ := json.Marshal(responseOwner{Target: target, Session: plan.SessionID})
		if err := s.cacheWrite(responsePinKey(plan.Alias, plan.Caller, responseID, plan.Endpoint), string(raw)); err != nil {
			return fmt.Errorf("response ownership could not be saved")
		}
	}
	return nil
}

// requiresNativeOwnership 判断对话输入是否含供应商绑定的不透明推理历史。
// 参数 body 为未改写的用户正文，返回是否必须固定原部署；PlanRoute 调用，不解密、不写缓存。
// 普通推理摘要和 DeepSeek 公开 reasoning_content 不触发固定；加密项即使格式错误也不能触发跨账户重试。
func requiresNativeOwnership(body map[string]any) bool {
	return requiresHistoryOwnership(body, false)
}

// requiresHistoryOwnership 按入口判断是否需要网关保存的原连接证明，参数为正文及是否为统一 Messages。
// 返回强制绑定标志；PlanRoute 调用。完整原生 Messages 历史由客户端提供、上游验签，
// 不读取其他用户数据，不因公开别名修改或本地归属缺失拒绝；封装的 Responses 密文仍绑定原连接。
func requiresHistoryOwnership(body map[string]any, nativeMessages bool) bool {
	items, _ := body["input"].([]any)
	for _, value := range items {
		item, _ := value.(map[string]any)
		if item["encrypted_content"] != nil {
			return true
		}
	}
	// Chat 扩展只有严格验证后的公开摘要可以迁移；密文、签名或非法结构仍要求原连接证明。
	messages, _ := body["messages"].([]any)
	for _, value := range messages {
		message, _ := value.(map[string]any)
		role, _ := message["role"].(string)
		if llmdialogue.ChatReasoningRequiresNativeOwnership(message["reasoning_items"], role) {
			return true
		}
		blocks, _ := message["content"].([]any)
		for _, value := range blocks {
			block, _ := value.(map[string]any)
			// 原厂签名不授予读取网关历史的权限；统一 Messages 允许携带完整历史重新选路，签名原样交给上游验证。
			// 跨协议封装不是原厂签名，不能借此绕过原连接校验；原生 bypass 保持原策略。
			if llmdialogue.IsResponsesReasoningSignature(block["signature"]) || llmdialogue.IsResponsesReasoningSignature(block["data"]) || (!nativeMessages && (block["type"] == "redacted_thinking" || (block["type"] == "thinking" && block["signature"] != nil && block["signature"] != ""))) {
				return true
			}
		}
	}
	return false
}
