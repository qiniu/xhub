# 第四阶段：兼容层归属审计

> 2026-10-28 更新：后续完整收尾队列以 [固定收尾清单](phase4-closure.md) 为准。下文是原注册审计与当时验证记录，不能作为最新剩余数量。

本次审计基于 `939e0d6c`（#180 已进入 main），**第四阶段尚未整体完成**。供应商、搜索工具、提示词、护栏和策略已收拢；访问组 #182、向量库配置 #184 仍是待合入 PR。测试通过、合入和部署是三个不同状态。

本轮只增加测试、快照及开发文档，不修改接口、权限、预算或存储，不降低 70% 覆盖率门禁，不合并或部署。

## 清单如何产生

`internal/gateway/route_audit_test.go` 调用实际模块的 `Mount`，包含循环与字符串拼接注册的 SCIM 路径，并交给生产的 `Server.handle`。模块先于目录注册、已移除路径过滤、Gin 参数别名归一和首个注册者优先均使用现有代码。审计再与正常 `mountModules` / `mountCatalog` 得到的 Gin 路由集合比较。

结果保存于 `internal/gateway/testdata/route-ownership.json`。入口新增、移除或归属变化会使普通 Go 测试失败；变异用例实际移除 identity 模块、增删目录、用模块接管目录路由，确认检查会拒绝变化。测试也通过内存 Gin HTTP 请求验证首个处理器和路径参数还原。

这份清单补充现有 `contracts/migration-inventory.json`，不替代 OpenAPI：

- 原清单记录源代码声明与契约迁移情况。
- 新快照记录默认内置模块的注册归属。它不执行所有业务处理器，不证明供应商、SSO、代理配置或全部接口可用。
- `catalog:<handler>:<class>` 中 class 来自现有目录分类，**不是处理器实际执行权限的证明**；例如协议处理器可以再转入不同路径。
- `module:<name>` 表示模块先接管了目录入口；`module_only` 补充目录之外的模块注册。
- 快照不能发现处理器内部逻辑改变，仍需领域单元测试、真实 HTTP/数据库回归和浏览器 E2E。未知接口不能因此加入 Swagger 的已支持集合。

## 当前数量与含义

| 范围 | 数量 | 含义 |
| --- | ---: | --- |
| 目录声明 | 755 | 包含别名和已移除条目，不是可用接口数 |
| 已移除目录声明 | 89 | 注册器不挂载 |
| 被专用模块优先接管的目录声明 | 138 | 如用户、Key、模型、日志 |
| 仍由目录处理器挂载的声明 | 528 | 含数据面、公开入口、退役入口及管理兼容入口 |
| 其中 `serveFamilyRoute:manage` | 296 | 需继续追踪内部委派，不能直接视为 296 个缺陷 |
| 目录之外的模块注册 | 95 | 包含动态拼接的目录同步等路径 |
| 最终 Gin 唯一路由 | 757 | 参数名不同的声明可能对应同一注册键，不能直接将上表相加 |

296 条管理兼容声明中，69 条属于供应商 8、搜索 6、提示词 8、策略 28、护栏 19 的现有委派范围。这里统计的是资源入口，不代表每条路径都支持操作：护栏等明确保留 501/404 分支。#182 的 11 条访问组和 #184 的 5 条向量库配置即使合入，也不会从目录快照消失，因为它们仍保留旧 HTTP 适配入口、在内部委派。

因此，不能用目录条数下降或某个大文件变短判断第四阶段完成。

## 已有边界与待合入项

| 资源 | 服务边界 / PR | 状态和验收限制 |
| --- | --- | --- |
| 供应商 | `suppliers` / #169、#171 | main；配置、遮蔽、轮换与真实本地上游调用 |
| 搜索工具 | `searchconfig` / #173 | main；存储、实际探测、上游失败与删除 |
| 提示词 | `promptconfig` / #175 | main；版本、持久化、实际探测与失败 |
| 护栏 | `guard` 管理服务 / #177、#178 | main；持久化、运行时拦截/脱敏/放行；不支持的远程能力仍拒绝 |
| 策略 | `policyconfig` / #180 | main；配置与运行时护栏边界保持，不将配置字段解释为新的授权规则 |
| 访问组 | `accessgroupconfig` / #182 | 待合入；配置不等于已接入运行时模型授权；单复数存储仍分开 |
| 向量库配置 | `vectorconfig` / #184 | 待合入；单数配置与复数数据面分开；配置 completed 不证明索引完成 |

#182、#184 的真实测试证据见对应 PR，本轮 main 验证不包含这两个尚未合入的服务。不能把分支分别通过解释为合并后的组合验收。

## 尚未完成的工作

