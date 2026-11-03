import { registerPluginFilterBoundary } from "../../plugin-filter-scenario";

/** 注册en真实配置筛选与浏览器对话；共享场景负责HTTP断言与清理，不模拟网关响应。 */
registerPluginFilterBoundary("en");
