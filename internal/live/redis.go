// Package live is Redis for the hot path. Cooldown, latency, RPM, TPM, and spend deltas live here. The request itself does not write PostgreSQL.
package live

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sunqirui1987/xhub/internal/util"
)

// Client is the hot-path Redis client. A request only updates it. Spend logs are written to PostgreSQL by the timed flush.
// Client is the gateway hot path. Router cooldown, latency, and token counters
// live here, as do RPM/TPM buckets and the spend delta. Spend logs are queued
// and flushed to PostgreSQL by the server; the request does not insert them.
type Client struct {
	rdb *redis.Client
}

// SpendRef 拼出热花费计数的 Redis 键，按密钥、团队、用户、组织或项目分开。
// 参数 kind（string）：分类名，用来选择限额主体、日志类型或官方端点；id（string）：花费引用使用的主键。空串表示调用方没有指定记录。
// 返回 string（string）：热花费计数的 Redis 前缀，按密钥、团队、用户或组织分开。
// 调用：dataplane/live.go、gateway/limits.go、gateway/spend.go
// 测试：无直接单测
func SpendRef(kind, id string) string {
	if kind == "" || id == "" {
		return id
	}
	return kind + "/" + id
}

var logTraceOnceRedis sync.Once

// Open parses the Redis URL and pings it. On failure it closes the client and returns the error.
// 参数 url（string）：根地址或完整 URL。空串表示改用供应商默认根，末尾斜杠会去掉。
// 返回 *Client（*Client）：已 ping 通的 Redis 客户端。解析 URL 或 ping 失败时为 nil，并且会关掉半开的客户端；error（error）：URL 不合法或 ping 失败。nil 表示可以用。
// 调用：gateway/server.go、iam/db.go、store/engine.go、store/store.go
// 测试：authz_test.go、builtin_providers_test.go、chains_test.go
func Open(url string) (*Client, error) {
	logTraceOnceRedis.Do(func() { util.Trace("enter live.Open") })

	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	c := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, err
	}
	return &Client{rdb: c}, nil
}

// Close closes Redis. A nil client is not an error.
// 参数：无。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：dataplane/official.go、dataplane/serve.go、dataplane/stream.go、gateway/ingress.go
// 测试：authz_test.go、builtin_providers_test.go、bypass_logic_test.go
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// RecordFailure increments the failure count. After allowed failures it writes the cooldown key. An allowed below 1 does nothing.
// 参数 id（string）：记录失败使用的主键。空串表示调用方没有指定记录；allowed（int）：记录失败使用的整数。零表示没有这项或尚未计数；cooldown（time.Duration）：一段时间。零值表示改用调用方约定的默认时长，例如会话一小时、官方任务七天。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：dataplane/live.go、gateway/wire.go
// 测试：无直接单测
func (c *Client) RecordFailure(id string, allowed int, cooldown time.Duration) error {
	if c == nil || id == "" || allowed < 1 {
		return nil
	}
	ctx := context.Background()
	n, err := c.rdb.Incr(ctx, "xhub:fails:"+id).Result()
	if err != nil {
		return err
	}
	_ = c.rdb.Expire(ctx, "xhub:fails:"+id, cooldown).Err()
	if int(n) < allowed {
		return nil
	}
	return c.rdb.Set(ctx, "xhub:cooldown:"+id, "1", cooldown).Err()
}

// Cooled reports which deployment ids are still cooling down. If Redis is unavailable it returns an empty map and the caller treats that as no cooldown.
// 参数 ids（[]string）：要保留或查询的一组 id。空切片表示没有可处理的记录。
// 返回 map[string]bool（map[string]bool）：Cooled。没有该键表示假，不要当成缺省 JSON。
// 调用：dataplane/live.go
// 测试：无直接单测
func (c *Client) Cooled(ids []string) map[string]bool {
	out := map[string]bool{}
	if c == nil || len(ids) == 0 {
		return out
	}
	ctx := context.Background()
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "xhub:cooldown:" + id
	}
	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return out
	}
	for i, v := range vals {
		if v != nil {
			out[ids[i]] = true
		}
	}
	return out
}

