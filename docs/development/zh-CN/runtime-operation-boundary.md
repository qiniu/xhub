# 无执行器的旧运维接口（第四阶段 C3/runtime-ops）

固定收尾清单中这12条入口只有目录KV读写，没有调度器、预热、事件批处理或Vantage导出执行器。之前 `POST /vantage/export` 可以保存配置并返回 `status: ok`、`exported_count: 0`，这不代表完成导出。现在明确失败，不再将配置对象冒充执行结果。

## 本次精确范围

| 方法 | 路径 |
| --- | --- |
| `DELETE` | `/schedule/anthropic_beta_headers_reload` |
| `DELETE` | `/vantage/delete` |
| `GET` | `/schedule/anthropic_beta_headers_reload/status` |
| `GET` | `/vantage/settings` |
| `POST` | `/api/event_logging/batch` |
| `POST` | `/lazy/warm/{name}` |
| `POST` | `/reload/anthropic_beta_headers` |
| `POST` | `/schedule/anthropic_beta_headers_reload` |
| `POST` | `/vantage/dry-run` |
| `POST` | `/vantage/export` |
| `POST` | `/vantage/init` |
| `PUT` | `/vantage/settings` |

按方法和完整路径匹配，兼容单个尾斜杠。`{name}` 必须是一个非空路径段，不吞掉额外子路径；不按 `/schedule`、`/lazy` 或 `/vantage` 整个前缀拒绝。

## 行为与兼容性

- 原鉴权保持：匿名401、普通用户403；管理员通过鉴权及JSON校验后收到501，`error.type=unsupported_operation`，`Cache-Control: no-store`。
- 损坏JSON和顶层JSON-RPC仍先返回400；不存在的相似入口不因本次修改变成已支持。
- 不读取或修改同名KV，不回显输入中的API Key、存量私有配置，不调用模型或外部导出服务。旧配置不删除、不迁移。
- 调用方依赖旧200的逻辑需要处理明确错误；未引入可替代的运维执行器，不会通过硬编码 `success:false` 搭配200掩盖失败。
- 现有真实配置服务、供应商凭据轮换、模型请求、权限和预算算法不改；真实配置可读写不等于本批调度/导出已支持。

## 已运行的验收路径

```sh
go test -race ./internal/gateway/family ./internal/gateway -count=1
XHUB_REGRESSION_COVERPROFILE="$PWD/.verification/phase4-c3/runtime-regression.out"   bash scripts/regression.sh '^Test(RuntimeOperationBoundary|PolicyConfigService|SupplierServiceRuntimeRotation|TeamlessSessionBudgetHTTP)'
E2E_PORTABLE_CONSOLE=1 bash scripts/e2e.sh scenarios/regression/runtime-operation-en.test.ts scenarios/regression/runtime-operation-zh-CN.test.ts
```

真实HTTP/PostgreSQL回归先验证12条旧入口全部失败，再验证修复后各类身份、损坏JSON/RPC、全KV快照不变、零上游请求。关联回归覆盖配置持久化及存储失败、供应商凭据真实轮换和无团队用户预算。数据库使用私有schema，退出清理失败不能算通过。

双语E2E：管理员页面创建策略配置并刷新读回 → 12条旧运维接口明确501且配置/上游计数不变 → 刷新页面确认原配置 → 调试台选择模型与Chat端点并发送英文提示词 → 实际上游返回e2e-ok → 独立清理夹具删除策略，运行器回收schema。当前策略表单原本使用中文标签，本次没有改动其文案；中英文调试台使用对应翻译定位。

模型上游为本地确定性HTTP夹具，未调用付费模型、真实Vantage或生产调度服务。未实现的集成只证明明确拒绝，不能宣称已验证外部服务可用。真实回归采集覆盖率并上传，双语场景加入发布门禁，70%不变。

## C3仍未完成的部分

另有固定清单 `settings` 的15条设置、品牌和配置导出入口待逐项验收：4组get/update、SSO UI设置别名、3条品牌读取、settings以及2条config读取。部分前端仍调用它们，不能像无调用方的运维操作一样统一拒绝。

已发现 `/get/...settings` 列表包装硬编码空values，`/update/...` 与读取命名空间不一致；必须核对实际消费者才能决定修复方式。不能仅让写入后读回成功就宣称默认团队预算、用户默认值或页面开关已生效。本批不修改这些15条入口，不提前改变main审计状态。
