-- 企业登录配置和稳定身份绑定；秘密由服务端加密后存储。
CREATE TABLE IF NOT EXISTS enterprise_oidc (
 connection_id TEXT PRIMARY KEY REFERENCES directory_connections(id) ON DELETE RESTRICT, name TEXT NOT NULL CHECK(name <> ''),
 issuer TEXT NOT NULL, client_id TEXT NOT NULL, secret TEXT NOT NULL DEFAULT '',
 redirect_uri TEXT NOT NULL, return_uri TEXT NOT NULL, identity_claim TEXT NOT NULL DEFAULT 'sub',
 enabled BOOLEAN NOT NULL DEFAULT false, revision BIGINT NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS enterprise_login_transactions (
 state_hash TEXT PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES enterprise_oidc(connection_id),
 revision BIGINT NOT NULL, browser_hash TEXT NOT NULL, nonce TEXT NOT NULL, verifier TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS enterprise_login_identities (
 connection_id TEXT NOT NULL REFERENCES enterprise_oidc(connection_id), issuer TEXT NOT NULL, subject TEXT NOT NULL,
 user_id TEXT NOT NULL REFERENCES users(id), PRIMARY KEY(connection_id,issuer,subject),
 UNIQUE(connection_id,user_id)
);

-- 保留已有 OIDC 数据和外键，幂等扩展协议配置；新连接默认仍要求 HTTPS。
ALTER TABLE enterprise_oidc ADD COLUMN IF NOT EXISTS protocol TEXT NOT NULL DEFAULT 'oidc';
ALTER TABLE enterprise_oidc ADD COLUMN IF NOT EXISTS allow_http BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE enterprise_oidc ADD COLUMN IF NOT EXISTS idp_metadata TEXT NOT NULL DEFAULT '';
ALTER TABLE enterprise_oidc ADD COLUMN IF NOT EXISTS idp_metadata_url TEXT NOT NULL DEFAULT '';
ALTER TABLE enterprise_oidc ADD COLUMN IF NOT EXISTS certificate TEXT NOT NULL DEFAULT '';
-- 验证过的稳定身份仅存活到五分钟事务过期；最终 GET 仍须验证原浏览器凭据。
ALTER TABLE enterprise_login_transactions ADD COLUMN IF NOT EXISTS saml_subject TEXT NOT NULL DEFAULT '';

-- 不同组织不能共享同一 SAML 实体，以免 IdP 注册覆盖；旧 OIDC client_id 不受影响。
CREATE UNIQUE INDEX IF NOT EXISTS enterprise_saml_entity_unique ON enterprise_oidc(client_id) WHERE protocol='saml';

ALTER TABLE enterprise_oidc ADD COLUMN IF NOT EXISTS allow_expired_idp_certificate BOOLEAN NOT NULL DEFAULT false;

-- 手工 SAML 元数据刷新默认关闭；单独租约表不改变已发起登录的配置版本。
CREATE TABLE IF NOT EXISTS enterprise_saml_refresh (
 connection_id TEXT PRIMARY KEY REFERENCES enterprise_oidc(connection_id) ON DELETE CASCADE,
 enabled BOOLEAN NOT NULL DEFAULT false, source_url TEXT NOT NULL DEFAULT '',
 interval_hours INTEGER NOT NULL DEFAULT 6 CHECK(interval_hours BETWEEN 1 AND 24),
 last_attempt BIGINT NOT NULL DEFAULT 0, last_success BIGINT NOT NULL DEFAULT 0,
 next_attempt BIGINT NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
 lease TEXT NOT NULL DEFAULT '', lease_until BIGINT NOT NULL DEFAULT 0
);