// AddLatency pushes one latency onto the left of the list, keeps the latest 20, and expires the key after one hour.
// 参数 id（string）：累加延迟使用的主键。空串表示调用方没有指定记录；ms（float64）：累加延迟使用的小数。0 表示没有费用或尚未计价。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：dataplane/live.go
// 测试：无直接单测
func (c *Client) AddLatency(id string, ms float64) error {
	if c == nil || id == "" {
		return nil
	}
	ctx := context.Background()
	key := "xhub:latency:" + id
	pipe := c.rdb.TxPipeline()
	pipe.LPush(ctx, key, fmt.Sprintf("%g", ms))
	pipe.LTrim(ctx, key, 0, 19)
	pipe.Expire(ctx, key, time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

// Latencies returns the average of recent latencies for each deployment. An id with no sample is omitted.
// 参数 ids（[]string）：要保留或查询的一组 id。空切片表示没有可处理的记录。
// 返回 map[string]float64（map[string]float64）：Latencies的数值表，例如花费或 token。没有该键表示还没有发生过。
// 调用：dataplane/live.go
// 测试：无直接单测
func (c *Client) Latencies(ids []string) map[string]float64 {
	out := map[string]float64{}
	if c == nil {
		return out
	}
	ctx := context.Background()
	for _, id := range ids {
		vals, err := c.rdb.LRange(ctx, "xhub:latency:"+id, 0, 19).Result()
		if err != nil || len(vals) == 0 {
			continue
		}
		sum := 0.0
		n := 0
		for _, raw := range vals {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				continue
			}
			sum += v
			n++
		}
		if n > 0 {
			out[id] = sum / float64(n)
		}
	}
	return out
}

// AddUsage adds tokens to the current minute bucket.
// 参数 id（string）：累加用量使用的主键。空串表示调用方没有指定记录；tokens（int）：累加用量使用的整数。零表示没有这项或尚未计数。
// 返回 error（error）：失败原因，nil 表示这一步成功。
// 调用：dataplane/live.go
// 测试：无直接单测
func (c *Client) AddUsage(id string, tokens int) error {
	if c == nil || id == "" || tokens == 0 {
		return nil
	}
	if tokens < 0 {
		return fmt.Errorf("negative token increment")
	}
	return incrementWithTTL.Run(context.Background(), c.rdb, []string{"xhub:routetpm:" + id}, tokens, 60000).Err()
}

// Usages returns each deployment's token usage for the current minute.
// 参数 ids（[]string）：要保留或查询的一组 id。空切片表示没有可处理的记录。
// 返回 map[string]float64（map[string]float64）：Usages的数值表，例如花费或 token。没有该键表示还没有发生过。
// 调用：dataplane/live.go
// 测试：无直接单测
func (c *Client) Usages(ids []string) map[string]float64 {
	out := map[string]float64{}
	if c == nil || len(ids) == 0 {
		return out
	}
	ctx := context.Background()
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "xhub:routetpm:" + id
	}
	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return out
	}
	for i, v := range vals {
		s, _ := v.(string)
		n, err := strconv.ParseFloat(s, 64)
		if err == nil {
			out[ids[i]] = n
		}
	}
	return out
}

// minuteBucket is the current Unix minute, used as the suffix of RPM and TPM keys.
// 参数：无。
// 返回 int64（int64）：当前 Unix 分钟，即 time.Now().Unix()/60。用作 RPM 和 TPM 键的后缀。这一步不会失败，也不会返回 0 来表示缺失。
// 调用：仅在 redis.go 内使用
// 测试：无直接单测
func minuteBucket() int64 { return time.Now().Unix() / 60 }

// HitRPM increments the current minute's request count and returns the new value.
// 参数 id（string）：虚拟密钥的令牌哈希，即 Principal.Hash。不是部署 id。计数键是 xhub:rpm: 加上这个哈希，再加当前分钟。
// 返回 int64（int64）：这一分钟累加 1 之后的请求数。客户端为 nil 时返回 0 且 error 为 nil；Redis INCR 失败时返回 0 和 error。
// 调用：gateway/limits.go
// 测试：无直接单测
func (c *Client) HitRPM(id string) (int64, error) {
	return c.bump("xhub:rpm:"+id, 1)
}

// HitTPM adds tokens to the current minute and returns the new value.
// 参数 id（string）：虚拟密钥的令牌哈希，即 Principal.Hash。不是部署 id。计数键是 xhub:tpm: 加上这个哈希，再加当前分钟；tokens（int）：这次估算要累加的 token 数。
// 返回 int64（int64）：这一分钟累加后的 token 数。客户端为 nil 时返回 0 且 error 为 nil；Redis INCR 失败时返回 0 和 error。
// 调用：gateway/limits.go
// 测试：无直接单测
func (c *Client) HitTPM(id string, tokens int) (int64, error) {
	return c.bump("xhub:tpm:"+id, int64(tokens))
}

