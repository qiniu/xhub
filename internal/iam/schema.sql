-- Identity, membership, keys, usage and audit. Applied to an empty database;
-- there is no migration from the previous JSON-membership schema.

CREATE TABLE IF NOT EXISTS bootstrap_state (
    id           INT PRIMARY KEY CHECK (id = 1),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 具名路由模板保存完整 JSON 策略，router 编译并校验；身份未绑定时使用内置默认值。
-- 放在 organizations 前声明，因为组织表引用模板。
CREATE TABLE IF NOT EXISTS route_templates (
    id         TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name       TEXT NOT NULL UNIQUE CHECK (name <> ''),
    body       TEXT NOT NULL DEFAULT '{}',
    organization_id TEXT,
    team_id TEXT,
    CONSTRAINT route_templates_team_needs_org CHECK (team_id IS NULL OR organization_id IS NOT NULL),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);


CREATE TABLE IF NOT EXISTS users (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    email           TEXT NOT NULL CHECK (email <> ''),
    name            TEXT NOT NULL DEFAULT '',
    password_hash   TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    session_version INT  NOT NULL DEFAULT 1,
    max_budget      NUMERIC CHECK (max_budget IS NULL OR max_budget >= 0),
    spend           NUMERIC NOT NULL DEFAULT 0,
    rpm_limit INT CHECK (rpm_limit IS NULL OR rpm_limit >= 0),
    tpm_limit INT CHECK (tpm_limit IS NULL OR tpm_limit >= 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower ON users (lower(email));

-- 请求预占无外键：团队转移/删除不改变执行中请求的计费路径。结算与释放使用同一事务。
CREATE TABLE IF NOT EXISTS budget_reservations (
    request_id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('key','user')),
    node_id TEXT NOT NULL, key_id TEXT NOT NULL DEFAULT '', user_id TEXT NOT NULL,
    team_id TEXT NOT NULL DEFAULT '', org_id TEXT NOT NULL DEFAULT '',
    amount NUMERIC NOT NULL CHECK (amount >= 0), created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS organizations (
    id         TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name       TEXT NOT NULL UNIQUE CHECK (name <> ''),
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    max_budget NUMERIC CHECK (max_budget IS NULL OR max_budget >= 0),
    spend      NUMERIC NOT NULL DEFAULT 0,
    rpm_limit INT CHECK (rpm_limit IS NULL OR rpm_limit >= 0),
    tpm_limit INT CHECK (tpm_limit IS NULL OR tpm_limit >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    route_template_id TEXT REFERENCES route_templates (id) ON DELETE SET NULL
);

-- 仅补充当前组织表的新字段，不导入旧系统数据；空范围与空元数据使已有组织保持原行为。
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS models TEXT NOT NULL DEFAULT '[]';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS metadata TEXT NOT NULL DEFAULT '{}';

-- Deleting an organization that still has teams is refused (RESTRICT).
--
-- models is the team's model set and the ceiling for everything under it. An
-- empty list means "no restriction", not "no models": a team created a moment
-- ago has had nothing assigned yet, and reading that as a denial would leave it
-- unable to reach anything. A team that must reach nothing is blocked.
CREATE TABLE IF NOT EXISTS teams (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE RESTRICT,
    name            TEXT NOT NULL CHECK (name <> ''),
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    models          TEXT NOT NULL DEFAULT '[]',
    max_budget      NUMERIC CHECK (max_budget IS NULL OR max_budget >= 0),
    spend           NUMERIC NOT NULL DEFAULT 0,
    rpm_limit INT CHECK (rpm_limit IS NULL OR rpm_limit >= 0),
    tpm_limit INT CHECK (tpm_limit IS NULL OR tpm_limit >= 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name),
    UNIQUE (id, organization_id),
    route_template_id TEXT REFERENCES route_templates (id) ON DELETE SET NULL
);

-- An organization administrator runs one organization: its teams, their
-- projects, and the usage inside them. It is a membership rather than an
-- account role, because the same person can administer one organization and be
-- an ordinary member of another.
--
-- A platform administrator does not need a row here: the administrator role is
-- account-wide and already reaches every organization.
CREATE TABLE IF NOT EXISTS organization_members (
    organization_id TEXT NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role            TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('org_admin', 'member')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX IF NOT EXISTS organization_members_user ON organization_members (user_id);

CREATE TABLE IF NOT EXISTS team_members (
    team_id    TEXT NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('team_admin', 'member')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS projects (
    id         TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id    TEXT NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (name <> ''),
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
    models     TEXT NOT NULL DEFAULT '[]',
    max_budget NUMERIC CHECK (max_budget IS NULL OR max_budget >= 0),
    spend      NUMERIC NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (team_id, name),
    UNIQUE (id, team_id)
);

CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    token_hash   TEXT NOT NULL UNIQUE,
    key_prefix   TEXT NOT NULL DEFAULT '',
    owner_type   TEXT NOT NULL DEFAULT 'personal' CHECK (owner_type = 'personal'),
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    project_id   TEXT REFERENCES projects (id) ON DELETE CASCADE,
    created_by   TEXT REFERENCES users (id) ON DELETE SET NULL,
    name         TEXT NOT NULL DEFAULT '',
    models       TEXT NOT NULL DEFAULT '[]',
    max_budget   NUMERIC CHECK (max_budget IS NULL OR max_budget >= 0),
    spend        NUMERIC NOT NULL DEFAULT 0,
    tpm_limit    INT CHECK (tpm_limit IS NULL OR tpm_limit >= 0),
    rpm_limit    INT CHECK (rpm_limit IS NULL OR rpm_limit >= 0),
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked', 'revoked')),
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    route_template_id TEXT REFERENCES route_templates (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS api_keys_user ON api_keys (user_id);

-- Ownership is snapshotted at write time and has no foreign keys, so later
-- membership changes or deletions never re-attribute historical spend.
CREATE TABLE IF NOT EXISTS usage_events (
    id                BIGSERIAL PRIMARY KEY,
    request_id        TEXT NOT NULL UNIQUE,
    ts                TIMESTAMPTZ NOT NULL DEFAULT now(),
    key_id            TEXT NOT NULL DEFAULT '',
    owner_type        TEXT NOT NULL CHECK (owner_type IN ('personal', 'session')),
    user_id           TEXT NOT NULL DEFAULT '',
    team_id           TEXT NOT NULL DEFAULT '',
    project_id        TEXT NOT NULL DEFAULT '',
    organization_id   TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    call_type         TEXT NOT NULL DEFAULT '',
    supplier          TEXT NOT NULL DEFAULT '',
    upstream_model    TEXT NOT NULL DEFAULT '',
    streamed          BOOLEAN NOT NULL DEFAULT false,
    inbound_endpoint TEXT NOT NULL DEFAULT '',
    outbound_endpoint TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT '',
    http_status       INT NOT NULL DEFAULT 0,
    prompt_tokens     INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    cost              NUMERIC NOT NULL DEFAULT 0,
    duration_ms       INT NOT NULL DEFAULT 0,
    ended_at          TIMESTAMPTZ,
    ttft_ms           INT,
    cache_hit         BOOLEAN NOT NULL DEFAULT false,
    key_hash          TEXT NOT NULL DEFAULT '',
    key_alias         TEXT NOT NULL DEFAULT '',
    team_alias        TEXT NOT NULL DEFAULT '',
    provider          TEXT NOT NULL DEFAULT '',
    cached_tokens     INT,
    session_id        TEXT NOT NULL DEFAULT '',
    session_source    TEXT NOT NULL DEFAULT '',
    cache_key         TEXT NOT NULL DEFAULT '',
    guardrail         TEXT NOT NULL DEFAULT '',
    label             TEXT NOT NULL DEFAULT '',
    price_snapshot    TEXT NOT NULL DEFAULT '',
    task_settled      BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS usage_events_team_ts ON usage_events (team_id, ts);
CREATE INDEX IF NOT EXISTS usage_events_user_ts ON usage_events (user_id, ts);
CREATE INDEX IF NOT EXISTS usage_events_key_ts ON usage_events (key_id, ts);
CREATE INDEX IF NOT EXISTS usage_events_org_ts ON usage_events (organization_id, ts);

CREATE INDEX IF NOT EXISTS usage_events_session_ts ON usage_events (session_id, ts);

-- Router template selection, one scope per level of the inheritance chain.
--
-- Null means "not selected", which is what makes a scope inherit from the level
-- above it. There is no third state: a scope either selects a template or it
-- does not, and "selects nothing" is the same row value as "never chosen".
--
-- ON DELETE SET NULL is a backstop only. The delete endpoint refuses while a
-- template is still selected, so in practice no row is ever cleared this way;
-- the constraint is there so an out-of-band delete cannot leave a dangling id.
-- These three indexes back "which scopes currently select this template", the
-- query the delete path runs before refusing.
CREATE INDEX IF NOT EXISTS organizations_route_template ON organizations (route_template_id);
CREATE INDEX IF NOT EXISTS teams_route_template ON teams (route_template_id);
CREATE INDEX IF NOT EXISTS api_keys_route_template ON api_keys (route_template_id);

-- Ownership. Two columns rather than an owner_type plus an id, because the
-- authorization layer already carries OrgID and TeamID on every object: the
-- existing OrgAdminOf / TeamAdminOf take them directly, and a type tag would make
-- every decision translate one shape into the other first.
--
-- Both NULL is a platform template, which everyone reads. Only the organization
-- is a template for that organization and the teams beneath it. Both means a
-- template for one team.
--
-- CASCADE, unlike the SET NULL on the selection columns below: those say "which
-- one was chosen", so a deleted template means nothing was chosen. These say
-- "who owns it", and a configuration whose owner is gone has no meaning.
-- 路由模板和组织/团队互相引用，必须在全部表创建后补齐外键。
-- 这里只处理全新结构的循环依赖；不添加旧列、不修复旧数据、不安装触发器。
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'route_templates'::regclass AND conname = 'route_templates_organization_fk') THEN
        ALTER TABLE route_templates ADD CONSTRAINT route_templates_organization_fk
            FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'route_templates'::regclass AND conname = 'route_templates_team_fk') THEN
        ALTER TABLE route_templates ADD CONSTRAINT route_templates_team_fk
            FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS route_templates_ownership ON route_templates (organization_id, team_id);

CREATE TABLE IF NOT EXISTS request_logs (
    request_id    TEXT PRIMARY KEY REFERENCES usage_events (request_id) ON DELETE CASCADE,
    request_body  TEXT NOT NULL DEFAULT '',
    response_body TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    proxy_request TEXT NOT NULL DEFAULT '',
    upstream_response TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS usage_daily (
    day               DATE NOT NULL,
    organization_id   TEXT NOT NULL DEFAULT '',
    team_id           TEXT NOT NULL DEFAULT '',
    project_id        TEXT NOT NULL DEFAULT '',
    user_id           TEXT NOT NULL DEFAULT '',
    key_id            TEXT NOT NULL DEFAULT '',
    owner_type        TEXT NOT NULL,
    model             TEXT NOT NULL DEFAULT '',
    requests          BIGINT NOT NULL DEFAULT 0,
    prompt_tokens     BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cost              NUMERIC NOT NULL DEFAULT 0,
    PRIMARY KEY (day, organization_id, team_id, project_id, user_id, key_id, owner_type, model)
);
CREATE INDEX IF NOT EXISTS usage_daily_team ON usage_daily (team_id, day);
CREATE INDEX IF NOT EXISTS usage_daily_user ON usage_daily (user_id, day);

CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id    TEXT NOT NULL DEFAULT '',
    actor_kind  TEXT NOT NULL,
    action      TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id   TEXT NOT NULL DEFAULT '',
    team_id     TEXT NOT NULL DEFAULT '',
    detail      JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS audit_logs_ts ON audit_logs (ts);

-- 15 分钟桶准确支持现行民用时区，保留原始授权归属，查询无需扫描账单。
CREATE TABLE IF NOT EXISTS model_monitor_buckets (
    bucket_start TIMESTAMPTZ NOT NULL,
    organization_id TEXT NOT NULL DEFAULT '',
    team_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    key_id TEXT NOT NULL DEFAULT '',
    owner_type TEXT NOT NULL,
    supplier TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    requests BIGINT NOT NULL DEFAULT 0,
    failed_requests BIGINT NOT NULL DEFAULT 0,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cost NUMERIC NOT NULL DEFAULT 0,
    tps_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    tps_samples BIGINT NOT NULL DEFAULT 0,
    ttft_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    ttft_samples BIGINT NOT NULL DEFAULT 0,
    latency_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_samples BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket_start, organization_id, team_id, project_id, user_id, key_id, owner_type, supplier, model)
);
CREATE INDEX IF NOT EXISTS model_monitor_team_time ON model_monitor_buckets (team_id, bucket_start);
CREATE INDEX IF NOT EXISTS model_monitor_user_time ON model_monitor_buckets (user_id, bucket_start);
CREATE INDEX IF NOT EXISTS model_monitor_org_time ON model_monitor_buckets (organization_id, bucket_start);
CREATE INDEX IF NOT EXISTS model_monitor_key_time ON model_monitor_buckets (key_id, bucket_start);
CREATE INDEX IF NOT EXISTS model_monitor_project_time ON model_monitor_buckets (project_id, bucket_start);
CREATE INDEX IF NOT EXISTS model_monitor_supplier_time ON model_monitor_buckets (supplier, model, bucket_start);

-- 客户端备注独立于提示词正文；现有数据库启动时补齐日志字段。
ALTER TABLE usage_events ADD COLUMN IF NOT EXISTS label TEXT NOT NULL DEFAULT '';

-- 上游基础地址随调用快照保存；旧日志保持空值，不以当前配置推断历史。
ALTER TABLE usage_events ADD COLUMN IF NOT EXISTS api_base TEXT NOT NULL DEFAULT '';

-- CREATE TABLE 不会更新旧表；启动时幂等补齐会话来源，避免日志与账单事务整体失败。
-- 历史来源未知时保持空值，不根据当前路由推断，也不改写已有来源或重新结算账单。
ALTER TABLE usage_events ADD COLUMN IF NOT EXISTS session_source TEXT NOT NULL DEFAULT '';

-- 新口径独立存储，旧版本网关只写 tps_sum/tps_samples，不能污染修正后的输出速度。
ALTER TABLE model_monitor_buckets ADD COLUMN IF NOT EXISTS output_tps_sum DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE model_monitor_buckets ADD COLUMN IF NOT EXISTS output_tps_samples BIGINT NOT NULL DEFAULT 0;

-- 指标口径升级仅记录完成状态；账单、费用与日用量保持原样，避免每次启动重复扫描历史事件。
CREATE TABLE IF NOT EXISTS model_monitor_migrations (
    name TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 组织周期按固定 UTC 时长计算；旧组织保持不重置，不回填或改写既有账单。
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS budget_duration TEXT NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS budget_reset_at TIMESTAMPTZ;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS budget_period_start TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS organizations_budget_reset_due ON organizations(budget_reset_at) WHERE budget_duration <> '';
