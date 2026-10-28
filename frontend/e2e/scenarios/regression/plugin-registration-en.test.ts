import { registerPluginRegistrationBoundary } from "../../plugin-registration-scenario";

/** 注册en真实插件登记和对话流程；共享场景负责HTTP断言与清理，不模拟后台或安装客户端。 */
registerPluginRegistrationBoundary("en");
