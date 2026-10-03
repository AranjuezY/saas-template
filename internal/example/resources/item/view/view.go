// Package view 是 item 资源的展示层：templ 组件与展示辅助。
// handler 只认识本包；跨资源共用原语在 shared/ui。
package view

import (
	"fmt"
	"math"
	"net/http"
	"time"

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

// ExpiryLabel 返回到期时间的展示文案："N 天后到期" / "24 小时内到期" /
// "已过期"；无到期时间（draft / archived）返回空串。
func ExpiryLabel(i port.Item) string {
	if i.ExpiresAt == nil {
		return ""
	}
	d := time.Until(*i.ExpiresAt)
	if d < 0 {
		return "已过期，即将自动归档"
	}
	// 向上取整：到期前最后一天仍显示"N 天后"，与"还有几天"的直觉一致。
	if days := int(math.Ceil(d.Hours() / 24)); days >= 1 {
		return fmt.Sprintf("%d 天后到期", days)
	}
	return "24 小时内到期"
}

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
