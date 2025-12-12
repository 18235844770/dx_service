-- 初始化 PostgreSQL 数据库脚本
-- 目标：dx_service

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS users (
    id              BIGSERIAL PRIMARY KEY,
    phone           TEXT UNIQUE NOT NULL,
    nickname        TEXT,
    avatar          TEXT,
    location_city   TEXT,
    gps_lat         DOUBLE PRECISION,
    gps_lng         DOUBLE PRECISION,
    invite_code     TEXT UNIQUE,
    bind_agent_id   BIGINT,
    agent_path      TEXT,
    status          TEXT NOT NULL DEFAULT 'normal',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS admins (
    id             BIGSERIAL PRIMARY KEY,
    username       TEXT UNIQUE NOT NULL,
    password_hash  TEXT NOT NULL,
    display_name   TEXT,
    status         TEXT NOT NULL DEFAULT 'active',
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ DEFAULT NOW(),
    updated_at     TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agents (
    id             BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    level          INT DEFAULT 1,
    total_invited  INT DEFAULT 0,
    total_profit   BIGINT DEFAULT 0,
    created_at     TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_profit_logs (
    id             BIGSERIAL PRIMARY KEY,
    agent_id       BIGINT NOT NULL REFERENCES agents(id),
    from_user_id   BIGINT NOT NULL REFERENCES users(id),
    match_id       BIGINT,
    level          INT NOT NULL,
    rake_amount    BIGINT NOT NULL,
    profit_amount  BIGINT NOT NULL,
    created_at     TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wallets (
    user_id            BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    balance_total      BIGINT DEFAULT 0,
    balance_available  BIGINT DEFAULT 0,
    balance_frozen     BIGINT DEFAULT 0,
    total_recharge     BIGINT DEFAULT 0,
    total_win          BIGINT DEFAULT 0,
    total_consume      BIGINT DEFAULT 0,
    total_rake         BIGINT DEFAULT 0,
    updated_at         TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS recharge_orders (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id),
    amount_cny   INT NOT NULL,
    points       BIGINT NOT NULL,
    status       TEXT NOT NULL,
    channel      TEXT,
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    paid_at      TIMESTAMPTZ,
    out_trade_no TEXT UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS billing_logs (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT NOT NULL REFERENCES users(id),
    type           TEXT NOT NULL,
    delta          BIGINT NOT NULL,
    balance_after  BIGINT NOT NULL,
    match_id       BIGINT,
    meta_json      JSONB,
    created_at     TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS scenes (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT NOT NULL,
    seat_count           INT NOT NULL,
    min_in               BIGINT NOT NULL,
    max_in               BIGINT NOT NULL,
    base_pi              BIGINT NOT NULL,
    min_unit_pi          BIGINT NOT NULL,
    mango_enabled        BOOL DEFAULT FALSE,
    bobo_enabled         BOOL DEFAULT FALSE,
    distance_threshold_m INT DEFAULT 0,
    rake_rule_id         BIGINT,
    status               TEXT NOT NULL DEFAULT 'enabled',
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rake_rules (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT,
    type        TEXT NOT NULL,
    remark      TEXT,
    status      TEXT NOT NULL DEFAULT 'enabled',
    config_json JSONB NOT NULL,
    effective_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS agent_rules (
    id                  BIGSERIAL PRIMARY KEY,
    max_level           INT NOT NULL,
    level_ratios_json   JSONB NOT NULL,
    base_platform_ratio FLOAT DEFAULT 0.6,
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tables (
    id            BIGSERIAL PRIMARY KEY,
    scene_id      BIGINT NOT NULL REFERENCES scenes(id),
    status        TEXT NOT NULL,
    seat_count    INT NOT NULL,
    mango_streak  INT DEFAULT 0,
    players_json  JSONB,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS matches (
    id          BIGSERIAL PRIMARY KEY,
    table_id    BIGINT NOT NULL REFERENCES tables(id),
    scene_id    BIGINT NOT NULL REFERENCES scenes(id),
    result_json JSONB,
    rake_json   JSONB,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    ended_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS match_round_logs (
    id          BIGSERIAL PRIMARY KEY,
    match_id    BIGINT NOT NULL REFERENCES matches(id),
    round_no    INT NOT NULL,
    actions_json JSONB,
    cards_json  JSONB,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_queue_users_agent ON users(bind_agent_id);
CREATE INDEX IF NOT EXISTS idx_wallets_updated_at ON wallets(updated_at);
CREATE INDEX IF NOT EXISTS idx_billing_logs_user ON billing_logs(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_match_round_logs_match ON match_round_logs(match_id, round_no);
CREATE INDEX IF NOT EXISTS idx_scenes_status ON scenes(status);
CREATE INDEX IF NOT EXISTS idx_rake_rules_status ON rake_rules(status);

-- 初始化默认管理员 (可选)
-- INSERT INTO admins (username, password_hash, display_name) VALUES ('admin', '$2a$10$YourHashedPasswordHere', 'Super Admin') ON CONFLICT DO NOTHING;