下列顺序以避免改变现有规则为前提，每项先补正常/失败/持久化/运行时证据，再决定独立服务或明确不支持。表中的路径是排查入口，不是已支持声明。

| 顺序 | 仍需确认的范围 | 已发现的边界与下一步 |
| --- | --- | --- |
| 1 | 标签及客户配置，例如 `/tag/new`、`/tag/info`、`/customer/update` | 标签写入仍走通用 KV；`GET /tag/list` 在 usage 模块返回空目录。已有批量删除回归通过详情读取验证落库，不能证明列表或运行时消费。先整理读写消费者，避免只搬文件把不一致固定下来。 |
| 2 | 预算配置及 IAM 旧别名，例如 `/budget/new`、`PATCH /organization/member_update`、`POST /team/block` | 注册归属仍可能是通用目录，不等于已调用 IAM/预算服务。需要逐项核对原客户端和真实效果；本轮不更改权限、限额、封禁或预算结算。 |
| 3 | 设置、健康及“执行型”目录，例如 `/coordination_redis/settings/test`、`/health/drain`、`/debug/memory/gc/configure` | 路径名称不能证明实际探测或动作发生。先用本地依赖验证副作用和失败；没有执行器时不能仅因写 KV 返回 200 就视为实现。 |
| 4 | 保留的 server、插件、skills、OAuth 别名及其他兼容入口 | 审计各路径实际 handler、认证和调用方，区分明确 501/410、已移除与仍在通用存储的分支；不批量恢复功能，也不批量删入口。已验证的 OAuth/JSON-RPC 拒绝测试不覆盖所有近似命名路径。 |
| 5 | 可删除的重复/死分支及组合验收 | 只有确认无已挂载入口、无其他调用者且有兼容回归，才删除旧实现。#182/#184 合入后重新跑组合回归，更新快照和状态。 |

`/rag/*`、`/ocr`、`/models/{model_name}:generateContent` 等名字类似推理的入口仍出现在管理目录分组，需优先核对真实语义。这是注册证据暴露的风险，不是本轮已经复现的供应商执行故障；不能把这些入口直接宣布为第五阶段已覆盖。

真正的数据面生命周期整理（原生与跨协议转换、SSE、历史归属、重试、日志和结算）仍属第五阶段。管理入口的假成功若被证实，应单独修复并保留证据，不能借第五阶段延期掩盖。

## 本轮验证与复核方式

实际执行结果：路由审计及变异测试 9 个叶子用例通过；真实 PostgreSQL/HTTP 回归 37 个叶子用例通过、0 跳过；浏览器 8 个场景通过、0 失败，其他 419 个场景未运行。浏览器范围为供应商保存/刷新/非法更新/模型调用、OAuth 明确拒绝、护栏管理与数据面、策略配置隔离（中英文），以及英文提示词/搜索真实探测失败路径。E2E 清理 `test_exit=0, cleanup_exit=0`。

~~~bash
go test -race ./internal/gateway -run '^TestRoute(OwnershipAudit|Audit)' -count=1
bash scripts/regression.sh '^Test(SupplierServiceRuntimeRotation|SupplierCatalogLifecycle|SearchConfigServiceLifecycle|PromptConfigService.*|GuardManagementService.*|PolicyConfigService.*|CatalogIntegrity.*)$'
E2E_RUN_DIR="$PWD/.e2e/phase4-audit" bash scripts/e2e.sh \
  scenarios/regression/guard-management-service-en.test.ts \
  scenarios/regression/guard-management-service-zh-CN.test.ts \
  scenarios/regression/policy-config-service-en.test.ts \
  scenarios/regression/policy-config-service-zh-CN.test.ts \
  scenarios/regression/catalog-persistence-en.test.ts \
  scenarios/regression/catalog-persistence-zh-CN.test.ts \
  scenarios/regression/prompts-probe-en.test.ts \
  scenarios/regression/search_tools-probe-en.test.ts
~~~

仅在审核实际路由差异后，本地用 `XHUB_UPDATE_ROUTE_AUDIT=1 go test ./internal/gateway -run '^TestRouteOwnershipAudit$' -count=1` 更新快照，再取消变量运行只读检查；CI 默认不更新。普通 Go 质量作业会运行该测试，不需要新建服务。

本机报告：`.verification/phase4-audit/report.txt`、`.verification/phase4-audit/registration.jsonl`、`.e2e/phase4-audit/results.json`、`.e2e/phase4-audit/cleanup-status.json`。后台运行目录由 `scripts/regression.sh` 输出。本轮不访问生产账号、外部模型、真实企业 SSO 或线上反向代理；本地上游通过不证明这些边界可用。