// bump adds n to a minute counter and returns the new value. The key includes the minute number, so the next minute starts at zero after expiry.
// 参数 prefix（string）：Redis 键的前半段。HitRPM 传入 xhub:rpm: 加上密钥哈希，HitTPM 传入 xhub:tpm: 加上密钥哈希。不是部署 id，也不是要剥掉的路径前缀；n（int64）：要加到这个分钟计数上的数量。
// 返回 int64（int64）：INCRBY 之后的新计数。客户端为 nil 时返回 0 且 error 为 nil。Redis 失败时返回 0 和 error。键两分钟后过期。
// 调用：仅在 redis.go 内使用
// 测试：无直接单测
func (c *Client) bump(prefix string, n int64) (int64, error) {
	if c == nil {
		return 0, nil
	}
	if n < 0 {
		return 0, fmt.Errorf("negative counter increment")
	}
	key := fmt.Sprintf("%s:%d", prefix, minuteBucket())
	return incrementWithTTL.Run(context.Background(), c.rdb, []string{key}, n, 120000).Int64()
}

var incrementWithTTL = redis.NewScript("local n = redis.call('INCRBY', KEYS[1], ARGV[1]); redis.call('PEXPIRE', KEYS[1], ARGV[2]); return n")

// HotSpend 读取尚未确认落库的花费，供网关限额展示和检查使用，不消费队列。
// 参数 id 为 SpendRef 生成的主体标识；返回对应金额，缺少客户端、标识或可读计数时返回零。
// 严格预算准入不依靠此遥测读取降级放行；写入和释放只经过 EnqueueSpend 与 AckFlushed。
// 测试：redis_test.go 验证各层计数、非法入队、并发去重和重复确认。
func (c *Client) HotSpend(id string) float64 {
	if c == nil || id == "" {
		return 0
	}
	v, err := c.rdb.Get(context.Background(), "xhub:spend:"+id).Float64()
	if err != nil {
		return 0
	}
	return v
}

// HotSpendSince 读取组织当前周期内尚未确认的费用；id 为组织 ID，since 为包含在内的周期起点。
// 预算准入调用；新队列通过组织时间索引读取，升级时混有未建索引的旧事件则从队列兼容读取。
// 返回未落库金额；异常保守沿用全量热计数，不因损坏记录放宽额度；不消费或重写账单。
func (c *Client) HotSpendSince(id string, since time.Time) float64 {
	if c == nil || id == "" {
		return 0
	}
	value, err := organizationPendingScript.Run(context.Background(), c.rdb,
		[]string{"xhub:spendlog", "xhub:spend:indexed", "xhub:spend:org-pending:" + id, "xhub:spend:requests"}, since.UnixMicro()).Float64()
	if err == nil {
		return value
	}
	// 旧进程入队的记录没有时间索引。过渡期以原队列为事实源，待它们确认后自动恢复索引读取。
	rows, readErr := c.rdb.LRange(context.Background(), "xhub:spendlog", 0, -1).Result()
	if readErr != nil {
		return c.HotSpend(SpendRef("org", id))
	}
	var total float64
	for _, raw := range rows {
		var row SpendLog
		if json.Unmarshal([]byte(raw), &row) != nil {
			return c.HotSpend(SpendRef("org", id))
		}
		if row.OrgID != id || !row.SpendValid {
			continue
		}
		started, parseErr := time.Parse(time.RFC3339Nano, row.Start)
		// 无时间的旧记录无法证明属于上周期，保守计入，和落库时的时间回退规则一致。
		if parseErr != nil || !started.Before(since) {
			total += row.Spend
		}
	}
	return total
}

// HotUserSpendSince 汇总用户本周期待落库费用；id和since指定个人周期，不修改队列。
// 准入调用；一次队列快照避免扫描期间变动，损坏记录或读取失败返回NaN使准入明确失败。
func (c *Client) HotUserSpendSince(id string, since time.Time) float64 {
	if c == nil || id == "" {
		return 0
	}
	if c.rdb == nil {
		return math.NaN()
	}
	rows, err := c.rdb.LRange(context.Background(), "xhub:spendlog", 0, -1).Result()
	if err != nil {
		return math.NaN()
	}
	var total float64
	for _, raw := range rows {
		var row SpendLog
		if json.Unmarshal([]byte(raw), &row) != nil {
			return math.NaN()
		}
		if row.UserID != id || !row.SpendValid {
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, row.Start)
		if err != nil || !started.Before(since) {
			total += row.Spend
		}
	}
	return total
}

var organizationPendingScript = redis.NewScript(`
-- 全局待结算索引的数量必须覆盖整个队列，才能确认没有旧进程写入的未索引账单。
if redis.call('SCARD', KEYS[2]) ~= redis.call('LLEN', KEYS[1]) then return nil end
local total = 0
for _, id in ipairs(redis.call('ZRANGEBYSCORE', KEYS[3], ARGV[1], '+inf')) do
  local cost = tonumber(redis.call('HGET', KEYS[4], id))
  if not cost then return nil end
  total = total + cost
end
return tostring(total)
`)

