package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/shared/db"
)

// newTestStore 为每个测试创建独立的临时数据库，测试之间互不影响。
func newTestStore(t *testing.T) port.Ops {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database)
}

func mustCreate(t *testing.T, st port.Ops, title string) port.Item {
	t.Helper()
	out, err := st.CreateItem(context.Background(), port.CreateItemInput{Item: port.Item{Title: title}})
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return out.Item
}

func TestItemLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	created := mustCreate(t, st, " hello world ")
	if created.ID == 0 {
		t.Fatal("expected non-zero id")
	}
	if created.Title != "hello world" {
		t.Errorf("title not normalized: %q", created.Title)
	}
	if created.Stage != port.StageDraft {
		t.Errorf("stage = %q, want draft", created.Stage)
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at not parsed")
	}

	updated, err := st.ApplyItemLifecycle(ctx, port.ApplyItemLifecycleInput{
		ID:        created.ID,
		Stage:     port.StageActive,
		ExpiresAt: time.Now().Add(port.DefaultValidity).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("apply lifecycle: %v", err)
	}
	if updated.Item.Stage != port.StageActive {
		t.Errorf("stage = %q, want active", updated.Item.Stage)
	}
	if updated.Item.ExpiresAt == nil {
		t.Error("active 后应带到期时间")
	}

	// 同值再推一次：幂等（写相同值，不产生新事实）
	again, err := st.ApplyItemLifecycle(ctx, port.ApplyItemLifecycleInput{
		ID:        created.ID,
		Stage:     port.StageActive,
		ExpiresAt: updated.Item.ExpiryDisplay(),
	})
	if err != nil {
		t.Fatalf("idempotent apply lifecycle: %v", err)
	}
	if again.Item.UpdatedAt.Before(updated.Item.UpdatedAt) {
		t.Error("idempotent update should not regress updated_at")
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	if err := st.DeleteItem(ctx, port.DeleteItemInput{ID: created.ID, Reason: "test"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.DeleteItem(ctx, port.DeleteItemInput{ID: created.ID, Reason: "test"}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	cases := map[string]port.CreateItemInput{
		"标题为空": {Item: port.Item{}},
		"阶段非法": {Item: port.Item{Title: "t", Stage: "hacked"}},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := st.CreateItem(ctx, in)
			var ve port.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if ve.Message == "" {
				t.Error("validation message is empty")
			}
		})
	}
}

func TestApplyLifecycleNotFound(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	_, err := st.ApplyItemLifecycle(ctx, port.ApplyItemLifecycleInput{ID: 4242, Stage: port.StageArchived})
	if !errors.Is(err, port.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetNotFound(t *testing.T) {
	st := newTestStore(t)

	if _, err := st.Get(context.Background(), 9999); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
