# 开发指南

| 要做什么 | 从这里开始 |
| --- | --- |
| 添加供应商 / 新协议 | [完整 Go 注册示例](provider-catalog.md) |
| 修改模型表单 / 端点 | [模型与端点](model-endpoints.md)、[认证表单](provider-credential-forms.md) |
| 修改路由 | [模板 JSON 与执行规则](routing.md)、[路由组](routing-groups.md) |
| 修改会话保持 / 供应商粘性 / 日志归组 | [推荐设计](session-affinity-design.md) |
| 修改权限 / 限额 | [权限](permissions.md)、[额度链](quota-chain.md) |
| 修改费用 / 护栏 | [计价](pricing.md)、[XGo 示例](guardrails.md) |
| 查 HTTP 入口 | [接口与源码](api-reference.md) |
| 运行验证 | [测试命令](testing.md)、[后台回归](regression.md)、[浏览器 E2E](e2e-regression.md) |
| 保持功能并改善完整性 | [接口契约与渐进解耦实施计划](interface-integrity-roadmap.md)、[接口和测试基线](interface-integrity-baseline.md) |

## 一次修改


~~~bash
 git status --short
 rg 'RegisterTransport' internal/provider
 go test ./internal/provider/... -count=1
~~~

先读修改目录的 AGENTS.md、CLAUDE.md 和包 README。产品修改同时补单元、后台 regression 和浏览器 E2E；测试数据使用独立 schema，并在结束时清理。

## 不要破坏

- 服务端重新鉴权；隐藏按钮不能代替授权。
- 模板不能扩大模型权限；流已输出后不能换供应商续写。
- 历史费用读取当次快照；数据库提交后才确认 Redis 队列。
- 中英文文案同步；请求示例与提示词使用英文。
- 不手改 schema.d.ts：当前缺少 Go OpenAPI 生成源，gen:api 会明确失败。用真实 HTTP regression 和前端消费者测试核对契约。

[请求链](architecture.md) · [代码地图](implementation.md) · [操作指南](../../user-guide.zh-CN.md)

- [提示词与搜索测试契约](real-probe-contracts.md)