// PendingSpendCount 为硬额度启用检查读取队列长度；无 Redis 返回零，连接或类型错误必须返回错误。
// 不消费队列，调用方在真实 Flush 后用它确认遗留未预占费用已全部处理。
func (c *Client) PendingSpendCount() (int64, error) {
	if c == nil {
		return 0, nil
	}
	if c.rdb == nil {
		return 0, fmt.Errorf("Redis unavailable")
	}
	return c.rdb.LLen(context.Background(), "xhub:spendlog").Result()
}

// HotKeySpend 按稳定 KeyID 汇总尚未落库的消费，供准入与累计费用头使用，不消费队列。
// 参数 id 为 api_keys 行 ID；空客户端或空 ID 返回零。损坏返回错误，依赖该费用的预算准入必须拒绝；展示可省略。
// 新队列使用每 Key 请求索引；混有旧进程账单时在同一个 Lua 快照内扫描，避免并发确认造成漏费。
// 旧 Key 账单缺少 KeyID 时无法证明轮换前归属，保守返回错误；排空后惰性清理旧 flusher 遗留的索引成员。
func (c *Client) HotKeySpend(id string) (float64, error) {
	if c == nil || id == "" {
		return 0, nil
	}
	if c.rdb == nil {
		return 0, fmt.Errorf("key spend: Redis client is unavailable")
	}
	return keyPendingScript.Run(context.Background(), c.rdb, []string{"xhub:spendlog", "xhub:spend:key-indexed", "xhub:spend:key-pending:" + id, "xhub:spend:requests"}, id).Float64()
}

// keyQueueSnapshotLua 用队列长度及首尾摘要检测旧进程未同步维护新索引的入队或确认。
// 新 writer 只能延续已验证快照；未知旧队列保持回退直到排空，避免集合数量碰巧相等时漏算费用。
const keyQueueSnapshotLua = `
local function keyQueueSnapshot()
  local n = redis.call('LLEN', 'xhub:spendlog')
  return tostring(n) .. ':' .. redis.sha1hex(redis.call('LINDEX', 'xhub:spendlog', 0) or '') .. ':' .. redis.sha1hex(redis.call('LINDEX', 'xhub:spendlog', -1) or '')
end
local function keyIndexTrusted()
  return redis.call('LLEN', 'xhub:spendlog') == 0 or redis.call('GET', 'xhub:spend:key-queue-state') == keyQueueSnapshot()
end
`

var keyPendingScript = redis.NewScript(keyQueueSnapshotLua + `
local total = 0
local function add(raw)
  local cost = tonumber(raw)
  if not cost or cost < 0 or cost ~= cost or cost == math.huge or total + cost == math.huge then
    error('invalid pending key cost')
  end
  total = total + cost
end
-- 数量不一致说明存在旧 writer 或未能证明 KeyID 的历史记录；整个回退也必须原子读取。
if not keyIndexTrusted() or redis.call('SCARD', KEYS[2]) ~= redis.call('LLEN', KEYS[1]) then
  for _, raw in ipairs(redis.call('LRANGE', KEYS[1], 0, -1)) do
    local row = cjson.decode(raw)
    if type(row) ~= 'table' or type(row.request_id) ~= 'string' or row.request_id == '' then
      return redis.error_reply('invalid pending key event')
    end
    if row.spend_valid then
      local cost = tonumber(row.spend)
      if not cost or cost < 0 or cost ~= cost or cost == math.huge then return redis.error_reply('invalid pending key cost') end
      if row.key_id == ARGV[1] then add(cost)
      elseif cost > 0 and row.api_key and row.api_key ~= '' and (not row.key_id or row.key_id == '') then
        return redis.error_reply('legacy pending key event has no key_id')
      end
    end
  end
else
  for _, id in ipairs(redis.call('SMEMBERS', KEYS[3])) do
    if redis.call('SISMEMBER', KEYS[2], id) == 1 then add(redis.call('HGET', KEYS[4], id))
    else redis.call('SREM', KEYS[3], id) end
  end
end
return tostring(total)
`)

