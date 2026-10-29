# 第四阶段候选组合验收

## 验证对象与结论

本次组合基线 main `e8a29c4f972e8832e1c6e02a8226d0befcc65584`。集成分支 `codex/stage4-final-integration` 纳入五个候选，保留其提交历史，不合并 main、不部署。

| PR | 验证的候选提交 | 内容 |
| --- | --- | --- |
| #231 | 3db9211f41aedec845f3b2931fc9ae64190d3871 | 旧设置15条、真实主题、前端错误处理 |
| #233 | 42b42bbf06eadded271e0225b224264cfda4b8f6 | 模型旧别名14条、真实模型管理 |
| #235 | d9765274c04a2960fad55798ac532b0232efdf46 | 统计26条、真实日志/用量 |
| #237 | e505484ecf19954a79f97bb64630307281414b86 | 旧文件/WS7条、原生协议保留 |
| #239 | 23bdd2ae787672786bb845cd531da7378060ac59 | 发现9条、真实搜索供应商定义 |

#239 的最新提交全部测试/构建/契约/Codecov检查通过；publish/deploy按PR设计跳过。Fenno评论未发现可报告缺陷，评审对象为产品提交80e7578；后续23bdd2a仅更新证据链接。机器人未运行浏览器，本次自行实际运行。其余四个候选最新检查也无失败/待运行项。候选通过不代替组合PR通过。

## 组合处理

- 保留设置、模型、统计、发现、文件各自的精确分发；顺序均在原JSON-RPC拒绝之后、通用KV之前。
- 合并质量工作流的真实regression覆盖选择器与发布E2E列表；保留每批用例，加入组合用例，不提高超时、不跳过测试、不降低门禁。
- 新增 `cmd/regression/stage4_combined_acceptance_test.go`：同一HTTP/PG中先计费，再交错调用五批旧入口；核对原权限、会话、模型、主题、KV和账单不变；再验证真实停用/启用、零预算403及上游失败日志。
- 新增 `frontend/e2e/stage4-combined-scenario.ts` 及两个语言入口：浏览器供应商选择、真实计费对话、交错拒绝、模型页面、日志详情与刷新、真实用量页；清理自建模型和隔离数据库。
- `internal/gateway/phase4_closure_test.go` 固定五个待合入PR与71条声明；JSON移除已随#229合入的过时待办，保留历史观察。中英文清单将历史表折叠，不能再把历史状态当作当前结果。
- 组合分支对权限、预算算法及 `codecov.yml` 无修改。70%是既有补丁覆盖率目标，不是宣称每种报告总覆盖率都超过70%。

## 实际验证

| 验证 | 结果 | 本地报告 |
| --- | --- | --- |
| 严格数据库模式Go race/覆盖 | 4244叶子通过，4个既有外部/专用夹具跳过；零测试或构建失败（4721通过事件包含父子测试） | `unit-race-final.json` / `unit-final.out` |
| 完整后台HTTP/PG regression | 1254叶子通过，32既有条件跳过，零失败 | `backend-full.log` |
| 五批及组合集中回归/覆盖采集 | 112叶子通过，零失败/跳过 | `backend-boundaries.log` / `backend-boundaries.out` |
| 目标浏览器E2E | 23通过，零失败/跳过/重试；test_exit=0、cleanup_exit=0 | `.e2e/stage4-final-integration/` |
| 相关前端单元/组件 | 165通过，零失败/跳过 | `frontend.json` |
| 工作流/场景登记 | 108通过，零失败/跳过 | `workflow-final.log` |
| 收尾清单门禁 | 11通过事件（含父子测试），零失败 | `closure-final.json` |
| API兼容性 | 7单元通过；5份OpenAPI对main无破坏性变更 | `compatibility-unit.log` / `compatibility.log` |
| ESLint既有预算 | 221错误 ≤ 原预算289，未放宽 | `eslint-budget-final.log` |
| 文档链接 | 3通过 | `document-links-final.log` |
| Go vet / diff空白检查 | 通过 | `vet.log` |

完整后台的32个条件跳过包括外部供应商live、未配置的真实Agent CLI、本地Next代理专用开关及未配置专属Redis的冷却场景，不能统称为本次已验证；本批112个集中回归没有跳过。Go单元的4个跳过为两种付费多模态live、Kodo归档live和企业浏览器专用夹具。无新增skip。

命令从仓库根目录运行（前端命令注明工作目录）：

