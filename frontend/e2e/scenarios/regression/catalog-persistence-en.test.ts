import { registerCatalogPersistence } from "../../catalog-persistence-scenario";

// 英文真实持久化闭环；前置隔离数据库/本地上游，注册函数负责 finally 清理所有资源。
registerCatalogPersistence("en");
