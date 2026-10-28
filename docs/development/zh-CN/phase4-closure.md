# 第四阶段固定收尾清单

> C1 已提交 [#224](https://github.com/qiniu/xhub/pull/224)：26条辅助接口明确拒绝，另外2条单数策略查询补充验收。该PR未合入，下面的main基线状态保持不变。C2 已提交 [#226](https://github.com/qiniu/xhub/pull/226)：22条旧身份操作明确拒绝，真实密码登录、SAML、会话和预算已回归；同样尚未合入。剩余C3–C7及最终组合验收。

评审基线：`939e0d6c0ba174bd03d6dad4c1035da8bf0be8cd`，2026-10-28。**第四阶段未完成；未合并、未部署。**

## 审计范围与结论

固定范围是全部 303 条目录持有的管理声明：296 条 family、5 条供应商文件、1 条 passthrough WebSocket、1 条 realtime WebSocket。逐条归属、处理链、历史 HTTP 观察、状态和批次保存在 `internal/gateway/testdata/phase4-closure.json`。原始注册范围仍由 `route-ownership.json` 保存，包含 755 条目录声明和 95 条模块独有注册；声明不是唯一URL数量。

不能只核对 family 的296条；文件、WebSocket的包装处理器也可能有占位成功。`{provider}` 使用 `audit-fixture` 探测的是非OpenAI分支，**不能推广到openai分支**。WEBSOCKET方法观察不等于真实WebSocket客户端握手/会话验收。

303条目前分为：22条已有真实配置CRUD回归；94条代码确认语义错配/无执行器，部分已有待合入修复；8条明确不支持；179条仍缺逐操作语义验收。后者包括待合入配置服务、已有护栏能力、统计和设置等，**不是179个已确认bug**。不把证据不足塞进“已验证”，也不把未实现入口一律改成支持。

本次采集匿名、普通用户、管理员共909次真实本地HTTP请求。管理员状态：200有204次、404有87次、501有7次、204有3次、400和426各1次；94条观察到KV变化。测试使用同一隔离schema顺序探测、统一最小正文，ID可能碰撞；这些数字只证明响应/存储现象，**不是功能通过数、缺陷数量、有效输入成功率，也不是权限或无副作用的完整证明**。资源存在/缺失、合法正文、权限、失败原子性等由各批次独立真实回归验收。

## 固定批次与完成条件

| 批次 | 范围 | 完成条件 |
| --- | --- | --- |
| C1 | 已拆分服务的辅助入口 | 策略测试/解析/版本/附件/状态、凭据迁移/按模型查询、提示词版本逐条验收；不能把操作名写成配置ID；保留基本CRUD和真实护栏执行。 |
| C2 | OAuth/引导、邀请和JWT映射别名 | 以令牌、身份绑定实际副作用验证；无执行器明确拒绝；不改现有SSO和权限规则。 |
| C3 | 设置、品牌、配置导出、调度、预热、事件、Vantage | 保存后读回且对应消费者可观察；无消费者或执行器单独标明不支持；禁止修改现有配置安全边界。 |
| C4 | 模型、fallback、公开/封禁旧别名 | 与模型专用服务及真实调用结果对照，不能只验证KV；不改变授权与路由规则。 |
| C5 | 统计、支出、日志旧别名 | 使用确有成功、失败、消费的隔离夹具核对查询；重置/刷新不能假成功，不改预算算法。 |
| C6 | 供应商文件与WebSocket兼容入口 | 上传/读回/删除和真实协议握手/错误必须对应真实操作；测试openai与其他provider分流，保护现有数据面。 |
| C7 | 根页、注册表、路由及辅助目录 | 对照实际注册/消费者，不能将KV目录冒充路由/能力目录。 |
| integration | 既有待合入PR和最终组合验收 | 在隔离集成分支解决重叠修改，跑全套真实回归/E2E、权限/预算负例、兼容性与70%门禁；不得将单PR通过等同组合通过。 |

每批先写会失败的语义测试，再决定委派现有服务或明确拒绝。默认不新增功能，不因为“收尾”改变现有权限和预算规则。新发现只能挂到上述批次的精确入口；新增路由导致清单门禁失败，必须说明范围变化，而不是静默扩张。

## 待合入与清单外关联边界

- #182、#184、#188、#190、#192、#194、#196、#198、#200、#204、#206、#208、#210、#212、#214、#216、#218、#220、#222 尚未合入；本审计是 #186。PR编号是相关范围索引，**不是该组每个入口的已修复证明**。
- 模块接管入口也有组合验收项：#188标签列表、#190客户/终端用户列表、#194数据库readiness、#196缓存ping/settings/flush、#214 SkillHub目录/发布/安装；#216为无引用前端Key控件清理。不能因不在303条内而漏掉。
- 专用模块138条目录声明及95条独有注册：保留已有契约与真实回归，本轮只复核注册边界，不宣称逐条重新业务验收。以上关联PR须补充覆盖。
- 已移除89条：保留不挂载；mixed34条的退役语义、public11条元数据、data180条声明保留原入口和既有测试，不纳入新增管理功能。这些数量不是功能已支持声明。第五阶段再整理真实协议执行链；本轮发现的假成功不能借此排除。
- #202 SAML自动刷新单独跟踪；不算第四阶段收尾项。

## 分组明细

下表是有限队列。状态以main为准；相关PR测试结果不能直接改main状态。证据文件和逐入口观察在JSON中，路径参数示例不包含真实账号或凭据。

### 访问组与向量库配置（16条，integration）

现有通用配置存储；独立服务在待合入PR，配置保存不证明授权/索引生效。

相关待合入PR：#182、#184。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /access_group/{access_group}/delete` | 待补语义验收 |
| `DELETE /v1/access_group/{access_group_id}` | 待补语义验收 |
| `GET /access_group/list` | 待补语义验收 |
| `GET /access_group/{access_group}/info` | 待补语义验收 |
| `GET /access_groups` | 待补语义验收 |
| `GET /v1/access_group` | 待补语义验收 |
| `GET /v1/access_group/{access_group_id}` | 待补语义验收 |
| `GET /vector_store/list` | 待补语义验收 |
| `POST /access_group/new` | 待补语义验收 |
| `POST /v1/access_group` | 待补语义验收 |
| `POST /vector_store/delete` | 待补语义验收 |
| `POST /vector_store/info` | 待补语义验收 |
| `POST /vector_store/new` | 待补语义验收 |
| `POST /vector_store/update` | 待补语义验收 |
| `PUT /access_group/{access_group}/update` | 待补语义验收 |
| `PUT /v1/access_group/{access_group_id}` | 待补语义验收 |

### 统计、支出与日志旧别名（26条，C5）

通用KV读写/空统计不能证明真实账单查询或重置，须用有消费数据的夹具逐条验收。

| 精确入口 | main状态 |
| --- | --- |
| `GET /global/activity/exceptions` | 待补语义验收 |
| `GET /global/activity/exceptions/deployment` | 待补语义验收 |
| `GET /global/all_end_users` | 待补语义验收 |
| `GET /global/spend` | 待补语义验收 |
| `GET /global/spend/report` | 待补语义验收 |
| `GET /key/spend/report` | 待补语义验收 |
| `GET /model/metrics` | 待补语义验收 |
| `GET /model/metrics/exceptions` | 待补语义验收 |
| `GET /model/metrics/slow_responses` | 待补语义验收 |
| `GET /model/streaming_metrics` | 待补语义验收 |
| `GET /organization/spend/report` | 待补语义验收 |
| `GET /otel-spans` | 待补语义验收 |
| `GET /spend/logs` | 待补语义验收 |
| `GET /spend_logs/end_users` | 待补语义验收 |
| `GET /spend_logs/users` | 待补语义验收 |
| `GET /tag/dau` | 待补语义验收 |
| `GET /tag/distinct` | 待补语义验收 |
| `GET /tag/mau` | 待补语义验收 |
| `GET /tag/summary` | 待补语义验收 |
| `GET /tag/user-agent/per-user-analytics` | 待补语义验收 |
| `GET /tag/wau` | 待补语义验收 |
| `GET /team/spend/report` | 待补语义验收 |
| `GET /user/spend/report` | 待补语义验收 |
| `POST /global/spend/refresh` | 待补语义验收 |
| `POST /global/spend/reset` | 待补语义验收 |
| `POST /team/{team_id}/member/{user_id}/reset_spend` | 待补语义验收 |

### OAuth、邀请、JWT映射、引导旧入口（22条，C2）

这些精确路径仍读写KV，不是已修复的/authorize/flow或MCP OAuth；须验证令牌/身份副作用并明确支持边界。

相关待合入PR：#226。

| 精确入口 | main状态 |
| --- | --- |
| `GET /authorize` | 待修复（代码与观察证据） |
| `GET /discover` | 待修复（代码与观察证据） |
| `GET /enabled` | 待修复（代码与观察证据） |
| `GET /invitation/info` | 待修复（代码与观察证据） |
| `GET /jwt/key/mapping/info` | 待修复（代码与观察证据） |
| `GET /jwt/key/mapping/list` | 待修复（代码与观察证据） |
| `GET /onboarding/get_token` | 待修复（代码与观察证据） |
| `GET /token/generate` | 待修复（代码与观察证据） |
| `POST /delete` | 待修复（代码与观察证据） |
| `POST /introspect` | 待修复（代码与观察证据） |
| `POST /invitation/delete` | 待修复（代码与观察证据） |
| `POST /invitation/new` | 待修复（代码与观察证据） |
| `POST /invitation/update` | 待修复（代码与观察证据） |
| `POST /jwt/key/mapping/delete` | 待修复（代码与观察证据） |
| `POST /jwt/key/mapping/new` | 待修复（代码与观察证据） |
| `POST /jwt/key/mapping/update` | 待修复（代码与观察证据） |
| `POST /onboarding/claim_token` | 待修复（代码与观察证据） |
| `POST /register` | 待修复（代码与观察证据） |
| `POST /revoke` | 待修复（代码与观察证据） |
| `POST /test/connection` | 待修复（代码与观察证据） |
| `POST /token` | 待修复（代码与观察证据） |
| `POST /v3/login/exchange` | 待修复（代码与观察证据） |

### 预算配置与组织/团队旧入口（25条，integration）

预算配置独立服务及无执行器旧IAM入口拒绝待合入；不改变真实权限预算服务。

相关待合入PR：#192。

| 精确入口 | main状态 |
| --- | --- |
| `GET /budget/list` | 待补语义验收 |
| `GET /budget/settings` | 待补语义验收 |
| `GET /budgets` | 待补语义验收 |
| `GET /provider/budgets` | 待补语义验收 |
| `GET /team/filter/ui` | 待补语义验收 |
| `GET /team/metadata_schema` | 待补语义验收 |
| `GET /team/permissions_list` | 待补语义验收 |
| `GET /team/{team_id}/members/me` | 待补语义验收 |
| `PATCH /organization/member_update` | 待补语义验收 |
| `PATCH /team/{team_id}` | 待补语义验收 |
| `PATCH /v2/organization/{organization_id}` | 待补语义验收 |
| `POST /budget/delete` | 待补语义验收 |
| `POST /budget/info` | 待补语义验收 |
| `POST /budget/new` | 待补语义验收 |
| `POST /budget/update` | 待补语义验收 |
| `POST /organization/info` | 待补语义验收 |
| `POST /team/block` | 待补语义验收 |
| `POST /team/bulk_member_add` | 待补语义验收 |
| `POST /team/key/bulk_update` | 待补语义验收 |
| `POST /team/model/add` | 待补语义验收 |
| `POST /team/model/delete` | 待补语义验收 |
| `POST /team/permissions_bulk_update` | 待补语义验收 |
| `POST /team/permissions_update` | 待补语义验收 |
| `POST /team/unblock` | 待补语义验收 |
| `POST /team/{team_id}/disable_logging` | 待补语义验收 |

### 缓存管理（2条，integration）

缓存设置不生效的修复待合入；真实Flush仍保留。

相关待合入PR：#196。

| 精确入口 | main状态 |
| --- | --- |
| `GET /redis/info` | 待补语义验收 |
| `POST /cache/settings/test` | 待补语义验收 |

### 根页、路由/注册表及辅助目录（9条，C7）

返回通用KV不证明目录与已注册路由一致；现有前端/SDK调用方需核对。

| 精确入口 | main状态 |
| --- | --- |
| `GET /` | 待补语义验收 |
| `GET /end_user/daily/activity` | 待补语义验收 |
| `GET /network/client-ip` | 待补语义验收 |
| `GET /openapi-registry` | 待补语义验收 |
| `GET /registry.json` | 待补语义验收 |
| `GET /routes` | 待补语义验收 |
| `GET /search_tools/ui/available_providers` | 待补语义验收 |
| `GET /user/available_roles` | 待补语义验收 |
| `GET /utils/available_routes` | 待补语义验收 |

### 护栏管理与明确不支持分支（19条，retain）

本地规则有运行时验收；远程提交及部分UI能力明确拒绝。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /guardrails/{guardrail_id}` | 待补语义验收 |
| `GET /guardrails/list` | 待补语义验收 |
| `GET /guardrails/submissions` | 明确不支持 |
| `GET /guardrails/submissions/{guardrail_id}` | 明确不支持 |
| `GET /guardrails/ui/add_guardrail_settings` | 待补语义验收 |
| `GET /guardrails/ui/category_yaml/{category_name}` | 明确不支持 |
| `GET /guardrails/ui/major_airlines` | 明确不支持 |
| `GET /guardrails/ui/provider_specific_params` | 待补语义验收 |
| `GET /guardrails/{guardrail_id}` | 待补语义验收 |
| `GET /guardrails/{guardrail_id}/info` | 待补语义验收 |
| `GET /v2/guardrails/list` | 待补语义验收 |
| `PATCH /guardrails/{guardrail_id}` | 待补语义验收 |
| `POST /guardrails` | 待补语义验收 |
| `POST /guardrails/register` | 明确不支持 |
| `POST /guardrails/submissions/{guardrail_id}/approve` | 明确不支持 |
| `POST /guardrails/submissions/{guardrail_id}/reject` | 明确不支持 |
| `POST /guardrails/test_custom_code` | 待补语义验收 |
| `POST /guardrails/validate_blocked_words_file` | 明确不支持 |
| `PUT /guardrails/{guardrail_id}` | 待补语义验收 |

### 旧RAG/OCR/Google操作别名（6条，integration）

无推理执行器却写KV；拒绝修复待合入，原生推理不受影响。

相关待合入PR：#218。

| 精确入口 | main状态 |
| --- | --- |
| `POST /models/{model_name}:countTokens` | 待修复（代码与观察证据） |
| `POST /models/{model_name}:generateContent` | 待修复（代码与观察证据） |
| `POST /models/{model_name}:streamGenerateContent` | 待修复（代码与观察证据） |
| `POST /ocr` | 待修复（代码与观察证据） |
| `POST /rag/ingest` | 待修复（代码与观察证据） |
| `POST /rag/query` | 待修复（代码与观察证据） |

### 旧医学文本操作（2条，integration）

操作名被当作公共配置ID，修复待合入。

相关待合入PR：#222。

| 精确入口 | main状态 |
| --- | --- |
| `POST /comprehendmedical` | 待修复（代码与观察证据） |
| `POST /comprehendmedical/{operation}` | 待修复（代码与观察证据） |

### 标签、客户和终端用户配置（16条，integration）

标签/客户配置修复待合入；终端用户封禁是否消费不能由KV记录证明。

相关待合入PR：#188、#190。

| 精确入口 | main状态 |
| --- | --- |
| `GET /customer/info` | 待补语义验收 |
| `GET /end_user/info` | 待补语义验收 |
| `POST /customer/block` | 待补语义验收 |
| `POST /customer/delete` | 待补语义验收 |
| `POST /customer/new` | 待补语义验收 |
| `POST /customer/unblock` | 待补语义验收 |
| `POST /customer/update` | 待补语义验收 |
| `POST /end_user/block` | 待补语义验收 |
| `POST /end_user/delete` | 待补语义验收 |
| `POST /end_user/new` | 待补语义验收 |
| `POST /end_user/unblock` | 待补语义验收 |
| `POST /end_user/update` | 待补语义验收 |
| `POST /tag/delete` | 待补语义验收 |
| `POST /tag/info` | 待补语义验收 |
| `POST /tag/new` | 待补语义验收 |
| `POST /tag/update` | 待补语义验收 |

### 模型信息、fallback、封禁和公开旧别名（14条，C4）

与专用模型模块不同；不应由同名前缀推断真实路由/公开权限已修改。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /fallback/{model}` | 待补语义验收 |
| `GET /fallback/login` | 待补语义验收 |
| `GET /fallback/{model}` | 待补语义验收 |
| `GET /model/deprecations` | 待补语义验收 |
| `GET /model/info` | 待补语义验收 |
| `GET /model/settings` | 待补语义验收 |
| `GET /models/{model_id}` | 待补语义验收 |
| `GET /v1/model/deprecations` | 待补语义验收 |
| `GET /v1/model/info` | 待补语义验收 |
| `POST /fallback` | 待补语义验收 |
| `POST /make_public` | 待补语义验收 |
| `POST /model/block` | 待补语义验收 |
| `POST /model/unblock` | 待补语义验收 |
| `POST /model_group/make_public` | 待补语义验收 |

### 运维旧操作（15条，integration）

占位运维接口的明确拒绝及readiness修复待合入。

相关待合入PR：#194。

| 精确入口 | main状态 |
| --- | --- |
| `GET /coordination_redis/settings` | 待修复（代码与观察证据） |
| `GET /debug/asyncio-tasks` | 待修复（代码与观察证据） |
| `GET /debug/memory/details` | 待修复（代码与观察证据） |
| `GET /debug/memory/summary` | 待修复（代码与观察证据） |
| `GET /health/backlog` | 待修复（代码与观察证据） |
| `GET /health/drain` | 待修复（代码与观察证据） |
| `GET /health/history` | 待修复（代码与观察证据） |
| `GET /health/license` | 待修复（代码与观察证据） |
| `GET /health/shared-status` | 待修复（代码与观察证据） |
| `GET /memory-usage` | 待修复（代码与观察证据） |
| `GET /memory-usage-in-mem-cache` | 待修复（代码与观察证据） |
| `GET /memory-usage-in-mem-cache-items` | 待修复（代码与观察证据） |
| `POST /coordination_redis/settings` | 待修复（代码与观察证据） |
| `POST /coordination_redis/settings/test` | 待修复（代码与观察证据） |
| `POST /debug/memory/gc/configure` | 待修复（代码与观察证据） |

### 插件注册、启停、筛选、marketplace（10条，integration）

分别提交的注册、筛选、启停边界需组合回归，不能将启用标志视为执行器。

相关待合入PR：#206、#208、#210。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /claude-code/plugins/{plugin_name}` | 待补语义验收 |
| `GET /api/plugins` | 待补语义验收 |
| `GET /api/plugins/auth-token` | 待补语义验收 |
| `GET /claude-code/marketplace.json` | 待补语义验收 |
| `GET /claude-code/plugins` | 待补语义验收 |
| `GET /claude-code/plugins/{plugin_name}` | 待补语义验收 |
| `POST /claude-code/plugins` | 待补语义验收 |
| `POST /claude-code/plugins/{plugin_name}/disable` | 待补语义验收 |
| `POST /claude-code/plugins/{plugin_name}/enable` | 待补语义验收 |
| `PUT /claude-code/plugins/{plugin_name}` | 待补语义验收 |

### 健康路径OPTIONS（3条，retain）

观测204仅证明预检，不证明数据库就绪或健康检查成功。

| 精确入口 | main状态 |
| --- | --- |
| `OPTIONS /health/liveliness` | 待补语义验收 |
| `OPTIONS /health/liveness` | 待补语义验收 |
| `OPTIONS /health/readiness` | 待补语义验收 |

### 文件及WebSocket兼容入口（7条，C6）

非openai文件入口落到管理KV；passthrough只计算URL，realtime只写握手。协议执行不得伪报成功；不能改现有原生数据面。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /{provider}/v1/files/{file_id}` | 待修复（代码与观察证据） |
| `GET /{provider}/v1/files` | 待修复（代码与观察证据） |
| `GET /{provider}/v1/files/{file_id}` | 待修复（代码与观察证据） |
| `GET /{provider}/v1/files/{file_id}/content` | 待修复（代码与观察证据） |
| `POST /{provider}/v1/files` | 待修复（代码与观察证据） |
| `WEBSOCKET /openai_passthrough/{endpoint}` | 待修复（代码与观察证据） |
| `WEBSOCKET /vertex_ai/live` | 待修复（代码与观察证据） |

### 供应商批处理别名（4条，integration）

非openai供应商落到管理KV；修复待合入，须保留openai数据面分支。

相关待合入PR：#220。

| 精确入口 | main状态 |
| --- | --- |
| `GET /{provider}/v1/batches` | 待修复（代码与观察证据） |
| `GET /{provider}/v1/batches/{batch_id}` | 待修复（代码与观察证据） |
| `POST /{provider}/v1/batches` | 待修复（代码与观察证据） |
| `POST /{provider}/v1/batches/{batch_id}/cancel` | 待修复（代码与观察证据） |

### 供应商、搜索、提示词和策略基本配置（22条，retain）

仅已有真实回归覆盖的配置CRUD；不推出同前缀的执行、版本或统计能力已支持。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /credentials/{credential_name}` | 已验证（限定配置CRUD） |
| `DELETE /policies/{policy_id}` | 已验证（限定配置CRUD） |
| `DELETE /prompts/{prompt_id}` | 已验证（限定配置CRUD） |
| `DELETE /search_tools/{search_tool_id}` | 已验证（限定配置CRUD） |
| `GET /credentials` | 已验证（限定配置CRUD） |
| `GET /credentials/by_name/{credential_name}` | 已验证（限定配置CRUD） |
| `GET /policies/list` | 已验证（限定配置CRUD） |
| `GET /policies/{policy_id}` | 已验证（限定配置CRUD） |
| `GET /prompts/list` | 已验证（限定配置CRUD） |
| `GET /prompts/{prompt_id}` | 已验证（限定配置CRUD） |
| `GET /prompts/{prompt_id}/info` | 已验证（限定配置CRUD） |
| `GET /search_tools/list` | 已验证（限定配置CRUD） |
| `GET /search_tools/{search_tool_id}` | 已验证（限定配置CRUD） |
| `PATCH /credentials/{credential_name}` | 已验证（限定配置CRUD） |
| `PATCH /prompts/{prompt_id}` | 已验证（限定配置CRUD） |
| `POST /credentials` | 已验证（限定配置CRUD） |
| `POST /policies` | 已验证（限定配置CRUD） |
| `POST /prompts` | 已验证（限定配置CRUD） |
| `POST /search_tools` | 已验证（限定配置CRUD） |
| `PUT /policies/{policy_id}` | 已验证（限定配置CRUD） |
| `PUT /prompts/{prompt_id}` | 已验证（限定配置CRUD） |
| `PUT /search_tools/{search_tool_id}` | 已验证（限定配置CRUD） |

### 调度、预热、事件、Vantage集成（12条，C3）

注册存在但只做KV读写，未接相应调度、热加载、事件或外部导出执行器。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /schedule/anthropic_beta_headers_reload` | 待修复（代码与观察证据） |
| `DELETE /vantage/delete` | 待修复（代码与观察证据） |
| `GET /schedule/anthropic_beta_headers_reload/status` | 待修复（代码与观察证据） |
| `GET /vantage/settings` | 待修复（代码与观察证据） |
| `POST /api/event_logging/batch` | 待修复（代码与观察证据） |
| `POST /lazy/warm/{name}` | 待修复（代码与观察证据） |
| `POST /reload/anthropic_beta_headers` | 待修复（代码与观察证据） |
| `POST /schedule/anthropic_beta_headers_reload` | 待修复（代码与观察证据） |
| `POST /vantage/dry-run` | 待修复（代码与观察证据） |
| `POST /vantage/export` | 待修复（代码与观察证据） |
| `POST /vantage/init` | 待修复（代码与观察证据） |
| `PUT /vantage/settings` | 待修复（代码与观察证据） |

### server资源、OAuth、个人配置及导入审批（25条，integration）

子操作拒绝边界待合入，基本server配置不等于MCP运行时。

相关待合入PR：#198、#200、#204。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /server/{server_id}` | 待补语义验收 |
| `DELETE /server/{server_id}/oauth-user-credential` | 待补语义验收 |
| `DELETE /server/{server_id}/user-credential` | 待补语义验收 |
| `DELETE /server/{server_id}/user-env-vars` | 待补语义验收 |
| `GET /server` | 待补语义验收 |
| `GET /server/health` | 待补语义验收 |
| `GET /server/oauth/{server_id}/authorize` | 待补语义验收 |
| `GET /server/submissions` | 待补语义验收 |
| `GET /server/{server_id}` | 待补语义验收 |
| `GET /server/{server_id}/oauth-user-credential/status` | 待补语义验收 |
| `GET /server/{server_id}/user-env-vars` | 待补语义验收 |
| `GET /user-credentials` | 待补语义验收 |
| `GET /user-env-vars/status` | 待补语义验收 |
| `POST /server` | 待补语义验收 |
| `POST /server/import` | 待补语义验收 |
| `POST /server/oauth/session` | 待补语义验收 |
| `POST /server/oauth/{server_id}/register` | 待补语义验收 |
| `POST /server/oauth/{server_id}/token` | 待补语义验收 |
| `POST /server/register` | 待补语义验收 |
| `POST /server/{server_id}/oauth-user-credential` | 待补语义验收 |
| `POST /server/{server_id}/user-credential` | 待补语义验收 |
| `POST /server/{server_id}/user-env-vars` | 待补语义验收 |
| `PUT /server` | 待补语义验收 |
| `PUT /server/{server_id}/approve` | 待补语义验收 |
| `PUT /server/{server_id}/reject` | 待补语义验收 |

### 已拆分服务下的辅助操作（28条，C1）

策略执行/解析/附件/版本/状态、凭据迁移/按模型查询、提示词版本仍经parseMgmt映射CRUD；需语义级修复，不能沿用配置已验证结论。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /policies/attachments/{attachment_id}` | 待修复（代码与观察证据） |
| `DELETE /policies/name/{policy_name}/all-versions` | 待修复（代码与观察证据） |
| `GET /credentials/by_model/{model_id}` | 待修复（代码与观察证据） |
| `GET /credentials/migrate-encryption/check` | 待修复（代码与观察证据） |
| `GET /policies/attachments/list` | 待修复（代码与观察证据） |
| `GET /policies/attachments/{attachment_id}` | 待修复（代码与观察证据） |
| `GET /policies/compare` | 待修复（代码与观察证据） |
| `GET /policies/name/{policy_name}/versions` | 待修复（代码与观察证据） |
| `GET /policies/usage/overview` | 待修复（代码与观察证据） |
| `GET /policies/{policy_id}/resolved-guardrails` | 待修复（代码与观察证据） |
| `GET /policy/info/{policy_name}` | 待补语义验收 |
| `GET /policy/list` | 待补语义验收 |
| `GET /policy/templates` | 待修复（代码与观察证据） |
| `GET /prompts/{prompt_id}/versions` | 待修复（代码与观察证据） |
| `POST /credentials/migrate-encryption` | 待修复（代码与观察证据） |
| `POST /policies/attachments` | 待修复（代码与观察证据） |
| `POST /policies/attachments/estimate-impact` | 待修复（代码与观察证据） |
| `POST /policies/name/{policy_name}/versions` | 待修复（代码与观察证据） |
| `POST /policies/resolve` | 待修复（代码与观察证据） |
| `POST /policies/test-pipeline` | 待修复（代码与观察证据） |
| `POST /policy/templates/enrich` | 待修复（代码与观察证据） |
| `POST /policy/templates/enrich/stream` | 待修复（代码与观察证据） |
| `POST /policy/templates/suggest` | 待修复（代码与观察证据） |
| `POST /policy/templates/test` | 待修复（代码与观察证据） |
| `POST /policy/test` | 待修复（代码与观察证据） |
| `POST /policy/validate` | 待修复（代码与观察证据） |
| `POST /utils/test_policies_and_guardrails` | 待修复（代码与观察证据） |
| `PUT /policies/{policy_id}/status` | 待修复（代码与观察证据） |

### 旧设置、品牌、配置导出（15条，C3）

get/update使用不同命名空间，设置列表还会返回硬编码空值；需核对消费者及读写一致性。

| 精确入口 | main状态 |
| --- | --- |
| `GET /config/field/info` | 待补语义验收 |
| `GET /config/yaml` | 待补语义验收 |
| `GET /get/default_team_settings` | 待补语义验收 |
| `GET /get/internal_user_settings` | 待补语义验收 |
| `GET /get/ui_settings` | 待补语义验收 |
| `GET /get/user_banner` | 待补语义验收 |
| `GET /get_favicon` | 待补语义验收 |
| `GET /get_image` | 待补语义验收 |
| `GET /get_logo_url` | 待补语义验收 |
| `GET /settings` | 待补语义验收 |
| `GET /sso/get/ui_settings` | 待补语义验收 |
| `PATCH /update/default_team_settings` | 待补语义验收 |
| `PATCH /update/internal_user_settings` | 待补语义验收 |
| `PATCH /update/ui_settings` | 待补语义验收 |
| `PATCH /update/user_banner` | 待补语义验收 |

### Skill资源与归档（5条，integration）

资源CRUD及归档明确拒绝修复待合入；SkillHub另有模块补充项。

相关待合入PR：#212、#214。

| 精确入口 | main状态 |
| --- | --- |
| `DELETE /v1/skills/{skill_id}` | 待补语义验收 |
| `GET /v1/skills` | 待补语义验收 |
| `GET /v1/skills/{skill_id}` | 待补语义验收 |
| `GET /v1/skills/{skill_id}/archive` | 待补语义验收 |
| `POST /v1/skills` | 待补语义验收 |

## 检查与证据

`TestPhase4ClosureInventory` 对照实际注册要求全部303条有归属、状态、证据、批次与观察；变异测试拒绝缺项、重复、错误归属、未知状态、缺观察和无真实回归证据的“已验证”。这是清单完整性门禁，不能替代业务测试，也不降低70%门禁。

观察工具带 `phase4audit` build tag，默认CI与覆盖率统计不运行它；它只收集实验，不作为功能成功测试。复跑：

```bash
GOFLAGS=-tags=phase4audit XHUB_PHASE4_AUDIT_OUTPUT="$PWD/.verification/phase4-closure/http-observations.json" bash scripts/regression.sh '^TestPhase4AuditObservation$'
go test -race ./internal/gateway -run '^Test(Phase4Closure|RouteOwnershipAudit|RouteAudit)' -count=1
```

本次报告及实际执行结果见PR验证记录。原审计37项真实后台回归和8项浏览器流程的历史结果见 `compatibility-layer-audit.md`；未重新运行的历史结果不计入本轮。无线上、真实SSO或付费模型调用，隔离数据库清理失败不能通过。
