package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"go.temporal.io/sdk/temporal"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/example/resources/item/view"
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

// create 新增条目：draft 无承诺、无流程实例，直调 store 落库即可——
// 生命周期流程在首次 activate 时由 SignalWithStart 拉起。
// 成功后刷新列表片段 + 弹提示。
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		view.ErrorToast(w, r, "表单解析失败")
		return
	}

	created, err := h.store.CreateItem(ctx, port.CreateItemInput{
		Item: port.Item{Title: r.FormValue("title")},
	})
	if err != nil {
		h.writeError(w, r, err, "创建失败，请稍后重试")
		return
	}

	list, err := h.store.List(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "reload items", "err", err)
		view.ErrorToast(w, r, "创建成功，但列表刷新失败，请手动刷新页面")
		return
	}

	view.RowsWithToast(w, r, list, "已添加 "+created.Item.Title+"，激活后开始计有效期")
}

// activate 启用条目：UpdateWithStart 激活流程实例（不存在时自动拉起），
// 同步拿回落库后的新快照，只替换那一行——无需轮询数据库。
func (h *Handler) activate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	current, ok := h.loadItem(w, r)
	if !ok {
		return
	}

	out, err := h.orch.ActivateItem(ctx, port.ActivateItemInput{
		ID:               current.ID,
		CurrentStage:     current.Stage,
		CurrentExpiresAt: current.ExpiryDisplay(),
	})
	if err != nil {
		h.writeError(w, r, err, "启用失败，请稍后重试（需确保 Temporal 与 worker 已启动）")
		return
	}

	// 重复激活返回的快照不含资料字段（title 在库里），渲染前回读全量行。
	i := h.freshRow(ctx, current.ID, out.Item)
	view.RowWithToast(w, r, i, i.Title+" 已启用，"+view.ExpiryLabel(i))
}

// renew 续期：流程把到期时间顺延一个有效期，同步返回新快照。
func (h *Handler) renew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	current, ok := h.loadItem(w, r)
	if !ok {
		return
	}
	if current.Stage != port.StageActive {
		view.ErrorToast(w, r, "只有启用中的条目才能续期")
		return
	}

	out, err := h.orch.RenewItem(ctx, port.RenewItemInput{
		ID:               current.ID,
		CurrentStage:     current.Stage,
		CurrentExpiresAt: current.ExpiryDisplay(),
	})
	if err != nil {
		h.writeError(w, r, err, "续期失败，请稍后重试")
		return
	}

	i := h.freshRow(ctx, current.ID, out.Item)
	view.RowWithToast(w, r, i, i.Title+" 已续期，"+view.ExpiryLabel(i))
}

// archive 提前归档：流程进入终态并结束，同步返回新快照。
func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	current, ok := h.loadItem(w, r)
	if !ok {
		return
	}

	out, err := h.orch.ArchiveItem(ctx, port.ArchiveItemInput{
		ID:               current.ID,
		CurrentStage:     current.Stage,
		CurrentExpiresAt: current.ExpiryDisplay(),
	})
	if err != nil {
		h.writeError(w, r, err, "归档失败，请稍后重试")
		return
	}

	i := h.freshRow(ctx, current.ID, out.Item)
	view.RowWithToast(w, r, i, i.Title+" 已归档")
}

// delete 删除的合规形态（Entity decommission）：
//   - active（有活实例）：向流程发 delete 信号，流程内删行后自行结束——
//     删除全程留在 workflow 事件历史里，且构造上不可能留孤儿实例；
//   - draft / archived（无活实例）：直删即可。
//
// 返回 toast，空 body 让 htmx 移除该 tr。
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	current, err := h.store.Get(ctx, id)
	if err != nil {
		h.writeError(w, r, err, "删除失败，请稍后重试")
		return
	}

	if current.Stage == port.StageActive {
		if err := h.orch.DeleteItem(ctx, port.DeleteItemInput{ID: id, Reason: "deleted from ui"}); err != nil {
			h.writeError(w, r, err, "删除失败，请稍后重试（需确保 Temporal 与 worker 已启动）")
			return
		}
	} else if err := h.store.DeleteItem(ctx, port.DeleteItemInput{ID: id, Reason: "deleted from ui"}); err != nil {
		h.writeError(w, r, err, "删除失败，请稍后重试")
		return
	}

	view.SuccessToast(w, r, "已删除，生命周期流程已随之结束")
}

// freshRow 渲染前回读全量行：更新返回的快照可能不含资料字段
// （流程内存只有 stage / expires_at），回读失败时退回快照。
func (h *Handler) freshRow(ctx context.Context, id int64, fallback port.Item) port.Item {
	if i, err := h.store.Get(ctx, id); err == nil {
		return i
	}
	return fallback
}

// loadItem 读取并校验路径参数 {id}，再取当前快照（作为懒拉起实例的
// start 参数）。失败时已写出响应，返回 false。
func (h *Handler) loadItem(w http.ResponseWriter, r *http.Request) (port.Item, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return port.Item{}, false
	}
	current, err := h.store.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err, "加载条目失败，请稍后重试")
		return port.Item{}, false
	}
	return current, true
}

// writeError 把写路径错误翻译成用户可读的 toast。
// 覆盖三种形态：经 Temporal 传回的业务错误信封（ApplicationError，
// 含流程 Validator 的拒绝）、假编排实现直写库时返回的原始校验错误
// （测试路径），以及兜底。
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