// SpendLog 是网关发送到 Redis 队列的单次用量载荷，消费者转换为 iam.UsageRecord 写 PostgreSQL。
// RequestID 用于去重结算，SessionID 用于授权范围内归组，字段记录调用时事实而非刷库时配置。
type SpendLog struct {
	// 已验证完整用量的预算尝试 ID，随队列保留至 PostgreSQL 同事务结算。
	BudgetReservationID string `json:"budget_reservation_id,omitempty"`
	// 客户端明确指定的推理强度快照；独立于提示词保存，空值表示未指定或历史未记录，不推断厂商默认值。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// 客户端实际请求路径，不含查询串；由执行器记录，独立于提示词存储。
	InboundEndpoint string `json:"inbound_endpoint"`
	// 实际发送的上游路径，由传输层观察；未发送时为空。
	OutboundEndpoint string `json:"outbound_endpoint"`
	// 是否实际流式执行，供性能统计区分首字延迟与非流完整响应耗时。
	Streamed bool `json:"streamed"`
	// 调用时供应商连接身份快照；连接修改后不重写历史归属。
	Supplier string `json:"supplier,omitempty"`
	// 调用时上游基础地址快照，已剥离认证信息，不随部署修改或删除改变。
	APIBase string `json:"api_base,omitempty"`
	// 实际发送的上游型号快照，与公开 Model 分开保存。
	UpstreamModel string `json:"upstream_model,omitempty"`
	// 调用日志/账单事件唯一标识，用于关联正文及结算去重；不能用 SessionID 替代。
	RequestID string `json:"request_id"`
	// 执行操作类别，如 chat 或官方端点动作，用于日志及用量分类。
	CallType string `json:"call_type"`
	// 请求日志采用的公开模型/组别名，由 RecordSpend 的 alias 传入；实际上游型号由 UpstreamModel 单独记录。
	Model string `json:"model"`
	// 调用密钥哈希，供实时用量引用；不是明文凭据或 api_keys 行 ID。
	APIKey string `json:"api_key"`
	// 鉴权时 api_keys 行 ID 快照，用于日志权限和账单归属；会话用户可为空。
	KeyID string `json:"key_id,omitempty"`
	// 非负输入 token 数，无实测用量时为 0。
	Prompt int `json:"prompt_tokens"`
	// 非负输出 token 数，无实测用量时为 0。
	Completion int `json:"completion_tokens"`
	// 计算费用，单位沿用平台账单币种；失败或响应缓存命中为 0。
	Spend float64 `json:"spend"`
	// 费用是否已计算，区分未定价与已确认免费，影响实时费用累计。
	SpendValid bool `json:"spend_valid"`
	// 请求开始时间的序列化值，队列消费者用来恢复事件时间。
	Start string `json:"start"`
	// 请求结束时间的序列化值，队列消费者用来恢复调用耗时。
	End string `json:"end"`
	// 是否返回网关响应缓存；命中仍记录用量但推理费用为 0，不提交上游成功偏好。
	CacheHit bool `json:"cache_hit"`
	// 成功或失败分类，用于日志筛选；精确响应码见 HTTPStatus。
	Status string `json:"status"`
	// 最终返回客户端的 HTTP 状态码；0 表示未保存该事实。
	HTTPStatus int `json:"http_status,omitempty"`
	// 失败的原始错误正文，供详情诊断；成功为空，沿用现有脱敏规则。
	Error string `json:"error,omitempty"`
	// 鉴权时账单归属类型快照，与各层 ID 一起确定用量归属。
	OwnerType string `json:"owner_type,omitempty"`
	// 调用时团队 ID 快照，未归属团队时为空，不随后续成员关系变化。
	TeamID string `json:"team_id"`
	// 实际调用用户的 ID 快照，用于日志权限隔离和账单归属。
	UserID string `json:"user_id"`
	// 调用时组织 ID 快照，未归属组织时为空。
	OrgID string `json:"org_id"`
	// 调用时项目 ID 快照，未归属项目时为空。
	ProjectID string `json:"project_id,omitempty"`
	// 按提示词存储设置保留的请求内容，关闭存储时为空，不进入粘性载荷。
	Messages string `json:"messages,omitempty"`
	// 按提示词存储设置保留的响应内容，关闭存储时为空。
	Response string `json:"response,omitempty"`
	// 已脱敏的上游头和原始统计 JSON，独立于提示词存储。
	UpstreamResponse string `json:"upstream_response,omitempty"`
	// 网关方法、路径、脱敏请求头等元数据 JSON，供详情诊断。
	ProxyRequest string `json:"proxy_server_request,omitempty"`
	// 流请求的首字节或非流请求的完整响应耗时，单位毫秒；nil 表示没有测量。
	TTFTMs *int `json:"ttft_ms,omitempty"`
	// 调用密钥哈希快照，用于定位身份，不是明文密钥。
	KeyHash string `json:"key_hash,omitempty"`
	// 调用时密钥显示名称快照，名称变化后不重写旧日志。
	KeyAlias string `json:"key_alias,omitempty"`
	// 调用时团队显示名称快照，供日志展示。
	TeamAlias string `json:"team_alias,omitempty"`
	// 执行供应商/适配器标识，区别于具体供应商连接 ID。
	Provider string `json:"provider,omitempty"`
	// 上游报告的缓存输入 token 数；nil 表示没有统计，0 表示明确没有命中。
	CachedTokens *int `json:"cached_tokens,omitempty"`
	// SessionID 的识别来源，不是供应商来源或路由策略。由 PlanRoute 生成，经 CallNote 写入日志。
	// explicit：标准 X-Session-Id/正文 session_id；parent：授权父响应继承；
	// cache_key：prompt_cache_key 哈希；prompt：稳定首轮内容指纹；generated：随机 UUID。
	// cache_key/prompt 在日志中标为推断；不参与权限判断。空值表示未记录来源，omitempty 仅省略空 JSON 字段。
	SessionSource string `json:"session_source,omitempty"`
	// 明确、继承、推断或生成的会话标识，用于响应头和授权范围内日志归组；不是响应 ID，不授予历史读取权限。
	SessionID string `json:"session_id,omitempty"`
	// 网关响应缓存键，与会话指纹、原生 prompt_cache_key 及偏好缓存键相互独立。
	CacheKey string `json:"cache_key,omitempty"`
	// 护栏检查结果 JSON 数组，日志抽屉读取；空值表示未记录检查结果。
	Guardrail string `json:"guardrail,omitempty"`
	// 客户端备注，仅用于日志关联，不授予权限或证明测试通过。
	Label string `json:"label,omitempty"`
	// 实际费率和计费数量 JSON 快照，读取日志不按现价重算；空值表示未定价。
	PriceSnapshot string `json:"price_snapshot,omitempty"`
}

