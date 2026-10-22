# 用户企业身份绑定验证

关联 Proposal：#114。

平台管理员在用户新建或编辑表单中选择 SSO 连接，填写该连接的企业账号 ID。七牛 UID 配置下可填写 hanlupeng；其他企业按连接的 identity_claim 填写，不能假定均为邮箱或 LDAP UID。

CSV 新增可选列 sso_connection_id、sso_external_id，必须成对填写，旧模板仍可使用。界面展示可用连接 ID，预览显示身份字段，逐行导入沿用原有结果机制。

编辑时字段缺省保持绑定，两项空字符串解绑。账户与绑定原子提交；重复企业身份返回 409，失败不会留下新账号。改绑/解绑使旧会话失效，清除旧登录映射。绑定不自动加入团队，沿用原有模型和预算授权。SCIM 或目录组管理记录只读；企业登录仅支持普通用户。

## 修改文件

- internal/iam/users.go、user_enterprise_identity.go：事务写入、唯一性、并发串行化、会话及删除清理。
- internal/gateway/identity/handlers.go：创建/详情/更新契约和平台管理员权限。
- EnterpriseIdentityFields.tsx、CreateUserButton.tsx、UserAccountEditor.tsx：真实连接加载、新建、编辑、解绑。
- user_csv.ts、bulk_create_users_button.tsx、networking.tsx：CSV 模板、校验、预览及类型。
- en/zh-CN 翻译和对应单元、组件、后台 regression、E2E 测试。

## 实际执行与结果

Go 命令在仓库根目录执行，npm 命令在 frontend 执行。

    XHUB_REGRESSION_STRICT=1 go test ./internal/iam ./internal/gateway/identity -count=1 -json
    XHUB_REGRESSION_STRICT=1 go test ./internal/iam ./internal/gateway/identity ./cmd/regression -run 'TestEnterpriseIdentity|TestUserEnterpriseIdentity' -count=1 -json
    npm run test:unit -- --run src/components/create_user_payload.test.ts src/components/user_csv.test.ts
    npm run test:component -- --run src/components/EnterpriseIdentityFields.test.tsx src/components/CreateUserButton.test.tsx 'src/app/(dashboard)/users/_components/view_users/UserAccountEditor.test.tsx' src/components/bulk_create_users_button.test.tsx
    npm run test:e2e -- e2e/scenarios/regression/user-enterprise-identity.test.ts
    npm run test:e2e -- e2e/scenarios/regression/user-enterprise-identity.test.ts e2e/scenarios/regression/user-csv-import-en.test.ts e2e/scenarios/regression/user-csv-import-zh-CN.test.ts

- IAM/identity 全量：312 个测试（含子用例）通过，0 失败、0 跳过。
- 专项 IAM/identity/regression：14 个测试（含子用例）通过，0 失败、0 跳过。
- 前端单元 52 通过；组件 25 通过；均 0 失败。
- 新企业绑定 E2E 中英文 2 通过，0 失败：真实 CSV、新建、改绑、TLS IdP 登录、密钥推理、回读、解绑、旧会话 401 和旧身份登录 401。
- 原 CSV 中英文 2 通过：模板下载、输入校验、真实导入和重复导入。之前联合运行的企业绑定用例因测试清理失败，修正后已单独重跑通过。
- E2E 内生产构建及 TypeScript 检查通过。

本地报告：.verification/enterprise-identity/{go,backend-full,unit,components}.json；新 E2E：.e2e/run-10172-24030/results.json、junit.xml；旧 CSV 成功结果：.e2e/run-9608-17340/results.json。过滤运行的目录总覆盖率包含未选择场景，请以 Playwright stats 为准。

## 清理和外部边界

测试使用专属 PostgreSQL schema、本地 TLS IdP 和本地推理上游，无生产员工数据或供应商凭据。用户与 SSO 配置显式清理；目录锚点按现有产品行为保留，由运行器最终删除整个专属 schema。生产七牛 SAML/LDAP 断言与真实供应商未在本次调用，需要发布后用企业真实账号验证。
