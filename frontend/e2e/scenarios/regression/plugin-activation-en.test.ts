import { registerPluginActivationBoundary } from "../../plugin-activation-scenario";

/** 注册en真实插件启停拒绝与对话流程；前置隔离网关，场景负责HTTP断言和清理，无响应模拟。 */
registerPluginActivationBoundary("en");