// EnqueueSpend 将网关用量日志与各层未落库花费原子入队，是唯一的队列写入口。
// 参数 row 的 RequestID 必须是全局唯一的不可变事件标识；零费用日志仍须具有标识。
// 返回 error：客户端不可用、身份或费用/token 非法、Redis 写入失败时非 nil；重复请求无副作用。
// 首个 payload 生效，确认后重放也不重复扣费；去重身份不设置 TTL，依赖部署的 Redis 持久化。
// 调用：gateway/spend.go；不会等待磁盘 fsync 或副本 quorum。测试：redis_test.go 覆盖边界和失败原子性。
func (c *Client) EnqueueSpend(row SpendLog) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("enqueue spend: Redis client is unavailable")
	}
	if row.Spend < 0 || math.IsNaN(row.Spend) || math.IsInf(row.Spend, 0) || row.Prompt < 0 || row.Completion < 0 || (row.CachedTokens != nil && *row.CachedTokens < 0) {
		return fmt.Errorf("enqueue spend: invalid cost or token count")
	}
	if row.RequestID == "" {
		return fmt.Errorf("enqueue spend: request_id is required")
	}
	raw, err := json.Marshal(row)
	if err != nil {
		return err
	}
	started, err := time.Parse(time.RFC3339Nano, row.Start)
	// 缺失时间无法归入旧周期，直到实际落库确认前始终保守计入，和兼容队列读取保持一致。
	startedMicros := int64(math.MaxInt64)
	if err == nil {
		startedMicros = started.UnixMicro()
	}
	args := []any{raw, 0.0, row.RequestID, startedMicros, row.OrgID, row.KeyID, row.APIKey}
	if row.SpendValid {
		args[1] = row.Spend
	}
	for _, ref := range []struct{ kind, id string }{
		{"key", row.APIKey}, {"team", row.TeamID}, {"user", row.UserID},
		{"org", row.OrgID}, {"project", row.ProjectID},
	} {
		id := SpendRef(ref.kind, ref.id)
		if id != "" {
			args = append(args, id)
		}
	}
	return enqueueSpendScript.Run(context.Background(), c.rdb, []string{"xhub:spendlog", "xhub:spend:requests"}, args...).Err()
}

