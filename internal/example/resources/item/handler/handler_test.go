package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/example/resources/item/store"
	"github.com/AranjuezY/saas-template/internal/shared/db"
)

// fakeOrchestrator 用 store 直写模拟编排结果，让 handler 测试无需 Temporal Server。
// 语义与真实流程一致：activate 设 7 天到期，renew 从当前到期顺延一个周期
// （过期即拒），archive 清空到期，delete 直删。
type fakeOrchestrator struct{ ops port.Ops }

func (f *fakeOrchestrator) ActivateItem(ctx context.Context, in port.ActivateItemInput) (port.ActivateItemOutput, error) {
	cur, err := f.ops.Get(ctx, in.ID)
	if err != nil {
		return port.ActivateItemOutput{}, err
	}
	if cur.Stage == port.StageActive {
		// 幂等统一规则：目标已达成返回当前快照
		return port.ActivateItemOutput{Item: cur}, nil
	}
	out, err := f.ops.ApplyItemLifecycle(ctx, port.ApplyItemLifecycleInput{
		ID:        in.ID,
		Stage:     port.StageActive,
		ExpiresAt: time.Now().Add(port.DefaultValidity).UTC().Format(time.RFC3339),
	})
	return port.ActivateItemOutput{Item: out.Item}, err
}

func (f *fakeOrchestrator) RenewItem(ctx context.Context, in port.RenewItemInput) (port.RenewItemOutput, error) {
	base := time.Now().UTC()
	cur, err := f.ops.Get(ctx, in.ID)
	if err != nil {
		return port.RenewItemOutput{}, err
	}
	if cur.Stage != port.StageActive {
		return port.RenewItemOutput{}, port.ValidationError{Field: "stage", Message: "只有未到期的启用条目才能续期"}
	}
	if cur.ExpiresAt != nil && cur.ExpiresAt.After(base) {
		base = *cur.ExpiresAt
	}
	out, err := f.ops.ApplyItemLifecycle(ctx, port.ApplyItemLifecycleInput{
		ID:        in.ID,
		Stage:     port.StageActive,
		ExpiresAt: base.Add(port.DefaultValidity).UTC().Format(time.RFC3339),
	})
	return port.RenewItemOutput{Item: out.Item}, err
}

func (f *fakeOrchestrator) ArchiveItem(ctx context.Context, in port.ArchiveItemInput) (port.ArchiveItemOutput, error) {
	out, err := f.ops.ApplyItemLifecycle(ctx, port.ApplyItemLifecycleInput{
		ID:    in.ID,
		Stage: port.StageArchived,
	})
	return port.ArchiveItemOutput{Item: out.Item}, err
}

func (f *fakeOrchestrator) DeleteItem(ctx context.Context, in port.DeleteItemInput) error {
	return f.ops.DeleteItem(ctx, in)
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "handler.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	ops := store.New(database)
	mux := http.NewServeMux()
	New(ops, &fakeOrchestrator{ops: ops}).Register(mux)
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

func TestLifecycleFlow(t *testing.T) {
	h := newTestHandler(t)

	rec := do(t, h, http.MethodPost, "/items", url.Values{"title": {"第一条"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "第一条") || !strings.Contains(body, "data-toast") {
		t.Errorf("create response missing row/toast: %s", body)
	}

	// draft 无到期时间
	if strings.Contains(body, "后到期") {
		t.Error("draft 不应显示到期信息")
	}

	// 启用：7 天后到期
	rec = do(t, h, http.MethodPost, "/items/1/activate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "7 天后到期") {
		t.Errorf("activate 后应显示 7 天后到期: %s", rec.Body.String())
	}

	// 重复激活：幂等规则——目标已达成返回当前快照，到期不重算（仍为 7 天）
	rec = do(t, h, http.MethodPost, "/items/1/activate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate activate status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "7 天后到期") {
		t.Errorf("重复激活不应重算到期: %s", rec.Body.String())
	}

	// 续期：到期顺延到 14 天后
	rec = do(t, h, http.MethodPost, "/items/1/renew", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("renew status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "14 天后到期") {
		t.Errorf("renew 后应显示 14 天后到期: %s", rec.Body.String())
	}

	// 提前归档：到期清空、流程终态
	rec = do(t, h, http.MethodPost, "/items/1/archive", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "已归档") {
		t.Errorf("archive 后行应显示已归档: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "后到期") {
		t.Error("归档后不应再显示到期信息")
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

	rec := do(t, h, http.MethodPost, "/items/999/activate", nil)
	if !strings.Contains(rec.Body.String(), "该条目已不存在") {
		t.Errorf("missing item should yield specific toast, got: %s", rec.Body.String())
	}

	rec = do(t, h, http.MethodDelete, "/items/abc", nil)
	if !strings.Contains(rec.Body.String(), "非法的记录 ID") {
		t.Errorf("invalid id should yield specific toast, got: %s", rec.Body.String())
	}
}

func TestRenewOnlyWhenActive(t *testing.T) {
	h := newTestHandler(t)

	do(t, h, http.MethodPost, "/items", url.Values{"title": {"t"}})
	rec := do(t, h, http.MethodPost, "/items/1/renew", nil)
	if !strings.Contains(rec.Body.String(), "只有启用中的条目才能续期") {
		t.Errorf("renew on draft should be rejected, got: %s", rec.Body.String())
	}
}
