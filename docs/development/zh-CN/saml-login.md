# SAML 2.0 企业登录

SSO 设置同时支持现有 OIDC 和 SAML 2.0。一个连接绑定一个组织；协议变更需新建连接。SAML 使用 `crewjam/saml`，XML 签名依赖至少为 `goxmldsig v1.6.1`，避免已知签名绕过漏洞。

## 配置和注册

1. 在“单点登录设置”中新增配置，选择组织及 SAML 2.0。
2. 填入 XHub 公开源，例如 `https://console.example.com`。仅联调可显式启用“允许 HTTP 联调”，填写 `http://111.62.156.153:3000`；此选项允许本站地址及手工粘贴 XML 中的 IdP Redirect 登录端点使用 HTTP；远程元数据下载及其中的登录端点仍要求 HTTPS。
3. 生成固定 ACS `/auth/enterprise/saml/acs`、控制台返回 `/ui/login` 和 SP Entity ID。ACS 与返回地址必须同源；不要将回调注册为 `/gw/...`。
4. 填入 IdP HTTPS 元数据 URL（七牛：`https://sso.qiniu.io/saml2/meta`），或清空 URL 后粘贴单个 EntityDescriptor XML。使用 URL 时每次登录读取当前元数据，校验原 issuer 不变；网络失败或元数据过期时拒绝登录。粘贴 XML 需要管理员主动更新证书及有效期。
5. 稳定身份属性默认 `urn:oid:0.9.2342.19200300.100.1.1`（uid）。只接受唯一非空单值，不以 transient NameID 或邮箱自动绑定用户。
6. 先保存未启用配置，然后“下载 SP 元数据”，交给 SSO 管理员注册。服务端为每条连接生成 RSA 2048 位私钥和一年有效的自签名证书；私钥经现有企业 AES-GCM 主密钥加密存储，下载内容只有公开证书。必须保留服务端主密钥及数据库备份。
7. 管理员完成注册且 IdP 验签证书可用后启用配置，在独立浏览器完成登录。未登记身份应拒绝，不能被自动提升为管理员。

公开元数据：`GET /auth/enterprise/{connection_id}/saml/metadata`，返回 XML。管理下载接口：`GET /admin/enterprise/sso/{connection_id}/metadata`，要求管理会话并返回 `{ "metadata": "..." }`。元数据允许在登录尚未启用时导出。

## 用户和权限

沿用企业目录的稳定身份锚点。应先通过现有 SCIM 管理功能登记普通用户，SCIM `externalId` 必须精确对应上述 uid 属性值；SAML 并不要求同步开关持续启用，但用户、组织和 SSO 配置必须有效。没有自动开户、邮箱匹配、组转管理员、LDAP 密码收集或 SAML 单点登出功能。XHub 本地退出不等于注销七牛 SSO 会话。

## HTTP 联调和回调

HTTPS 默认启用 Secure、HttpOnly、SameSite=Lax 的浏览器事务 Cookie。HTTP 联调使用不同的非 Secure Cookie 名称；业务会话 Cookie 随配置源选择 Secure 属性，不信任浏览器传来的转发头来降级。

IdP 的跨站 POST 在验签、受众、请求 ID、接收地址和时效检查后，只暂存稳定身份，再 303 跳转本站 GET。GET 必须携带最初浏览器的随机 Cookie，才能原子消费五分钟事务并签发会话。该流程不要求浏览器在跨站 POST 上发送 Cookie，适用于 HTTP 和 HTTPS；重放、陌生浏览器、配置版本变化或用户停用都不能登录。

HTTP 会明文传输断言和会话，仅用于隔离联调账号；部分浏览器也可能提示从 HTTPS IdP 向 HTTP 提交表单。切换 HTTPS 后需更新回调/返回/Entity ID 和在 SSO 管理员处重新注册对应元数据。

## 已知外部条件（2026-10-20）

七牛生产元数据的 Entity ID 为 `https://sso.qiniu.io/saml2/meta`，Redirect SSO 地址为 `https://sso.qiniu.io`。本次读取到的 IdP 签名证书有效期截至 **2019-05-24**，SHA-256 指纹为 `58:DC:21:82:03:E4:D7:B7:17:03:7E:F6:CF:33:37:4B:DF:B0:50:51:7E:E2:E8:5B:7C:1D:DF:B7:93:A4:DD:1F`。

应用允许保存此未启用配置及导出 SP 元数据，但会显示到期提示并默认拒绝启用。下述隔离联调例外可允许固定过期证书，不跳过签名验证或修改时钟。常规使用仍应由 SSO 管理员提供当前有效的签名证书及匹配元数据并刷新配置。测试用本地 IdP 的成功不能代替七牛真实登录验收。

同轮从部署服务器获取该 HTTPS 元数据返回 403，本机读取成功。因此可由管理员粘贴可信 XML 完成配置，SAML 前通道登录不需要服务器调用 userinfo；证书更新后也需重新导入 XML。

本次未实现自动 SP 私钥轮换；一年后到期前需安排新连接及重新注册元数据，不能只替换数据库证书而丢失原私钥。

## 隔离联调的固定证书到期例外

默认继续拒绝过期 IdP 签名证书。`allow_expired_idp_certificate` 是独立、默认关闭的连接选项，必须同时满足 SAML、`allow_http=true`、元数据地址为空且手工粘贴 XML。现有证书过期警示保留。隔离联调结束应关闭此选项，常规发布前应更新 IdP 证书。

单连接验签器仅复制受信证书的内存对象，并对已过期证书临时延长用于校验的 `NotAfter`；Raw DER、公钥和 `NotBefore` 保持原值。goxmldsig 继续要求原始证书精确匹配，并完整验证 XML 签名。未来证书、陌生证书、篡改、过期断言及重放仍拒绝。不修改全局时钟或断言时钟；此实现依赖锁定版本 goxmldsig v1.6.1 的证书匹配行为，依赖升级时须重新验证。
