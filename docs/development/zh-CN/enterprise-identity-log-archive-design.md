# 企业身份与 Kodo 归档

[English](../en/enterprise-identity-log-archive-design.md)

当前实现包括 SCIM 目录同步、OIDC Code + PKCE 登录和 Kodo 归档。PostgreSQL 是身份与计费的权威数据源。

1. 部署公开 HTTPS origin，配置至少 32 字符且稳定的服务主密钥，用于加密企业秘密。
2. SSO 与 SCIM 配置独立管理，创建或删除其中一项不创建或删除另一项。需要同步时，在管理面板创建绑定组织的目录，保存一次性 SCIM 凭据，将生成地址配置到身份平台。
3. 同步用户与组，组映射只能选择同组织团队。
4. 注册 OIDC 应用及准确回调地址，稳定身份声明与 SCIM externalId 对齐；先停用保存，再启用测试。
5. 配置 Kodo bucket、region、凭据与 prefix，先停用保存，测试上传和清理，再启用归档。

OIDC 不创建未同步账号，不按邮箱合并身份。团队选择依次考虑手工覆盖、当前有效团队、排序映射。SCIM 提供有限过滤与属性，不提供 Bulk 或嵌套组。归档按计费事件产生对象，仅保存元数据；计费提交后由 outbox 写入 JSONL.gz 对象及 manifest。

实现见 internal/gateway/directory.go 和 internal/iam 中 directory、enterprise、archive 模块。真实租户和 Kodo 账号需独立验证，参见[验证入口](enterprise-directory-validation.md)。

## SAML 手工元数据自动刷新

已保存 SAML 连接的配置弹窗提供独立的“身份平台元数据自动刷新”区域。默认关闭，来源须为 HTTPS，间隔为 1～24 小时、默认 6 小时。先保存刷新设置，再执行“立即刷新元数据”。原“身份平台元数据 URL”必须留空，保留管理员已确认的 XML；现有每次登录读取 URL 的模式不改变。SSO 未启用时后台不执行，但管理员可以手动刷新。

后台每分钟检查到期任务，服务启动后也检查一次。下一次执行取配置间隔与剩余有效期一半中更早者，失败五分钟后重试；最短间隔一分钟。数据库两分钟租约保证多实例互斥，下载最多 20 秒。无需保持浏览器打开；页面显示状态快照，需要重新加载查看新结果，不包含邮件、短信等外部告警。

只允许更新收到的 XML 时效字段，实体、证书、登录端点及其他已解析协议设置必须保持一致。固定证书变化需要管理员重新导入确认，不会自动轮换；原有单连接过期证书兼容开关继续生效，不取消验签或断言时效检查。刷新失败不覆盖旧 XML；旧 XML 到期后仍拒绝登录，不能假装延期。成功只更新 XML，不提升登录配置 revision；并发编辑配置或调度时丢弃旧任务结果，正在进行的正常登录可以继续。

管理员接口（均沿用平台管理会话，响应 `Cache-Control: no-store`）：

| 方法与路径 | 行为 |
| --- | --- |
| `GET /admin/enterprise/sso/{connection_id}/refresh` | 读取设置及 `last_attempt`、`last_success`、`next_attempt`、`expires_at`（Unix 秒，0 为暂无记录）、`error_code` |
| `PUT /admin/enterprise/sso/{connection_id}/refresh` | 保存 `enabled`、`source_url`、`interval_hours`；保存不代表下载成功 |
| `POST /admin/enterprise/sso/{connection_id}/refresh` | 对已保存设置执行下载校验；成功 200，冲突 409，下载/校验失败 502，可 GET 读取脱敏原因 |

新增路由已登记迁移清单，但不冒充第三阶段五个核心领域的 OpenAPI 契约。定时状态和租约存于独立表 `enterprise_saml_refresh`，删除连接会级联删除；配置和刷新结果记录审计。关闭开关停止后续调度；回退旧程序前先关闭，保留附加表和最后已验证 XML，无需回退用户或预算数据。

本地真实 TLS IdP、PostgreSQL 和中英文浏览器覆盖刷新失败恢复、过期证书显式兼容，以及刷新发生在登录跳转与签名回调之间的完整认证。真实企业网络白名单、证书轮换和供应方服务可用性须在目标环境另行验证。本改动不自动启用任何现有连接。
