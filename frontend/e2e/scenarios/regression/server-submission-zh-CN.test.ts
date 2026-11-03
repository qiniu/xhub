import { registerServerSubmissionBoundary } from "../../server-submission-scenario";

/** 注册zh-CN真实浏览器审批拒绝及对话流程；环境与清理由共享场景负责，不模拟HTTP结果。 */
registerServerSubmissionBoundary("zh-CN");
