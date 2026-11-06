# 运行行为与限制

按执行路径判断保证，不按表单里有没有字段判断。

## 验证入口

~~~bash
go test ./internal/dataplane/... ./internal/live -count=1
~~~

| 路径 | 保证与边界 |
| --- | --- |
| 完整响应缓存 | 非流式，进程内 LRU；无 TTL、跨实例失效保证 |
| Idempotency-Key | 鉴权后非流式请求在当前进程内重放；PostgreSQL 保存摘要 24 小时，跨实例或重启后返回 `409 idempotency_result_unavailable`，不保存响应正文；数据库不可用或登记超时返回 `503 idempotency_unavailable` |
| 流式响应 | 失败不更新成功缓存/粘性；输出后不能重新开始 |
| 原生任务 | 使用创建身份及登记路径查询 |
| 任务结算 | 成功结果的实测用量，PostgreSQL 去重 |
| 任务上下文 | Redis 保存七天；无 Redis 时仅当前进程 |
| 护栏 | 普通 Chat/Responses 调用前文本检查，不覆盖每条 Bypass |

没有后台补查结算或预算预占。创建结果不确定时保留已有任务 ID，不盲目重发付费任务。提示词保存开关不提供自动加密或留存清理；资源目录存在不代表全部操作实现，未实现操作返回 501。

## 源码

[任务执行](../../../internal/dataplane/execution/official.go) · [数据面模块](../../../internal/dataplane/readme_cn.md) · [计价](pricing.md)