var enqueueSpendScript = redis.NewScript(keyQueueSnapshotLua + `
if redis.call('HEXISTS', KEYS[2], ARGV[3]) == 1 then
  return 0
end
local delta = tonumber(ARGV[2])
-- Lua 出错不会回滚此前写入，因此先检查队列类型与全部金额，再发布日志。
-- 不维护无人消费的旧主体索引；队列记录自身携带确认所需的全部归属。
-- OOM 等基础设施故障仍须运维恢复，Redis 并不是数据库事务 outbox。
local queueType = redis.call('TYPE', KEYS[1]).ok
if queueType ~= 'none' and queueType ~= 'list' then
  return redis.error_reply('spend queue is not a list')
end
local indexType = redis.call('TYPE', 'xhub:spend:indexed').ok
if indexType ~= 'none' and indexType ~= 'set' then return redis.error_reply('invalid pending index') end
local state = redis.call('GET', 'xhub:spend:key-queue-state')
local trusted = keyIndexTrusted()
local wasEmpty = redis.call('LLEN', KEYS[1]) == 0
local keyIndexType = redis.call('TYPE', 'xhub:spend:key-indexed').ok
if keyIndexType ~= 'none' and keyIndexType ~= 'set' then return redis.error_reply('invalid key coverage index') end
local keyIndex = 'xhub:spend:key-pending:' .. ARGV[6]
local keyType = redis.call('TYPE', keyIndex).ok
if keyType ~= 'none' and keyType ~= 'set' then return redis.error_reply('invalid key pending index') end
local orgIndex = 'xhub:spend:org-pending:' .. ARGV[5]
local orgType = redis.call('TYPE', orgIndex).ok
if orgType ~= 'none' and orgType ~= 'zset' then return redis.error_reply('invalid organization pending index') end
for i = 8, #ARGV do
  if delta ~= 0 then
    local raw = redis.call('GET', 'xhub:spend:' .. ARGV[i])
    local current = raw and tonumber(raw) or 0
    if (raw and not tonumber(raw)) or current + delta ~= current + delta or
       math.abs(current + delta) == math.huge then
      return redis.error_reply('invalid hot spend counter')
    end
  end
end
-- 旧 flusher 可能留下索引；空队列可证明旧成员均已确认，安全开始新的覆盖集合。
if wasEmpty then redis.call('DEL', 'xhub:spend:key-indexed') end
for i = 8, #ARGV do
  if delta ~= 0 then
    redis.call('INCRBYFLOAT', 'xhub:spend:' .. ARGV[i], delta)
  end
end
redis.call('RPUSH', KEYS[1], ARGV[1])
redis.call('HSET', KEYS[2], ARGV[3], ARGV[2])
redis.call('SADD', 'xhub:spend:indexed', ARGV[3])
-- 无 Key 的会话账单也计入覆盖集合；无法定位的旧 Key 费用故意不标记覆盖，强制保守回退。
if ARGV[6] ~= '' or ARGV[7] == '' or delta == 0 then redis.call('SADD', 'xhub:spend:key-indexed', ARGV[3]) end
if ARGV[6] ~= '' and delta ~= 0 then redis.call('SADD', keyIndex, ARGV[3]) end
if ARGV[5] ~= '' and delta ~= 0 then redis.call('ZADD', orgIndex, ARGV[4], ARGV[3]) end
if trusted then redis.call('SET', 'xhub:spend:key-queue-state', keyQueueSnapshot()) else redis.call('DEL', 'xhub:spend:key-queue-state') end
return 1
`)

// PeekLogs 为 dataplane Flush 查看最多 n 条花费日志而不删除，返回解码记录及对应原始字符串。
// 客户端为空、n 小于一、读取失败或队列为空时返回 nil；损坏事件只返回其前面的合法前缀，不能越过坏记录。
// 测试：redis_test.go 覆盖正常队列和损坏队头，cmd/regression/spend_queue_test.go 覆盖刷写失败保留记录。
func (c *Client) PeekLogs(n int) (rows []SpendLog, raw []string) {
	if c == nil || n < 1 {
		return nil, nil
	}
	vals, err := c.rdb.LRange(context.Background(), "xhub:spendlog", 0, int64(n-1)).Result()
	if err != nil || len(vals) == 0 {
		return nil, nil
	}
	out := make([]SpendLog, 0, len(vals))
	for i, item := range vals {
		var row SpendLog
		if json.Unmarshal([]byte(item), &row) != nil || row.RequestID == "" {
			// 不能确认无法解码的事件，否则会永久丢失用量；坏队头必须显式修复后才能继续刷写。
			util.Error("spend queue contains an invalid event at offset %d", i)
			return out, vals[:i]
		}
		out = append(out, row)
	}
	return out, vals
}

// AckFlushed 在数据库事务成功后，原子释放各主体热花费并确认已写入的队列前缀。
// 参数 deltas 为主体花费标识到本批金额的映射，n 为记录数，head 为 PeekLogs 返回的首条原文。
// 返回 error：非法金额或 Redis 失败时非 nil；空客户端、空批次和队头已经改变时无副作用。
// 调用：dataplane/runtime.Flush；队头校验防止成功确认后重试重复扣减，金额预检防止只释放部分主体。
// 测试：redis_test.go 和 cmd/regression/spend_queue_test.go 验证确认原子性、重复确认及故障恢复。
func (c *Client) AckFlushed(deltas map[string]float64, n int, head string) error {
	if c == nil || n < 1 || head == "" {
		return nil
	}
	for _, delta := range deltas {
		if delta < 0 || math.IsNaN(delta) || math.IsInf(delta, 0) {
			return fmt.Errorf("ack spend: invalid delta")
		}
	}
	args := make([]any, 0, 2+len(deltas)*2)
	args = append(args, n, head)
	for id, delta := range deltas {
		if id == "" || delta == 0 {
			continue
		}
		args = append(args, id, delta)
	}
	return ackFlushScript.Run(context.Background(), c.rdb, []string{"xhub:spendlog"}, args...).Err()
}

