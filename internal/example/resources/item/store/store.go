// Package store 是 item 事实的唯一写入点：所有对 items 表的读写都经由此包。
// 写调用只应来自 activity（进而在 workflow 编排之下），读调用来自 handler 直连渲染。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/shared/db"
)

// Store 提供 item 的数据访问。
type Store struct {
	db *sql.DB
}

// New 基于既有连接构造 store。
func New(database *sql.DB) *Store { return &Store{db: database} }

const itemColumns = `id, title, stage, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanItem(sc rowScanner) (port.Item, error) {
	var (
		i                    port.Item
		createdAt, updatedAt string
	)
	if err := sc.Scan(&i.ID, &i.Title, &i.Stage, &createdAt, &updatedAt); err != nil {
		return port.Item{}, err
	}
	i.CreatedAt = db.ParseTime(createdAt)
	i.UpdatedAt = db.ParseTime(updatedAt)
	return i, nil
}

func notFound(err error) error {
	if errors.Is(err, db.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return port.ErrNotFound
	}
	return err
}

// List 查询全部条目，按创建时间倒序。
func (s *Store) List(ctx context.Context) ([]port.Item, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+itemColumns+" FROM items ORDER BY created_at DESC, id DESC LIMIT 500")
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	list := make([]port.Item, 0, 16)
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		list = append(list, i)
	}
	return list, rows.Err()
}

// Get 按 ID 查询单条记录，不存在时返回 port.ErrNotFound。
func (s *Store) Get(ctx context.Context, id int64) (port.Item, error) {
	query := "SELECT " + itemColumns + " FROM items WHERE id = ?"
	i, err := scanItem(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		return port.Item{}, notFound(err)
	}
	return i, nil
}

// Create 插入一条记录并返回落库后的完整数据。
func (s *Store) Create(ctx context.Context, in port.Item) (port.Item, error) {
	in.Normalize()
	if err := in.Validate(); err != nil {
		return port.Item{}, err
	}

	now := db.Now()
	const insert = `INSERT INTO items (title, stage, created_at, updated_at) VALUES (?, ?, ?, ?)`
	res, err := s.db.ExecContext(ctx, insert, in.Title, in.Stage, now, now)
	if err != nil {
		return port.Item{}, fmt.Errorf("create item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return port.Item{}, fmt.Errorf("last insert id: %w", err)
	}
	return s.Get(ctx, id)
}

// UpdateStage 更新条目阶段（只应被 workflow 的信号分支经 activity 调用）。
func (s *Store) UpdateStage(ctx context.Context, id int64, stage string) (port.Item, error) {
	if !port.IsValidStage(stage) {
		return port.Item{}, port.ValidationError{Field: "stage", Message: "未知的阶段"}
	}
	const update = `UPDATE items SET stage = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, update, stage, db.Now(), id)
	if err != nil {
		return port.Item{}, fmt.Errorf("update stage: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return port.Item{}, fmt.Errorf("update stage rows: %w", err)
	} else if n == 0 {
		return port.Item{}, port.ErrNotFound
	}
	return s.Get(ctx, id)
}

// Delete 删除条目记录（只应被 CancelItemWorkflow 的收尾 activity 调用）。
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("delete rows: %w", err)
	} else if n == 0 {
		return port.ErrNotFound
	}
	return nil
}
