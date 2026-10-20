# billing: Deployment rate selection and per-call pricing

[中文](readme_cn.md) · [Parent architecture](../readme.md)

## Integration from other modules

Integration here means other Go packages in this repository. Go internal import rules restrict imports from outside the repository; HTTP clients use mounted gateway endpoints.

After upstream success, call CallCost with the actual deployment ID, measured Usage, and original start time. Check ok and persist the Charge snapshot; accounting owns deductions, deduplication, and persistence.

| Entry point or resource | Usage and result |
| --- | --- |
| DeploymentFinder.FindDeployment(id) | Inject a current deployment lookup; must not be nil |
| CallCost(finder, alias, id, usage, start) | Return total, input/output, ok, and Charge |

### Integration demo

Save the code as `demo/main.go` inside the repository and run `go run ./demo` from the repository root. Remove the demo directory after use.

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

Prints true 0.000140. The fixture illustrates pricing, not real settlement.

### Errors, state, and side effects

Missing manual rates must not fall back to catalog prices. ok=false is not free usage. Model, time, and pricing dimensions must come from execution facts.

## Responsibility

Deployment rate selection and per-call pricing. No body parsing, budget deduction or persistence; missing manual prices must not silently fall back.

## Review order

cost.go: DeploymentFinder → CallCost → deploymentCost

Read injected interfaces or input contracts first, then entry points, helpers and adjacent tests. Inspect parent callers when changing request lifecycle behavior.

## Dependencies

only deployment lookup; catalog owns window/cache/image/video arithmetic; spend.go persists results.

## Verification

Adjacent tests: cost_test.go

From repository root: go test ./internal/gateway/billing -count=1

Real HTTP/persistence contracts belong in cmd/regression; browser → gateway → local upstream flows belong in frontend/e2e. Private schemas and temporary fixtures are cleaned up. Offline tests do not assume paid vendor credentials.
## Native Claude requests and automatic caching

Messages accepts `context_management` only for empty `edits` or strictly validated
`clear_thinking_20251015 / keep=all` no-op edits. These do not change the budget
bound; real clearing and unknown fields remain unsupported.

Every new strict plan freezes all known 5m/1h write rates in each reachable
price tier and reserves the highest cost.
`BudgetPlan.Charge` uses the original usage buckets and the measured context tier
for exact settlement. A client-declared 5m or 1h TTL describes its own cache
blocks; a transparent upstream can add system-prefix caching with another TTL.
Actual buckets can settle when their prices were frozen at reservation time,
even when their TTL differs from the client hint. Unknown TTLs, missing prices
in the actual tier, missing buckets for multiple prices, and contradictory
counts still retain the reservation; the current catalog is never a fallback.
Historical single-TTL snapshots keep their original restrictions and gain no new rates.
Existing uncertain snapshots lack the new rates and must not be automatically
recalculated or refunded using a new plan or the latest catalog.

Coverage: `TestStrictBudgetAutomaticCacheTTL*`, `TestHardBudgetAutomaticCacheTTL`,
`TestClaudeKeepAllBudgetAndStop`, and both `claude-client-compat-` browser locales.