var ackFlushScript = redis.NewScript(keyQueueSnapshotLua + `
local head = redis.call('LINDEX', KEYS[1], 0)
if (not head) or head ~= ARGV[2] then
  return 0
end
local n = tonumber(ARGV[1])
local rows = {}
local indexType = redis.call('TYPE', 'xhub:spend:indexed').ok
if indexType ~= 'none' and indexType ~= 'set' then return redis.error_reply('invalid pending index') end
local state = redis.call('GET', 'xhub:spend:key-queue-state')
local trusted = keyIndexTrusted()
local wasEmpty = redis.call('LLEN', KEYS[1]) == 0
local keyIndexType = redis.call('TYPE', 'xhub:spend:key-indexed').ok
if keyIndexType ~= 'none' and keyIndexType ~= 'set' then return redis.error_reply('invalid key coverage index') end
for _, raw in ipairs(redis.call('LRANGE', KEYS[1], 0, n - 1)) do
  local row = cjson.decode(raw)
  local orgType = redis.call('TYPE', 'xhub:spend:org-pending:' .. (row.org_id or '')).ok
  if orgType ~= 'none' and orgType ~= 'zset' then return redis.error_reply('invalid organization pending index') end
  local keyType = redis.call('TYPE', 'xhub:spend:key-pending:' .. (row.key_id or '')).ok
  if keyType ~= 'none' and keyType ~= 'set' then return redis.error_reply('invalid key pending index') end
  table.insert(rows, row)
end
-- Lua 错误不回滚已经执行的写入，必须在扣减前预检全部主体计数。
for i = 3, #ARGV, 2 do
  local raw = redis.call('GET', 'xhub:spend:' .. ARGV[i])
  local current = raw and tonumber(raw) or 0
  local delta = tonumber(ARGV[i + 1])
  if not delta or (raw and not tonumber(raw)) or
     current - delta ~= current - delta or math.abs(current - delta) == math.huge then
    return redis.error_reply('invalid hot spend counter')
  end
end
local i = 3
while i <= #ARGV do
  local id = ARGV[i]
  local delta = tonumber(ARGV[i + 1])
  if id ~= '' and delta ~= 0 then
    redis.call('INCRBYFLOAT', 'xhub:spend:' .. id, -delta)
  end
  i = i + 2
end
redis.call('LTRIM', KEYS[1], n, -1)
for _, row in ipairs(rows) do
  redis.call('SREM', 'xhub:spend:indexed', row.request_id)
  redis.call('SREM', 'xhub:spend:key-indexed', row.request_id)
  if row.key_id and row.key_id ~= '' then redis.call('SREM', 'xhub:spend:key-pending:' .. row.key_id, row.request_id) end
  if row.org_id and row.org_id ~= '' then redis.call('ZREM', 'xhub:spend:org-pending:' .. row.org_id, row.request_id) end
end
if redis.call('LLEN', KEYS[1]) == 0 then redis.call('DEL', 'xhub:spend:key-indexed') end
if trusted or redis.call('LLEN', KEYS[1]) == 0 then redis.call('SET', 'xhub:spend:key-queue-state', keyQueueSnapshot()) else redis.call('DEL', 'xhub:spend:key-queue-state') end
return 1
`)

// GetString reads one Redis string. A missing key returns ok false.
// 参数 ctx（context.Context）：上下文，取消时停止；key（string）：缓存或配置表的键。
// 返回 string（string）：读到的字符串。键不存在时为空串，调用方要把空串当成没有钉住；bool（bool）：Redis 里存在这个字符串键时返回真。没有这个键时返回假。
// 调用：gateway/affinity.go
// 测试：无直接单测
func (c *Client) GetString(ctx context.Context, key string) (string, bool) {
	if c == nil || c.rdb == nil || key == "" {
		return "", false
	}
	v, err := c.rdb.Get(ctx, key).Result()
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}

// SetString stores one Redis string with a TTL. A nil client does nothing.
// 参数 ctx（context.Context）：上下文，取消时停止；key（string）：缓存或配置表的键；value（string）：写入字符串使用的值。空串表示调用方没有提供这项；ttl（time.Duration）：一段时间。零值表示改用调用方约定的默认时长，例如会话一小时、官方任务七天。
// 返回：无。这条带 TTL 的字符串已写入 Redis。客户端或键为空时什么都不写。
// 调用：gateway/affinity.go
// 测试：无直接单测
func (c *Client) SetString(ctx context.Context, key, value string, ttl time.Duration) {
	if c == nil || c.rdb == nil || key == "" {
		return
	}
	_ = c.rdb.Set(ctx, key, value, ttl).Err()
}
