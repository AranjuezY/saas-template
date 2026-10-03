-- 0001_items: 示例域 item 资源表——模板的唯一基线迁移。
-- 派生项目把本文件整体替换为自己首个域的建表；此后结构演进只新增
-- 编号更大的迁移文件，不回改已应用的迁移（db.Open 按文件名顺序补应用）。
CREATE TABLE IF NOT EXISTS items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT    NOT NULL,
    stage      TEXT    NOT NULL DEFAULT 'draft',
    expires_at TEXT,               -- 到期时间（active 必有；draft / archived 为空），
                                   -- 由生命周期流程的 activity 写入，页面不得直接改
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_items_stage ON items (stage);
CREATE INDEX IF NOT EXISTS idx_items_created_at ON items (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_items_expires_at ON items (expires_at);
