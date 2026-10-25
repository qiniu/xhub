import { registerCatalogPersistence } from "../../catalog-persistence-scenario";

// 五字节 Key 的页面掩码保存后仍应可推理；共享流程负责真实服务验证和清理。
registerCatalogPersistence("en", true);
