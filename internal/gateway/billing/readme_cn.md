# billing：部署费率与一次调用费用计算

[English](readme.md) · [上层架构](../readme_cn.md)

## 如何由其他模块接入

这里的“对外”指仓库内其他 Go 包。Go 的 `internal` 规则限制了仓库外的直接导入；HTTP 客户端使用已挂载的网关接口。

上游成功后以实际部署 ID、实测 Usage 和原请求开始时刻调用 CallCost，检查 ok，保存返回 Charge 的快照；扣预算、去重与落库仍由结算链负责。

| 入口或资源 | 调用方式与返回结果 |
| --- | --- |
| DeploymentFinder.FindDeployment(id) | 注入当前部署查找，不能为 nil |
| CallCost(finder, alias, id, usage, start) | 返回总价、分项、ok 和 Charge |

### 接入 demo

将下面代码保存为仓库内的 `demo/main.go`，在仓库根目录执行 `go run ./demo`；运行后删除本次创建的 demo 目录。

```go
package main

import (
	"fmt"
	"github.com/sunqirui1987/xhub/internal/catalog"
	"github.com/sunqirui1987/xhub/internal/config"
	"github.com/sunqirui1987/xhub/internal/gateway/billing"
	"time"
)

type demoFinder struct{}

// FindDeployment 提供固定演示部署；接收 ID，返回手工单价和存在标记，不访问存储。
func (demoFinder) FindDeployment(id string) (config.ModelEntry, bool) {
	return config.ModelEntry{LiteLLMParams: map[string]any{"input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}, ModelInfo: map[string]any{"pricing_source": "manual"}}, id == "demo-deployment"
}

// main 演示本页 API 的最小调用；无参数和返回值，打印上述预期结果，依赖与副作用见本页说明。
func main() {
	usage := catalog.NormalizeUsage(map[string]any{"prompt_tokens": 100, "completion_tokens": 20})
	total, _, _, priced, _ := billing.CallCost(demoFinder{}, "demo-model", "demo-deployment", usage, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	fmt.Printf("%t %.6f\n", priced, total)
}
```

输出 true 0.000140；假部署查找仅用于演示计价接口，不代表真实结算。

### 错误、状态与副作用

手工缺价不能回落到目录；ok=false 不是免费。实际型号、时间和计价规格必须来自执行事实，不能信任客户端填的费用。

## 职责

部署费率与一次调用费用计算。不读正文、不扣预算、不落库；手工缺价不能默默回落目录。

## Review 顺序

cost.go: DeploymentFinder → CallCost → deploymentCost

先读消费方接口或输入契约，再沿入口阅读辅助函数及旁边的单元测试；涉及请求生命周期时检查上层调用方。

## 依赖

只依赖部署查找；catalog 统一计算时段、缓存、图片和视频价格；spend.go 落库。

## 验证

同目录测试：cost_test.go

仓库根目录执行：go test ./internal/gateway/billing -count=1

真实 HTTP/持久化契约放 cmd/regression；浏览器 → 网关 → 本地上游流程放 frontend/e2e。测试清理私有 schema 与临时夹具；离线测试不假设有付费供应商凭据。

## Claude 原生请求与自动缓存

Messages 的 `context_management` 仅允许空 `edits` 或严格的
`clear_thinking_20251015 / keep=all` 无操作配置，不改变额度上界；真正清理或未知字段仍拒绝。

请求未声明缓存 TTL 时，严格计划冻结可达价格档的 5m/1h 写入价，按最高费用预占，
再由 `BudgetPlan.Charge` 使用原始 usage 分桶和实际上下文档精确结算。
显式 TTL 不一致、未知期限、缺少多期限计量或总数矛盾均不释放占用。
旧 uncertain 的快照没有新费率，不能用新计划或最新目录自动重算、退款。

覆盖：`TestStrictBudgetAutomaticCacheTTL*`、`TestHardBudgetAutomaticCacheTTL`、
`TestClaudeKeepAllBudgetAndStop`，以及双语 `claude-client-compat-` 浏览器场景。
