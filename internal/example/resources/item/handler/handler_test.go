package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/example/resources/item/store"
	"github.com/AranjuezY/saas-template/internal/shared/db"
)

// fakeOrchestrator 用 store 直写模拟编排结果，让 handler 测试无需 Temporal Server。
type fakeOrchestrator struct{ db *sql.DB }

func (f *fakeOrchestrator) CreateItem(ctx context.Context, in port.Item) (port.Item, error) {
	return store.New(f.db).Create(ctx, in)
}

func (f *fakeOrchestrator) AdvanceStage(ctx context.Context, id int64, currentStage, nextStage string) error {
	_, err := store.New(f.db).UpdateStage(ctx, id, nextStage)
	return err
}

func (f *fakeOrchestrator) CancelItem(ctx context.Context, id int64, reason string) error {
	return store.New(f.db).Delete(ctx, id)
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "handler.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	mux := http.NewServeMux()
	New(store.New(database), &fakeOrchestrator{db: database}).Register(mux)
	return mux
}

func do(t *testing.T, h http.Handler, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}

	req := httptest.NewRequest(method, target, body)
	req.Header.Set("HX-Request", "true")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateThenListThenDelete(t *testing.T) {
	h := newTestHandler(t)

	rec := do(t, h, http.MethodPost, "/items", url.Values{"title": {"第一条"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "第一条") {
		t.Errorf("create response missing new row: %s", body)
	}
	if !strings.Contains(body, "data-toast") {
		t.Errorf("create response missing toast: %s", body)
	}

	rec = do(t, h, http.MethodGet, "/items", nil)
	if !strings.Contains(rec.Body.String(), "第一条") {
		t.Error("list should contain the created row")
	}

	// 阶段推进（信号路径，假实现直写）
	rec = do(t, h, http.MethodPatch, "/items/1/stage", url.Values{"stage": {port.StageActive}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="item-1"`) {
		t.Error("patch response should contain the updated row")
	}

	// 阶段推进到终态
	rec = do(t, h, http.MethodPatch, "/items/1/stage", url.Values{"stage": {port.StageArchived}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", rec.Code)
	}

	rec = do(t, h, http.MethodDelete, "/items/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", rec.Code)
	}

	rec = do(t, h, http.MethodGet, "/items", nil)
	if strings.Contains(rec.Body.String(), "第一条") {
		t.Error("item should be gone after delete")
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	h := newTestHandler(t)

	rec := do(t, h, http.MethodPost, "/items", url.Values{"title": {""}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (htmx does not swap on 4xx)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "标题不能为空") {
		t.Errorf("expected validation toast, got: %s", rec.Body.String())
	}
}

func TestOperationsOnMissingItem(t *testing.T) {
	h := newTestHandler(t)

	rec := do(t, h, http.MethodPatch, "/items/999/stage", url.Values{"stage": {port.StageActive}})
	if !strings.Contains(rec.Body.String(), "该条目已不存在") {
		t.Errorf("missing item should yield specific toast, got: %s", rec.Body.String())
	}

	rec = do(t, h, http.MethodDelete, "/items/abc", nil)
	if !strings.Contains(rec.Body.String(), "非法的记录 ID") {
		t.Errorf("invalid id should yield specific toast, got: %s", rec.Body.String())
	}
}
