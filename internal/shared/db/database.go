// Package db 是资源 store 共用的数据基础设施：SQLite 连接与迁移执行。
//
// 按依赖规则，只有各资源的 store 子包可以 import 本包——
// 这让"每个可变事实只有一个写入点"在依赖图上成立。
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 注册 "sqlite" driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound 表示目标记录不存在（底层语义，store 会翻译成自己 port 的哨兵）。
var ErrNotFound = errors.New("record not found")

// Open 打开（必要时创建）SQLite 数据库，并执行未应用的迁移。
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	dsn := "file:" + path + "?" + strings.Join([]string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(1)",
	}, "&")

	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite 只允许一个写事务。连接池压到 1，配合 busy_timeout 规避 SQLITE_BUSY。
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	database.SetConnMaxLifetime(0)

	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := migrate(ctx, database); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// migrate 按文件名顺序执行 migrations/ 下尚未应用的 SQL 文件。
func migrate(ctx context.Context, database *sql.DB) error {
	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`
	if _, err := database.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	applied, err := appliedVersions(ctx, database)
	if err != nil {
		return err
	}

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := applyMigration(ctx, database, name, string(body)); err != nil {
			return err
		}
		slog.Info("migration applied", "version", name)
	}
	return nil
}

func appliedVersions(ctx context.Context, database *sql.DB) (map[string]bool, error) {
	rows, err := database.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func applyMigration(ctx context.Context, database *sql.DB, name, body string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("exec migration %s: %w", name, err)
	}
	const insert = `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`
	if _, err := tx.ExecContext(ctx, insert, name, Now()); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// Now 返回写入数据库的统一时间格式（UTC RFC3339，字符串排序即时间排序）。
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// ParseTime 解析数据库中的时间字段。
func ParseTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}
