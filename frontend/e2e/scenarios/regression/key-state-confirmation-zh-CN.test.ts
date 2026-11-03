import { registerKeyStateConfirmation } from "../../key-state-confirmation-scenario";

// 独立登记zh-CN密钥状态确认流程；隔离数据库与真实网关，验证异常回复、回读和推理；共享场景finally清理Key。
registerKeyStateConfirmation("zh-CN");
