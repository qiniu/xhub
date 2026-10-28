export const samlRefresh = {
  "title": "身份平台元数据自动刷新",
  "hint": "由网关后台定时更新已保存的 XML，只接受身份标识、证书和端点保持不变的新元数据；这不会续期已过期的签名证书。",
  "enabled": "自动刷新元数据",
  "source": "刷新来源（HTTPS）",
  "interval": "刷新间隔（小时，1～24）",
  "save": "保存刷新设置",
  "now": "立即刷新元数据",
  "load": "重新加载刷新状态",
  "never": "暂无记录",
  "off": "自动刷新已关闭",
  "snapshot": "当前显示状态快照，重新加载可查看后台执行结果。",
  "last_attempt": "最近尝试时间",
  "last_success": "最近成功刷新",
  "next_attempt": "下次计划执行",
  "expires_at": "元数据到期时间",
  "saved": "刷新设置已保存",
  "refreshed": "元数据刷新成功",
  "error": "操作失败，请查看刷新状态后重试。",
  "errors": {
    "invalid_baseline": "已保存的元数据无效，请先导入可信 XML。",
    "invalid_metadata": "下载的元数据无效或无法用于 SAML。",
    "invalid_expiry": "下载的元数据没有可用的新到期时间。",
    "trust_changed": "身份标识、证书或协议配置发生变化，需要管理员确认。",
    "fetch_failed": "元数据下载失败，已保留原 XML。",
    "invalid_configuration": "刷新需要已保存的手工元数据和 HTTPS 来源。",
    "unknown": "刷新失败，请查看网关日志。"
  }
};
