export const cacheAdminZhCN = {
  title: "响应缓存",
  scope: "仅在当前网关进程缓存符合条件的完整非流式响应，与 Responses API 历史及供应商提示词缓存无关。清空不影响其他实例、Redis、会话、预算或用量记录。",
  unsupported: "目前不支持动态缓存配置和连接测试。以前保存的设置不会生效。",
  ready: "当前进程内存缓存可用",
  loading: "正在检查响应缓存…",
  loadFailed: "无法确认缓存状态，请刷新重试。",
  flush: "清空当前进程缓存",
  flushing: "正在清空…",
  flushed: "当前进程的响应缓存已清空。",
  flushFailed: "清空缓存失败，尚未确认成功；请重试或检查服务。",
  refresh: "刷新状态",
};
