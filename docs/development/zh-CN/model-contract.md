# 模型与端点管理契约

Proposal [#159](https://github.com/qiniu/xhub/issues/159)。[OpenAPI 源](../../../contracts/models/openapi.json) 为本批结构源，生成 Go/TypeScript 类型及[离线参考](model-api.generated.md)。不替换生产 handler，不安装严格运行时校验，不改变权限、预算或协议转换。

## 已迁移操作

| 操作 | 现有语义 |
| --- | --- |
| GET `/v2/model/info` | 平台管理员部署列表；`modelId` 精确过滤、`search` 包含匹配；`page/size` 解析失败回退默认。未知 ID 返回空列表，不是404。 |
| GET `/model/groups` | 平台管理员按公开名分组，搜索命中任一部署返回完整组；size最多200；包括默认权重和回退配置。存储不可用503，坏配置500。 |
| POST `/model/new` | 创建数据库部署，返回裸部署对象。多个部署可共享公开名。须有保存的连接或api_base；人工定价新建至少一个正费率。 |
| POST `/model/update`、PATCH `/model/{model_id}/update` | 合并补丁，路径ID优先；POST从model_info.id或id读取。省略保留，密钥遮罩`*****`不覆盖原值。YAML部署不可编辑。 |
| POST `/model/disable`、`/model/enable` | 数据库部署启停，返回部署；普通推理停用后不能继续出站。不把成功响应定义为只有一个布尔字段。 |
| POST `/model/delete` | 返回原部署加id与deleted=true；仍有回退引用时409；YAML不能删除。 |
| POST `/health/test_connection` | 根据model_info.id选择已保存部署和凭据，忽略客户端地址/密钥覆盖。输入400、未知部署404；上游失败也可能HTTP200，正文status=error、healthy=false。 |

`model_name` 是公开别名，`model_info.id` 是部署ID，`litellm_params.model` 是上游模型。`custom_llm_provider` 为凭据兼容协议，`transport` 为执行方式，`endpoint_types` 为入口能力，不能互相替代。连接探测只支持已实现的对话模式，不等于每个声明端点或每个模型能力都已通过验证。

前端本批只将 `modelPatchUpdateCall` 和 `modelDeleteCall` 的响应绑定到生成类型；请求与异常行为保持原样。创建、列表、探测和其他旧客户端未整体迁移，不能把本批说成全部前端已使用生成客户端。

## 兼容和未覆盖边界

- 鉴权由现有服务执行。契约测试验证匿名401、推理Key/非平台会话403、管理员操作；不通过扩大权限让测试成功。
- 部署参数和模型信息允许供应商扩展。Go 对这些开放映射、补丁和探测联合体保留 `json.RawMessage`，避免未知字段、显式null或精确价格被重新编码丢失；结构合法性仍由独立验证器检查，编译成功不是验收。
- OpenAPI 描述受支持的输入形状，不能保证旧 `readMap` 会拒绝所有多余字段或畸形JSON。ID别名、价格/协议/凭据组合等跨字段规则由真实HTTP回归验证，不能用结构测试替代。
- `/v2/model/info` 的 teamId、model、sortBy、sortOrder、access_group 等旧前端参数在现有handler未消费；本契约不将其列为有效过滤功能。页面客户端将部分403转为空列表的旧行为仍未改动。
- 读取遮罩针对顶层敏感字符串，不承诺任意嵌套扩展自动脱敏。探测错误固定脱敏，不回传供应商秘密或正文。
- 探测绕过普通路由、额度和记账，可能实际请求供应商；不能用探测成功证明用户授权、预算或费用链正确。另有真实用户Key调用、停用拦截和账单回读测试。
- 扁平费率兼容有限非负数字字符串及null，不能把生成类型收窄成只接受JSON数字；字符串完整语义由服务检查。
- 创建的人工价格要求正费率，而历史更新可接受0；本批不统一这些既有语义，不重置消费。
- 未迁移：价格目录、内置模型发现/批量添加、全局权重和回退写接口、模型监控、`/model/available`与SDK列表、实际推理协议和全量旧schema。这些功能并非因此不可用，只是未纳入本批新契约。
- 未验证：真实付费供应商、生产并发、所有存储故障与全部管理角色组合。已测试创建/更新存储失败；未宣称每个路由的每种错误来源都被注入。

## 验证方式

```bash
npm --prefix frontend run check:api:models
node --test frontend/scripts/gen-model-contract.test.mjs frontend/scripts/gen-key-contract.test.mjs frontend/scripts/gen-user-contract.test.mjs frontend/scripts/gen-supplier-contract.test.mjs scripts/test_quality_workflow.cjs
go test -race ./contracts/...
XHUB_REGRESSION_COVERPROFILE=coverage/model-contract.out bash scripts/regression.sh '^Test(ModelContract|OfficialCredentialSupportsNativeModel)'
E2E_RUN_DIR="$PWD/.e2e/model-contract-final" bash scripts/e2e.sh scenarios/regression/model-contract-en.test.ts scenarios/regression/model-contract-zh-CN.test.ts
```

真实后台使用隔离PostgreSQL与受控HTTP上游，核对九个操作、存储、权限、协议、上游403及错误JSON、停用后无出站、恢复推理与费用。浏览器两种语言覆盖部署详情、连接测试成功/失败、启停保存与刷新、真实推理、日志费用与删除清理；供应商与部署通过API准备，不声称验证了全部创建表单字段。

`Model API contract` CI负责生成漂移、类型正反例、契约单测、真实回归与覆盖率采集校验上传；后台报告按backend合并，70%补丁门槛不变。浏览器用例由既有发布E2E入口执行。生成文档可离线阅读；首次下载固定生成器、CI上传与真实供应商可用性仍是外部边界。

回退仅撤回本批契约、类型标注、文档及测试/CI，无数据库迁移、生产配置或用户数据需要回滚。

## 本批验证记录

2026-10-25：契约5包/124个叶用例、生成器及工作流22项、前端单测9项、真实HTTP/PG回归4项、中英文E2E2项全部通过，零失败/跳过，浏览器测试和清理退出码均为0。全量TypeScript、go vet及漂移检查通过；ESLint原217错误低于289门槛。本批仅定向E2E，不代表419项全套完成。

本地证据：`.verification/model-contract/`，`.e2e/runs/regression-OxRI0749/`，`.e2e/model-contract-final/{results.json,html/index.html,cleanup-status.json}`，`coverage/model-contract.out`。CI按以上命令重新生成并上传可下载证据；不将本地结果称为生产供应商验收。
