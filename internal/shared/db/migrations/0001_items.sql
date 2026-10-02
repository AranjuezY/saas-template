-- 0001_items: 示例域 item 资源表（派生项目时替换为自己的首个域）
CREATE TABLE IF NOT EXISTS items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT    NOT NULL,
    stage      TEXT    NOT NULL DEFAULT 'draft',
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_items_stage ON items (stage);
CREATE INDEX IF NOT EXISTS idx_items_created_at ON items (created_at DESC);
