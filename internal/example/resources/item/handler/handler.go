// Package handler 是 item 资源的 HTTP 处理层。
//
// 写路径全部经 Orchestrator 进入编排（接口由 resgen 生成于 orchestrator_gen.go，
// 生产实现是 workflow.Client，测试用假实现直写 store，
// handler 测试因此无需 Temporal Server）。
package handler

import (
	"net/http"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/example/workflow"
)

// Handler 聚合依赖，所有 handler 都是它的方法。
type Handler struct {
	store port.Ops
	orch  Orchestrator
}

// New 构造 item handler。生产编排实现是 workflow.Client。
func New(st port.Ops, orch Orchestrator) *Handler {
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
