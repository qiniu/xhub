# 测试命令与环境

## 按修改选验证

| 修改 | 验证层 |
| --- | --- |
| 解析 / 费率 / 转换 | 包单元测试 |
| HTTP / 鉴权 / 持久化 | 后台 regression |
| 页面到数据面 | 真实浏览器 E2E |
| 文档 | 双语、链接、代码高亮、图片加载 |

~~~bash
go test ./internal/provider/... ./internal/router ./internal/catalog -count=1
XHUB_REGRESSION_STRICT=1 go test ./cmd/regression -count=1
make e2e-offline
~~~

PostgreSQL 默认 127.0.0.1:5433，也可设置 XHUB_TEST_DATABASE_URL。地址无效、连接、创建或清理失败均使测试失败，不再依赖 XHUB_REGRESSION_STRICT；数据库测试前必须先启动 PostgreSQL。regression 每用例创建独立 schema 并清理；Redis 专项必须使用独立实例。

## 前端 / 网站

~~~bash
cd frontend
npm run test:unit -- src/components/route_templates/templateForm.test.ts
npm run test:component -- src/components/route_templates/RouteTemplateSelect.test.tsx
npm run test:types
~~~

~~~bash
cd website
npm test
npm run build
npm run audit:links
npm run test:e2e
~~~

## 不要误运行

| 命令 | 副作用 |
| --- | --- |
| make regression | 本地协议上游，免费；需要数据库 |
| make e2e-offline / e2e-model-endpoints | 本地上游，免费 |
| make regression-live / e2e / e2e-all | 真实供应商，会收费 |
| make testdata | 清空重建共享 xhub/public 和 Redis DB 1，不调用模型 |

[后台回归](regression.md) · [浏览器回归](e2e-regression.md) · [付费验证](regression-live.md)

报告必须列命令、pass/fail/skip、清理与依赖边界。前端生成类型缺少 Go OpenAPI 来源，不能用 routes.json 伪造响应 schema。

### 测试门禁证据

`scripts/regression.sh` 消费 `go test -json`，要求至少一个符合 `-run` 层级的叶用例实际通过。子用例零匹配、空跑、全跳过、损坏事件或包未完成均不得通过。可选外部供应商的跳过单独列出，不算通过。直接运行 `go test -run` 仍有 Go 自身的空匹配行为，验收使用脚本入口。

每轮输出 `.e2e/runs/regression-*/regression.log`、`events.jsonl` 和 `summary.json`，原始 Go 失败码优先于后续报告错误。浏览器入口同样拒绝零执行/全跳过，并记录 `$E2E_RUN_DIR/cleanup-status.json`；测试或清理非零都会使入口失败。验收编排在 Redis/锁清理后才生成最终报告，不把 `.e2e/current` 的历史结果当成本轮证据。

门禁自测（隔离子进程、真实 Go 匹配及本地 PostgreSQL schema）：

```bash
go test -race ./cmd/regression/testsupport -count=1
python3 -m unittest scripts.test_regression_results scripts.test_test_gates scripts.test_regression_output scripts.test_e2e_summary scripts.test_e2e_model_endpoints -v
node --test frontend/e2e/checklist-reporter.test.cjs
```

SIGKILL 等强制终止可能使清理/报告钩子完全无法执行；没有完整报告不能算通过。这些门禁测试不验证付费供应商和线上可用性。
