package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/example/resources/item/view"
)

// 阶段信号发出后，workflow 内 activity 落库是异步的。
// handler 短轮询数据库直至变化可见；超时则按当前状态渲染（最终一致）。
const (
	stagePollInterval = 50 * time.Millisecond
	stagePollTimeout  = 2 * time.Second
)

// list 只返回表格行片段，供 htmx 局部刷新使用（读路径直连库）。
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	list, err := h.store.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list items", "err", err)
		view.ErrorToast(w, r, "加载失败，请稍后重试")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = view.Rows(list).Render(ctx, w)
}

// create 新增条目：经 CreateItemWorkflow 落库并拉起生命周期流程，
// 成功后刷新列表片段 + 弹提示。
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		view.ErrorToast(w, r, "表单解析失败")
		return
	}

	in := port.Item{Title: r.FormValue("title")}

	created, err := h.orch.CreateItem(ctx, in)
	if err != nil {
		h.writeError(w, r, err, "创建失败，请稍后重试（需确保 Temporal 与 worker 已启动）")
		return
	}

	list, err := h.store.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "reload items", "err", err)
		view.ErrorToast(w, r, "创建成功，但列表刷新失败，请手动刷新页面")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = view.Rows(list).Render(ctx, w)
	view.SuccessToast(w, r, "已添加 "+created.Title+"，生命周期流程已启动")
}

// updateStage 阶段流转：向该条目的流程实例发信号，随后短轮询
// 数据库拿到落库结果，只替换那一行。
func (h *Handler) updateStage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		view.ErrorToast(w, r, "表单解析失败")
		return
	}
	next := r.FormValue("stage")
	if !port.IsValidStage(next) {
		view.ErrorToast(w, r, "未知的阶段")
		return
	}

	current, err := h.store.Get(ctx, id)
	if err != nil {
		h.writeError(w, r, err, "加载条目失败，请稍后重试")
		return
	}

	if err := h.orch.AdvanceStage(ctx, id, current.Stage, next); err != nil {
		h.writeError(w, r, err, "更新失败，请稍后重试")
		return
	}

	i := h.waitForStage(ctx, id, next)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = view.Row(i).Render(ctx, w)
	view.SuccessToast(w, r, i.Title+" → "+view.StageLabel(i.Stage))
}

// delete 删除的合规形态：经 CancelItemWorkflow 取消流程并删除记录。
// 返回 toast，空 body 让 htmx 移除该 tr。
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if err := h.orch.CancelItem(ctx, id, "deleted from ui"); err != nil {
		h.writeError(w, r, err, "删除失败，请稍后重试")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	view.SuccessToast(w, r, "已删除，生命周期流程已取消")
}

// waitForStage 轮询直到阶段变为 want 或超时，返回最后读到的记录。
func (h *Handler) waitForStage(ctx context.Context, id int64, want string) port.Item {
	var i port.Item
	deadline := time.Now().Add(stagePollTimeout)
	for {
		var err error
		i, err = h.store.Get(ctx, id)
		if err != nil || i.Stage == want || time.Now().After(deadline) {
			return i
		}
		time.Sleep(stagePollInterval)
	}
}

// writeError 把写路径错误翻译成用户可读的 toast。
// 覆盖两种形态：经 Temporal 传输回来的业务错误信封（ApplicationError），
// 以及假编排实现直写库时返回的原始错误（测试路径）。
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	var ae *temporal.ApplicationError
	if errors.As(err, &ae) && ae.NonRetryable() {
		view.ErrorToast(w, r, ae.Message())
		return
	}

	var ve port.ValidationError
	switch {
	case errors.As(err, &ve):
		view.ErrorToast(w, r, ve.Message)
	case errors.Is(err, port.ErrNotFound):
		view.ErrorToast(w, r, "该条目已不存在")
	default:
		slog.ErrorContext(r.Context(), "orchestrator error", "err", err)
		view.ErrorToast(w, r, fallback)
	}
}

// pathID 读取并校验路径参数 {id}。
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		view.ErrorToast(w, r, "非法的记录 ID")
		return 0, false
	}
	return id, true
}
