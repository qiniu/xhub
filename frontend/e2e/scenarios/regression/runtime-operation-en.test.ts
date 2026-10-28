import { registerRuntimeOperationBoundary } from "../../runtime-operation-scenario";

// 注册en真实配置、运维失败及对话流程，隔离资源由fixture和运行器清理。
registerRuntimeOperationBoundary("en");
