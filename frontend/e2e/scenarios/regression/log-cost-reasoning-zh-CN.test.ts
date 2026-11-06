/** 日志 zh-CN 闭环：三协议调用、完整小额费用和请求推理强度；场景与运行器清理所有测试数据。 */
import { registerLogCostReasoningScenario } from "../../log-cost-reasoning-scenario";
import { registerQueueConflictScenario } from "../../queue-conflict-scenario";
registerLogCostReasoningScenario("zh-CN");
// 发布验收已配置私有 Redis；在该模式下同时验证冲突账单不会阻断后续请求。
if (process.env.E2E_REDIS === "1") registerQueueConflictScenario("zh-CN");
