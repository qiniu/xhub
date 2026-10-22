# API 准入、用量刷写与日志查询

本文说明当前源码的执行路径和可观察边界。

## 请求准入

[网关限额检查](../../../internal/gateway/limits.go)读取当前密钥、用户、团队和组织状态，检查模型权限、预算及速率。首次准入使用 QuotaAndRates；目标复核使用 QuotaPath，不重复占用分钟容量。项目预算是并行上限。

[额度计算](../../../internal/iam/quota.go)构建相关组织额度树，处理固定分配、共享额度及后代保留权。预算合并 PostgreSQL 已落库消费与 Redis 热消费；请求成本不能保证只与当前路径深度有关。额度检查不等于转发前原子预占，不能承诺并发请求绝不超支。无团队个人主体仍检查用户和密钥上限；未设置上限不限制该层，明确为零的上限生效。

存储失败响应取决于分支：授权存储不可用返回 503，身份读取和额度链错误可能进入 401 分支；预算超限返回 403（`budget_exceeded`），RPM/TPM 限流返回 429。

## 用量持久化

[Redis 用量队列](../../../internal/live/redis.go)按 request_id 去重，事件入队并累加各主体热消费，保存调用时身份、价格及用量快照。去重标识不设置 TTL；发布不等待磁盘 fsync 或副本 quorum，可靠性依赖 Redis 部署持久化策略。

[后台刷写](../../../internal/dataplane/runtime/live.go)由周期计时器触发，每轮最多读取 500 条事件。general_settings.spend_log_flush_interval_seconds 省略时默认 5 秒，允许整数 1～86400，显式非法值导致配置加载失败，修改后重启生效；直接构造设置的非法值回退为 5 秒。见 [配置实现](../../../internal/config/config.go)。

刷写先调用 [RecordUsage](../../../internal/iam/usage.go)，在 PostgreSQL 事务中保存明细、正文、日统计及各级消费，按请求标识保持幂等。提交成功后才校验 Redis 队头、确认该批事件并扣回热消费。数据库失败保留整批；损坏事件只允许刷写此前有效前缀，损坏队头保留等待修复。

数据库提交与 Redis 确认是独立操作。确认失败可能重试，数据库幂等防止重复记账；预算观察可能暂时同时包含已提交消费和未确认热增量。当前没有持续排空积压或多实例消费者租约的保证。默认 5 秒是触发周期，不能保证所有日志 5 秒内可查询；积压、故障和事务耗时会延长可见时间。取消循环后正在执行的刷写完成，不额外清空队列。

## 日志查询

[用量查询](../../../internal/iam/usage_read.go)先应用授权范围和筛选。普通日志使用 limit / offset 分页；会话列表筛选后合并会话、计数并分页，不使用独立会话汇总表。排序字段由白名单选择，未知字段回退到时间排序。任意请求标识不授予 Redis 正文读取权限。尚未落库的事件可能暂时查询不到；接口没有统一 pending 状态契约。

## 验证边界

[基准测试指南](../../../benchmarks/README.md)说明如何运行隔离测量并解读结果。`benchmarks/reports/staircase/report.md`、`benchmarks/reports/validation/summary.md` 等报告是被 Git 忽略的本地产物，不包含在全新检出或发布的网站中。每份报告只代表记录的机器、版本、数据集和依赖配置，不能保证当前生产吞吐或延迟。供应商耗时、媒体任务时间和网关开销需要分别测量。实现测试位于 internal/config、internal/live、internal/dataplane/runtime、internal/iam 和 cmd/regression；真实 HTTP、PostgreSQL、Redis 与浏览器测试使用隔离数据并清理。外部供应商验证需要独立凭据和显式启用。