```bash
# CI一致的严格数据库模式和race覆盖，排除独立运行的后台regression及npm第三方Go源码
export XHUB_REGRESSION_STRICT=1
export XHUB_TEST_DATABASE_URL='<isolated-local-PostgreSQL-DSN>'
go list ./... | grep -vE '/cmd/regression$|/frontend/node_modules/' > /tmp/xhub-unit-packages.txt
xargs go test -json -race -timeout=25m -covermode=atomic -coverprofile=.verification/stage4-final-integration/unit-final.out < /tmp/xhub-unit-packages.txt

go vet ./...
go test -json ./internal/gateway -run '^TestPhase4Closure'
XHUB_REGRESSION_COVERPROFILE=.verification/stage4-final-integration/backend-boundaries.out bash scripts/regression.sh '^Test(SettingsOperationBoundary|ModelAliasBoundary|AnalyticsAliasBoundary|ProviderFileBoundary|LegacyWebsocketBoundary|DiscoveryBoundary|SearchProviderDiscovery|SearchProbe|Stage4CombinedAcceptance)'
bash scripts/regression.sh
node --test scripts/test_quality_workflow.cjs scripts/test_release_workflow.cjs frontend/e2e/checklist-reporter.test.cjs frontend/e2e/scenario-coverage.test.cjs frontend/e2e/optional-settings-response.test.cjs
PATH="$HOME/go/bin:$PATH" node --test frontend/scripts/check-contract-compatibility.test.mjs
PATH="$HOME/go/bin:$PATH" node frontend/scripts/check-contract-compatibility.mjs origin/main

# frontend目录
npx vitest run src/components/Teams.test.tsx src/components/team/TeamInfo.test.tsx src/components/networking.test.ts src/lib/http/api.test.ts 'src/app/(dashboard)/old-usage/page.test.tsx'
npx eslint . --format json --output-file ../.verification/stage4-final-integration/eslint.json
node scripts/check-eslint-error-budget.mjs ../.verification/stage4-final-integration/eslint.json 289
```

```bash
E2E_PORTABLE_CONSOLE=1 E2E_RUN_DIR="$PWD/.e2e/stage4-final-integration" bash scripts/e2e.sh scenarios/regression/stage4-combined-en.test.ts scenarios/regression/stage4-combined-zh-CN.test.ts scenarios/regression/settings-operation-en.test.ts scenarios/regression/settings-operation-zh-CN.test.ts scenarios/regression/model-alias-en.test.ts scenarios/regression/model-alias-zh-CN.test.ts scenarios/regression/analytics-boundary-en.test.ts scenarios/regression/analytics-boundary-zh-CN.test.ts scenarios/regression/protocol-boundary-en.test.ts scenarios/regression/protocol-boundary-zh-CN.test.ts scenarios/regression/discovery-boundary-en.test.ts scenarios/regression/discovery-boundary-zh-CN.test.ts scenarios/columns/search-tools-connection.test.ts scenarios/regression/search_tools-probe-en.test.ts scenarios/pages/logs.test.ts scenarios/pages/audit-logs.test.ts scenarios/keys/rotation-secret.test.ts scenarios/regression/swagger-reference-en.test.ts scenarios/regression/swagger-reference-zh-CN.test.ts agent-native-protocol.test.ts
```

本地准备阶段有三项失败，均保留报告而未算通过：首次直接运行 `go test ./...` 时，工作区原有的未跟踪历史调查文件 `output/log-filter-investigation/log_filter_probe_test.go` 被Go当成独立包编译失败。它不属于Git提交；已完整移至 `.verification/stage4-final-integration/historical-log-filter-investigation/` 保存；后续裸 `go test ./...` 把长时后台套件一起启动，触发Go默认10分钟总超时，独立 `bash scripts/regression.sh` 已按仓库既有运行器完整通过，未改超时配置。严格race/覆盖命令首次又因本机Go1.25工具链缺少 `covdata`，三个无测试包采集构建失败；使用同版本GOROOT源码编译补齐本地工具后重跑。缺失工具修复仅作用于本机，不提交工具二进制或修改仓库工具链版本。未删除或跳过任何仓库测试。ESLint原有问题依既有预算核验，不将原始eslint非零退出冒充零错误。

## 报告与边界

- 本地完整记录：`.verification/stage4-final-integration/`，含unit-race-final.json、两个后台日志、覆盖profile、前端JSON、ESLint预算、兼容性与工作流报告。
- 浏览器：`.e2e/stage4-final-integration/results.json`、Playwright报告和 `cleanup-status.json`；23条目标测试不等于全部483个登记场景已运行。清单19/483仅是登记项命中数，另外包含原生协议文件的多条测试，不混作失败数。
- 后台完整报告：.e2e/runs/regression-KM5nrlMz/；目标112叶子用例报告：`.e2e/runs/regression-xQc9URyU/`。
- 本地上游为真实HTTP协议夹具，不使用浏览器路由mock；能验证请求、鉴权、持久化、错误、计费与清理，不能证明外部付费模型/搜索商在线能力、企业SSO网络/证书或生产流量。
- live供应商用例缺凭据时按其既有定义跳过，单列跳过数，不计入通过。真实数据库连接、零匹配或清理失败仍会使门禁失败。
- 组合PR仍须GitHub CI与评审通过。当前不合并、不部署、不关闭五个源PR；以后若合入组合PR，应以其包含的源提交核对源PR状态，不能重复套用补丁。
