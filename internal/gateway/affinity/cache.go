package affinity

import (
	"context"
	"time"
)

// localCapacity 是所有本机偏好、响应和任务记录共用的容量上限，写入前淘汰最早到期项。
const localCapacity = 8192

// cacheRead 读取会话状态；生产续聊键优先读取PostgreSQL，普通偏好及测试状态使用缓存。
// 持久读取失败不降级；数据库未命中时仅导入仍有效的旧缓存，供升级期间恢复已有会话。
// 参数 key 是完整键，degrade 表示共享读取失败时是否允许改读本机；s 必须非 nil。
// 返回载荷、是否选用本机存储、共享读取错误三项；无 Redis 返回本机值、true、nil。
// 有 Redis 时成功读取（包括未命中）返回共享值、false、nil，不再读取本机旧值。
// 共享失败且 degrade=false 时直接返回错误；degrade=true 返回本机值、true 以及原错误。
// 保留错误供调用方记录降级；布尔值供 Claim/Save 选择存储分支，不表示是否命中。
// 共享访问使用独立的一秒超时、不持本机锁；本机读取可能删除过期项，不续期。
func (s *State) cacheRead(key string, degrade bool) (string, bool, error) {
	if s.durable != nil && durableKey(key) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		value, err := s.durable.ReadConversation(ctx, key)
		if err != nil || value != "" {
			return value, false, err
		}
		// 升级时只迁移仍有效的旧缓存；缺失记录不能由客户端历史自行补签归属。
		if s.redis != nil {
			value, err = s.redis.ReadAffinity(ctx, key)
		} else {
			value = s.affinityGetLocal(key)
		}
		if err != nil || value == "" {
			return value, false, err
		}
		value, err = s.durable.CompareConversation(ctx, key, "", value)
		return value, false, err
	}
	if s.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		value, err := s.redis.ReadAffinity(ctx, key)
		// 共享未命中不是故障，不可用本机旧值“补命中”；严格归属也禁止故障降级。
		if err == nil || !degrade {
			return value, false, err
		}
		return s.affinityGetLocal(key), true, err
	}
	return s.affinityGetLocal(key), true, nil
}

// affinityGetLocal 仅读本机有效载荷，供 cacheRead 无共享存储或普通偏好降级时使用。
// 参数 key 为完整键；返回有效字符串，无记录或到期返回空串；s 必须非 nil，nil map 可读取。
// 方法自行持 affinityMu，调用方不得已持此锁；到期条件为 now>=until，过期项立即删除。
// 不访问 Redis、不续期、不将本机值同步回共享存储。
func (s *State) affinityGetLocal(key string) string {
	s.affinityMu.Lock()
	defer s.affinityMu.Unlock()
	pin := s.affinity[key]
	if !time.Now().Before(pin.until) {
		delete(s.affinity, key)
		return ""
	}
	return pin.deployment
}

// cacheCompare 原子比较完整旧载荷并更新，供 PlanRoute 的 Claim/Save 及归属发布调用。
// 生产归属键委托PostgreSQL且无TTL；以下local/TTL行为仅适用于路由偏好和测试缓存。
// 参数 key 为完整键，old 为本轮读取的完整值（含 Revision），value 为新序列化目标，
// local 为是否限定本机；返回比较后的实际缓存值及存储错误，s 必须非 nil。
// local=false 且有 Redis 时委托共享原子比较，设置一秒超时；故障直接返回，不自行降级。
// 其余情况持本机锁比较，未命中或过期视为空串；old 为空实现首次申请。
// 不匹配返回现有赢家和 nil，不写、不续期；匹配写入 value，设置一小时 TTL。
// 只协调缓存，不锁上游执行；是否接受赢家由数据面按当前配置验证。
func (s *State) cacheCompare(key, old, value string, local bool) (string, error) {
	if s.durable != nil && durableKey(key) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.durable.CompareConversation(ctx, key, old, value)
	}
	if !local && s.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return s.redis.CompareAffinity(ctx, key, old, value, affinityTTL)
	}
	s.affinityMu.Lock()
	defer s.affinityMu.Unlock()
	now := time.Now()
	pin := s.affinity[key]
	if !now.Before(pin.until) {
		pin = affinityPin{}
		delete(s.affinity, key)
	}
	// 必须比较完整载荷，不能只比较 DeploymentID；Revision 让相同部署的新版本也可区分。
	if pin.deployment != old {
		return pin.deployment, nil
	}
	s.pruneLocal(now)
	s.affinity[key] = affinityPin{deployment: value, until: now.Add(affinityTTL)}
	return value, nil
}

// pruneLocal 在本机写入前初始化 map、清除过期项，并在容量已满时淘汰一项。
// 参数 now 为清理时间；无返回值，供 cacheCompare/affinitySetFor 在插入前调用。
// 调用方必须已持 affinityMu，本函数不自行加锁、不访问共享存储、不启动清理任务。
// 到期条件为 now>=until；若清理后仍至少有 8192 项，删除到期时间最早的一项，为写入留位。
// 淘汰按到期时间而非访问时间，同到期时间取决于 map 遍历顺序；各类记录共用容量。
// 覆盖已有键也会做容量检查；只影响临时缓存，不影响用量日志或上游运行。
func (s *State) pruneLocal(now time.Time) {
	if s.affinity == nil {
		s.affinity = map[string]affinityPin{}
	}
	// 遍历中同时清理过期项和寻找最早到期项，不采用访问频率或 LRU。
	var oldestKey string
	var oldest time.Time
	for key, pin := range s.affinity {
		if !now.Before(pin.until) {
			delete(s.affinity, key)
			continue
		}
		if oldestKey == "" || pin.until.Before(oldest) {
			oldestKey, oldest = key, pin.until
		}
	}
	if len(s.affinity) >= localCapacity {
		delete(s.affinity, oldestKey)
	}
}

// cacheWrite 发布成功响应历史或归属，供 CommitRoute 按先历史、后归属的顺序调用。
// 生产续聊键写PostgreSQL且无TTL；以下Redis及本机行为仅适用于未注入持久库的测试。
// 参数 key 为完整键，value 为序列化载荷；返回共享写入错误或 nil，s 必须非 nil。
// 有 Redis 使用一秒超时无条件写入并设置一小时 TTL，错误原样返回，不写本机替代归属。
// 历史发布失败时调用方可停止发布归属，避免其他实例得到无法恢复上下文的父响应。
// 无 Redis 由 affinitySet 写本机后返回 nil；该包装不额外校验空键或载荷。
// 两个键不是事务，无条件写入不强制归属只写一次；正确发布顺序由 CommitRoute 保证。
func (s *State) cacheWrite(key, value string) error {
	if s.durable != nil && durableKey(key) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.durable.WriteConversation(ctx, key, value)
	}
	if s.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return s.redis.WriteAffinity(ctx, key, value, affinityTTL)
	}
	s.affinitySet(key, value)
	return nil
}
