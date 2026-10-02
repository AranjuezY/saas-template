// Package view 是 item 资源的展示层：templ 组件与展示辅助。
// handler 只认识本包；跨资源共用原语在 shared/ui。
package view

import (
	"net/http"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/shared/ui"
)

var stageLabels = map[string]string{
	port.StageDraft:    "草稿",
	port.StageActive:   "启用",
	port.StageArchived: "已归档",
}

// StageLabel 返回阶段的中文标签。
func StageLabel(stage string) string {
	if v, ok := stageLabels[stage]; ok {
		return v
	}
	return stage
}

// StageOptions 返回下拉框选项。
func StageOptions() []string { return port.Stages }

// ---- handler 用的渲染辅助（经 shared/ui 中转）----

// ErrorToast 弹出错误提示。
func ErrorToast(w http.ResponseWriter, r *http.Request, message string) {
	ui.RenderToast(w, r, ui.ToastError, message)
}

// SuccessToast 弹出成功提示。
func SuccessToast(w http.ResponseWriter, r *http.Request, message string) {
	ui.RenderToast(w, r, ui.ToastSuccess, message)
}

// OOBAttrs 构造 htmx out-of-band 替换属性。
func OOBAttrs(id string) map[string]any {
	return ui.OOBAttrs(id)
}
