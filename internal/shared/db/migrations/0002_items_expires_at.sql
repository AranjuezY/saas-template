-- 0002_items_expires_at: item 启用后带有效期，到期未续期自动归档。
-- expires_at 由生命周期流程内的 activity 写入（activate / renew 顺延，archive 清空），
-- 页面不得直接改——与 stage 同属流程拥有的事实。
ALTER TABLE items ADD COLUMN expires_at TEXT;

CREATE INDEX IF NOT EXISTS idx_items_expires_at ON items (expires_at);
