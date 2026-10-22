# 个人 Key 共享预算验证

关联 Proposal：#116。验证日期：2026-10-22。未部署生产。

## 修改范围

- `internal/iam/quota.go`：Key 金额上限不预留资金，空预算共享个人余额；耗尽后允许管理 Key，但新推理仍被拒绝。Key 上限不能抵扣其他用户的预留。RPM/TPM 规则保持不变。
- `frontend/src/components/networking.tsx`：正确解包用户详情 `user_info`。
- `frontend/src/app/(dashboard)/usage/_components/components/UsagePageView.tsx`：保留 0 预算；加载中和失败不显示为无限额。
- `frontend/src/components/organisms/{createKeyPayload,create_key_button,PersonalBudgetSummary}`：空文本预算转为 null，显示个人总额、已用和剩余；保留后台错误分类，移除硬编码英文错误包装。
- 中英文额度说明、相关单元测试、后台 regression 和浏览器 E2E 同步更新。

## 实际执行

```bash
go test ./internal/iam -count=1 -json
go test ./internal/iam -run 'TestKeyCap' -count=1 -json
bash scripts/regression.sh 'Test.*(Quota|Budget|PersonalKey|Teamless|RotateKey)'
bash scripts/regression.sh 'TestQuotaThousandAllocationHTTP'
```

前端在 `frontend/` 执行：

```bash
npx vitest run src/components/organisms/createKeyPayload.test.ts src/components/userInfoBudget.test.ts src/components/organisms/PersonalBudgetSummary.test.tsx src/components/organisms/create_key_button.integration.test.tsx 'src/app/(dashboard)/usage/_components/components/UsagePageView.test.tsx' 'src/app/(dashboard)/usage/_components/components/usageChartLocale.test.tsx' --reporter=json --outputFile=../.verification/shared-budget/frontend.json
npx vitest run 'src/app/(dashboard)/usage/_components/components/UsagePageView.test.tsx' --reporter=json --outputFile=../.verification/shared-budget/usage-zero.json
```

仓库根目录执行：

```bash
E2E_RUN_DIR="$PWD/.e2e/shared-budget-final" bash scripts/e2e.sh scenarios/regression/shared-key-budget.test.ts scenarios/budgets/reserved-key.test.ts
git diff --check
```

## 结果

按最新重跑结果合并、去重统计，Go 数量包含子用例：

| 分层 | 通过 | 失败 | 跳过 |
| --- | ---: | ---: | ---: |
| IAM | 237 | 0 | 0 |
| 后台 regression | 277 | 0 | 7 |
| 前端单元与组件 | 124 | 0 | 0 |
| 浏览器 E2E | 3 | 0 | 0 |

E2E 脚本同时完成生产模式前端构建和网关编译。

首轮前端测试的空字段序列化断言、后台旧 Key 分配断言和 E2E 登录等待失败已经修正并重跑；最终统计只保留最新结果，不把跳过计为通过。

## E2E 完整流程

中英文分别用普通用户真实登录，在尚未创建 Key 时确认用量页显示 500。通过页面创建一个固定 500 的 Key 和两个留空 Key，核对接口持久化为数值/null，个人预算和消费保持 500/0。用三个真实 Key 分别调用本地供应商，再通过控制台模型选择与消息输入完成真实推理，检查英文原始提示词及回复。最后删除测试用户及 Key，运行器回收私有 schema。

额外运行既有 `reserved-key` 场景，确认固定 Key 不再阻断共享 Key，消费仍进入团队账单。后台覆盖个人耗尽时所有 Key/会话拒绝、零 Key 只阻断自身、非法金额、创建不消费、已超支仍可创建留空 Key、其他用户预留隔离。

## 本地报告

- `.verification/shared-budget/iam.json`、`key-boundaries.json`
- `.verification/shared-budget/frontend.json`、`usage-zero.json`
- `.e2e/runs/regression-ZolpOAVl/regression.log`（广泛回归，旧分配预期失败已在下一项重跑修正）
- `.e2e/runs/regression-uYIu4lN5/regression.log`（修正后定向通过）
- `.e2e/shared-budget-final/results.json`、`junit.xml`、`html/`

## 边界

7 个 live 子用例因未启用真实供应商凭据跳过，离线对应流程已运行。使用真实 PostgreSQL、隔离网关和浏览器到本地供应商的完整链路；未调用生产 SSO 或真实模型供应商，未变更生产数据。预算提示为已记账快照，不把未结算费用或团队/组织上限伪装成已包含；后台软预算准入仍以实际余额判断。
