-- 更新场景表，增加状态和更新时间
ALTER TABLE scenes 
ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'enabled',
ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();

-- 更新抽成规则表，增加名称、备注、状态、生效时间和更新时间
ALTER TABLE rake_rules 
ADD COLUMN IF NOT EXISTS name TEXT,
ADD COLUMN IF NOT EXISTS remark TEXT,
ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'enabled',
ADD COLUMN IF NOT EXISTS effective_at TIMESTAMPTZ,
ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();

-- 为新字段创建必要的索引（如果需要）
CREATE INDEX IF NOT EXISTS idx_scenes_status ON scenes(status);
CREATE INDEX IF NOT EXISTS idx_rake_rules_status ON rake_rules(status);

