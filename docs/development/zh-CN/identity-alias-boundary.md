# 旧身份接口的真实能力边界（第四阶段 C2）

这些旧目录入口没有令牌签发、撤销、内省、邀请消费或 JWT 映射执行器。以前返回的配置对象/列表不代表操作完成：例如 `POST /token` 只保存普通 KV，`POST /v3/login/exchange` 不会消费登录码或创建会话。此次修复让它们明确拒绝，避免客户端把 HTTP 200 当作认证成功。

## 精确范围

| 方法 | 路径 |
| --- | --- |
| `GET` | `/authorize` |
| `GET` | `/discover` |
| `GET` | `/enabled` |
| `GET` | `/invitation/info` |
| `GET` | `/jwt/key/mapping/info` |
| `GET` | `/jwt/key/mapping/list` |
| `GET` | `/onboarding/get_token` |
| `GET` | `/token/generate` |
| `POST` | `/delete` |
| `POST` | `/introspect` |
| `POST` | `/invitation/delete` |
| `POST` | `/invitation/new` |
| `POST` | `/invitation/update` |
| `POST` | `/jwt/key/mapping/delete` |
| `POST` | `/jwt/key/mapping/new` |
| `POST` | `/jwt/key/mapping/update` |
| `POST` | `/onboarding/claim_token` |
| `POST` | `/register` |
| `POST` | `/revoke` |
| `POST` | `/test/connection` |
| `POST` | `/token` |
| `POST` | `/v3/login/exchange` |

只匹配上述方法和完整路径（兼容单个尾斜杠），不按 `token`、`auth` 等前缀扩大范围。

## 响应与兼容性

- 原鉴权不变：无身份 401，普通用户 403；管理员有效 JSON 返回 501，`error.type=unsupported_operation`，`Cache-Control: no-store`。
- 损坏 JSON 或顶层 JSON-RPC 仍先返回 400，不伪装成支持的协议。
- 不读写同名 KV，不返回输入中的码/令牌，不发 `Set-Cookie`，不访问模型上游。
- 原有 KV 保留，不迁移或删除；它们并不是已生效的邀请或令牌。
- 依赖旧入口 HTTP 200 的集成会看到明确失败，不能继续把配置保存当身份操作。尚未提供邀请/JWT映射等替代执行器。

真实 `POST /login`、`/v2/login`、`/v3/login`、退出、`GET /auth/me`、密码重置及 `/auth/enterprise/...` 由现有专属处理器继续执行。`/authorize/flow`、`/authorize/complete` 及 MCP OAuth 的既有不支持响应也不改变。本次不启用跨 worker 登录码兑换，不修改权限、个人预算或 Key 预算。

## 可重复验证

1. `go test ./internal/gateway/family ./internal/gateway -count=1`：精确路径、边界、错误顺序、存储失败及目录扫描。
2. `bash scripts/regression.sh '^Test(IdentityAliasBoundary|EnterpriseSAMLHTTPFlow|UserContractPassword|CustomKeyFailurePreservesSession|TeamlessSessionBudgetHTTP)$'`：真实 HTTP、隔离 PostgreSQL，对每条旧入口检查三类身份、全 KV 前后快照、无上游调用；真实登录/SAML及预算另有回归。
3. `E2E_PORTABLE_CONSOLE=1 bash scripts/e2e.sh scenarios/regression/identity-alias-en.test.ts scenarios/regression/identity-alias-zh-CN.test.ts`：管理员调用旧接口得到明确拒绝，普通用户浏览器登录，预算仍为 500，页面发送英文请求并看到真实离线上游回复，最后删除测试用户及 Key。脚本回收隔离 schema。

上述浏览器和真实数据库测试不 mock 网关；模型上游和 SAML IdP 使用本地确定性夹具，不代表生产企业 IdP、邮件发送或外部 OAuth 供应商可用。身份别名 regression 已接入覆盖率上传，双语 E2E 加入发布门禁；70% 门槛不变。没有合并或生产部署动作。
