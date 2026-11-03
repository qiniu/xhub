# 服务辅助接口能力边界（第四阶段 C1）

关联：[#223](https://github.com/qiniu/xhub/issues/223)，[固定收尾审计 #186](https://github.com/qiniu/xhub/pull/186)。本批修复26条辅助入口，没有实现新的策略执行器、版本仓库、附件关系或凭据迁移。

## 用户可观察的变化

原来例如 `POST /policies/test-pipeline` 会把请求当成ID为 `test-pipeline` 的配置保存并返回200；它没有执行流水线。现在这些精确入口在原管理员鉴权、JSON和JSON-RPC校验之后返回501、`error.type=unsupported_operation`、`Cache-Control: no-store`，不读取/修改公共KV、不调用上游、不回显输入。

原有匿名401、普通用户403、损坏JSON400保持；不会将不支持操作变成权限错误。正常 `/policies`、`/prompts`、`/credentials` 配置CRUD、`/policy/list`及`/policy/info/{policy_name}`保留；资源仍可叫 `status`、`versions` 或 `migrate-encryption`。`/prompts/test`、本地护栏及模型调用保留，不改权限、预算算法、数据库结构和70%门禁。

策略页当前仅挂载基本配置表单。旧组件中的模拟器不是当前页面入口，本批不恢复它，也不以直接API检查冒充页面模拟器测试。已有客户端依赖这些假成功接口时，将明确收到501；这是有意的错误语义修复。没有执行器的能力不加入Swagger已支持集合。

## 精确范围

- `DELETE /policies/attachments/{attachment_id}`
- `DELETE /policies/name/{policy_name}/all-versions`
- `GET /credentials/by_model/{model_id}`
- `GET /credentials/migrate-encryption/check`
- `GET /policies/attachments/list`
- `GET /policies/attachments/{attachment_id}`
- `GET /policies/compare`
- `GET /policies/name/{policy_name}/versions`
- `GET /policies/usage/overview`
- `GET /policies/{policy_id}/resolved-guardrails`
- `GET /policy/templates`
- `GET /prompts/{prompt_id}/versions`
- `POST /credentials/migrate-encryption`
- `POST /policies/attachments`
- `POST /policies/attachments/estimate-impact`
- `POST /policies/name/{policy_name}/versions`
- `POST /policies/resolve`
- `POST /policies/test-pipeline`
- `POST /policy/templates/enrich`
- `POST /policy/templates/enrich/stream`
- `POST /policy/templates/suggest`
- `POST /policy/templates/test`
- `POST /policy/test`
- `POST /policy/validate`
- `POST /utils/test_policies_and_guardrails`
- `PUT /policies/{policy_id}/status`

## 验收边界

- 修复前真实HTTP/PostgreSQL测试26条全部失败；修复后预置操作名碰撞记录，逐条确认错误和全部KV不变。
- 单元覆盖方法、模板、尾斜杠、空段、额外路径、普通资源名；后台覆盖原鉴权/JSON错误、正常配置读改删、真实对话和预算拒绝。
- 中英文浏览器创建策略并刷新，核对26条API失败、配置保留和零上游调用，再在调试台完成实际本地模型对话；独立夹具删除策略，运行器删除schema。
- 观察范围使用本地供应商，不证明生产模型、企业SSO或线上代理状态；未合并、未部署。执行命令和报告见PR正文。
