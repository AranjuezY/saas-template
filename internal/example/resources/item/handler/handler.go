// Package handler 是 item 资源的 HTTP 处理层。
//
// 写路径全部经 Orchestrator 进入编排（接口定义在本包——消费者侧），
// 生产实现是 workflow.Client，测试用假实现直写 store，
// handler 测试因此无需 Temporal Server。
package handler

import (
	"context"
	"net/http"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/example/resources/item/store"
	"github.com/AranjuezY/saas-template/internal/example/workflow"
)

// Orchestrator 是 handler 对编排层的全部需求（消费者侧接口）。
type Orchestrator interface {
	// CreateItem 落库并拉起生命周期流程，返回完整记录。
	CreateItem(ctx context.Context, in port.Item) (port.Item, error)
	// AdvanceStage 向条目流程发阶段信号；currentStage 用于流程未启动时的拉起。
	AdvanceStage(ctx context.Context, id int64, currentStage, nextStage string) error
	// CancelItem 删除条目并取消其流程（删除的合规形态）。
	CancelItem(ctx context.Context, id int64, reason string) error
}

// Handler 聚合依赖，所有 handler 都是它的方法。
type Handler struct {
	store *store.Store
	orch  Orchestrator
}

// New 构造 item handler。生产实现是 workflow.Client。
func New(st *store.Store, orch Orchestrator) *Handler {
	return &Handler{store: st, orch: orch}
}

// Register 把 item 路由挂到 mux（Go 1.22+ 方法+通配符语法）。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /items", h.list)
	mux.HandleFunc("POST /items", h.create)
	mux.HandleFunc("PATCH /items/{id}/stage", h.updateStage)
	mux.HandleFunc("DELETE /items/{id}", h.delete)
}

// 编译期约束：编排生产实现满足消费者侧接口。
var _ Orchestrator = (*workflow.Client)(nil)
