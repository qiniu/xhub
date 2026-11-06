import { registerQueueConflictScenario } from "../e2e/queue-conflict-scenario";

// 独立 Redis 浏览器回归；服务、数据库和缓存由测试运行器清理。
registerQueueConflictScenario("en");
registerQueueConflictScenario("zh-CN");
