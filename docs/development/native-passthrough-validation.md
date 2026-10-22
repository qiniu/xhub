# 原生对话透传兼容与验证

关联提案：https://github.com/qiniu/xhub/issues/112

## 支持范围

- OpenAI Chat：`POST /bypass/openai/v1/chat/completions`。
- OpenAI Responses：`POST /bypass/openai/v1/responses`。
- Anthropic Messages：`POST /bypass/anthropic/v1/messages`。
- Anthropic Token 计数：`POST /v1/messages/count_tokens` 和 `POST /bypass/anthropic/v1/messages/count_tokens`。标准入口沿用 Messages 权限，透传入口要求显式开启，不进行跨协议转换。
- 可见公开模型详情：`GET /v1/models/{id}`。沿用列表权限；带 `anthropic-version` 返回 Anthropic 元数据，否则返回 OpenAI 元数据。时间为兼容占位值，不代表供应商发布日期。

计数仍验证身份、模型权限及现有准入限制，但不预留生成预算、不结算生成 Token、不提交生成响应归属。成功与失败结果都记录日志。原生未知 JSON 字段、大整数、查询参数、协议头和 SSE 由已有透传链处理；客户端鉴权不会替代服务端上游凭据。

## 定向验证

在隔离环境使用本地 HTTP 上游及真实后台和数据库，未使用生产上游 Key。测试资源由测试清理或隔离 schema 回收。

| 命令 | 结果 |
| --- | --- |
| `go test -json ./internal/provider/anthropic ./internal/provider/deployment ./internal/dataplane/execution ./internal/gateway/models` | 297 个测试/子测试通过，0 失败，2 跳过 |
| `python3 -m unittest e2e.test_fake_upstream.ArkUpstreamTest.test_native_token_count -v` | 1 通过，0 失败 |
| `bash scripts/regression.sh -v '^(TestAnthropicTokenCountContract|TestAgentNativeProtocolMatrix|TestNativeRefusalAndLifecycleContract|TestErrorLogFilterContract)$'` | 4 个顶层测试及 7 个子测试通过，0 失败 |
| `E2E_RUN_DIR=/tmp/xhub-native-count-final bash scripts/e2e.sh native-token-count.test.ts agent-native-protocol.test.ts error-filter-contract.test.ts` | 4 通过，0 失败 |

浏览器流程覆盖管理员登录、创建测试部署和 Key、模型查询、两种入口计数、零费用日志详情及删除测试资源；同时回归推理/工具续接和中英文错误筛选。后台覆盖 200/402/403/429/500 原始错误与日志状态一致、鉴权/模型可见性、缺少模型及零费用。

本次本地报告：`output/native-passthrough/`；浏览器报告：`/tmp/xhub-native-count-final/html/index.html`、`results.json`、`junit.xml`。报告为本机产物，不作为共享附件提交。

## 外部依赖与边界

- 两个跳过项为七牛 Seedance/Fal 外部凭据测试，与本次对话计数无关。
- 本次不声称支持全部供应商 API：OpenAI Responses 查询/删除/取消/列表、Realtime/WebSocket、Anthropic Batches/Files 未纳入支持；Anthropic 模型列表目前仍使用现有 OpenAI 格式，未提供原厂分页。
- 真实上游余额不足 402 或权限不足 403 需处理上游账号/产品权限，不能靠绕过 XHub 鉴权解决。
- 入口 WAF 可以在请求到达 XHub 前返回 403，这些请求不会产生应用调用日志，需关联 OpenResty/WAF 审计日志。客户端 403 与应用 501 未经同一请求关联不能视为同一错误。
- 本次代码尚未部署到生产；定向测试不等同于生产上游联调。
