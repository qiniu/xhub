/** 软预算替代旧硬预占场景；保持既有场景入口，真实流程和清理实现在 soft-budget-scenario。 */
export { registerSoftBudgetScenario as registerHardBudgetScenario } from "./soft-budget-scenario";
