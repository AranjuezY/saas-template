// Package store 是 item 事实的唯一写入点：所有对 items 表的读写都经由此包。
// stage / expires_at 属于流程拥有的事实，写调用只应来自 activity
// （进而在生命周期 workflow 编排之下）；title 等资料类事实可由 handler 直写。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/shared/db"
)

// itemStore 实现 port.Ops；保持未导出，外界只依赖生成的操作接口。
type itemStore struct {
	db *sql.DB
}

// New 构造 item 的事实写入点，返回生成的操作接口。
func New(database *sql.DB) port.Ops {
	return &itemStore{db: database}
}

// 编译期约束：未导出实现必须满足生成的操作接口。
var _ port.Ops = (*itemStore)(nil)

const itemColumns = `id, title, stage, expires_at, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanItem(sc rowScanner) (port.Item, error) {
	var (
		i                    port.Item
		expires              sql.NullString
		createdAt, updatedAt string
	)
	if err := sc.Scan(&i.ID, &i.Title, &i.Stage, &expires, &createdAt, &updatedAt); err != nil {
		return port.Item{}, err
	}
	if expires.Valid && expires.String != "" {
		if t := db.ParseTime(expires.String); !t.IsZero() {
			i.ExpiresAt = &t
		}
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
func (s *itemStore) List(ctx context.Context) ([]port.Item, error) {
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
func (s *itemStore) Get(ctx context.Context, id int64) (port.Item, error) {
	query := "SELECT " + itemColumns + " FROM items WHERE id = ?"
	i, err := scanItem(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		return port.Item{}, notFound(err)
	}
	return i, nil
}

// CreateItem 插入一条 draft 记录并返回落库后的完整数据。
// draft 无承诺、无流程实例——首次 activate 时才由 SignalWithStart 拉起。
// 幂等与审计规则见 resgen.yaml（CreateItem）。
func (s *itemStore) CreateItem(ctx context.Context, in port.CreateItemInput) (port.CreateItemOutput, error) {
	item := in.Item
	item.Normalize()
	if err := item.Validate(); err != nil {
		return port.CreateItemOutput{}, err
	}

	now := db.Now()
	const insert = `INSERT INTO items (title, stage, expires_at, created_at, updated_at) VALUES (?, ?, NULL, ?, ?)`
	res, err := s.db.ExecContext(ctx, insert, item.Title, item.Stage, now, now)
	if err != nil {
		return port.CreateItemOutput{}, fmt.Errorf("create item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return port.CreateItemOutput{}, fmt.Errorf("last insert id: %w", err)
	}
	created, err := s.Get(ctx, id)
	if err != nil {
		return port.CreateItemOutput{}, err
	}
	return port.CreateItemOutput{Item: created}, nil
}

// ApplyItemLifecycle 是 stage 与 expires_at 的唯一写入口，
// 只应被生命周期流程的信号分支 / 到期定时器经 activity 调用：
//   - activate: stage=active，expires_at 必填（RFC3339，由流程按 DefaultValidity 算出）
//   - renew:    stage=active，expires_at 为顺延后的新到期时间
//   - archive / 到期自动归档: stage=archived，expires_at 置空
func (s *itemStore) ApplyItemLifecycle(ctx context.Context, in port.ApplyItemLifecycleInput) (port.ApplyItemLifecycleOutput, error) {
	switch in.Stage {
	case port.StageActive:
		if _, err := time.Parse(time.RFC3339, in.ExpiresAt); err != nil {
			return port.ApplyItemLifecycleOutput{}, port.ValidationError{Field: "expires_at", Message: "启用时必须携带有效的到期时间"}
		}
	case port.StageArchived:
		if in.ExpiresAt != "" {
			return port.ApplyItemLifecycleOutput{}, port.ValidationError{Field: "expires_at", Message: "归档时到期时间应清空"}
		}
	default:
		return port.ApplyItemLifecycleOutput{}, port.ValidationError{Field: "stage", Message: "生命周期写入只接受 active / archived"}
	}

	const update = `UPDATE items SET stage = ?, expires_at = ?, updated_at = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, update, in.Stage, in.ExpiresAt, db.Now(), in.ID)
	if err != nil {
		return port.ApplyItemLifecycleOutput{}, fmt.Errorf("apply lifecycle: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return port.ApplyItemLifecycleOutput{}, fmt.Errorf("apply lifecycle rows: %w", err)
	} else if n == 0 {
		return port.ApplyItemLifecycleOutput{}, port.ErrNotFound
	}
	item, err := s.Get(ctx, in.ID)
	if err != nil {
		return port.ApplyItemLifecycleOutput{}, err
	}
	return port.ApplyItemLifecycleOutput{Item: item}, nil
}

// DeleteItem 删除条目记录（调用方应先经 CancelItem 取消流程实例；
// 残存流程下一次落库将遇 ErrNotFound 自行退出）。
func (s *itemStore) DeleteItem(ctx context.Context, in port.DeleteItemInput) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, in.ID)
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
