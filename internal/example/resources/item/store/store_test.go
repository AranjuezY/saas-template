package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/shared/db"
)

// newTestStore 为每个测试创建独立的临时数据库，测试之间互不影响。
func newTestStore(t *testing.T) *Store {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database)
}

func TestItemLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	created, err := st.Create(ctx, port.Item{Title: " hello world "})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
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

	activated, err := st.UpdateStage(ctx, created.ID, port.StageActive)
	if err != nil {
		t.Fatalf("update stage: %v", err)
	}
	if activated.Stage != port.StageActive {
		t.Errorf("stage = %q, want active", activated.Stage)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	if err := st.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.Delete(ctx, created.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	cases := map[string]port.Item{
		"标题为空": {},
		"阶段非法": {Title: "t", Stage: "hacked"},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := st.Create(ctx, in)
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

func TestUpdateStageNotFound(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if _, err := st.UpdateStage(ctx, 4242, port.StageActive); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
