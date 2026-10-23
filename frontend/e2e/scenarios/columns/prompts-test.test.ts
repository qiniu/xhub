/** 验证真实配置操作与执行结果；本机协议上游、隔离数据库，辅助流程负责finally清理。 */
import { registerProbeColumn } from "../../probe-scenario";
registerProbeColumn("/prompts", "测试");
