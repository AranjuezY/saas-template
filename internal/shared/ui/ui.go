// Package ui 存放所有资源 view 共用的渲染原语：布局组件、渲染与 toast 辅助。
// 按依赖规则，只有各资源的 view 子包依赖本包。
package ui

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
)

//go:embed assets/app.css assets/htmx.min.js
var assetsFS embed.FS

// Assets 返回 /assets/ 静态资源（样式与 htmx 均自托管，不依赖 CDN）。
// 挂载示例：mux.Handle("GET /assets/", ui.Assets())。
func Assets() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embed 路径在编译期固定，出错即程序错误
	}
	return http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))
}

// Toast 类型。
const (
	ToastSuccess = "success"
	ToastError   = "error"
	ToastInfo    = "info"
)

// ToastClass 返回 toast 的样式（daisyUI alert 语义类）。
func ToastClass(kind string) string {
	switch kind {
	case ToastSuccess:
		return "alert alert-success"
	case ToastError:
		return "alert alert-error"
	default:
		return "alert alert-info"
	}
}

// FormatTime 统一时间展示格式，零值显示为 "-"。
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// Initial 取首字符用于头像占位。
func Initial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// Render 渲染一个 templ 组件。渲染开始后无法再改状态码，因此先写 header。
func Render(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "render component", "err", err)
	}
}

// RenderWithToast 渲染主片段并附带 OOB toast：header 只写一次。
// 用于"成功路径"——主片段是 htmx 的交换目标，toast 经 hx-swap-oob
// 追加到 #toasts。与此相对，RenderToast 用于"只有 toast 没有主体"
// 的路径（htmx 不交换 4xx/5xx 响应体，因此错误反馈也固定返回 200）。
func RenderWithToast(w http.ResponseWriter, r *http.Request, status int, component templ.Component, kind, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "render component", "err", err)
		return
	}
	_ = Toast(kind, message).Render(r.Context(), w)
}

// RenderToast 是 htmx 的错误反馈通道：固定返回 200，提示通过 out-of-band
// swap 塞进 #toasts（htmx 默认不交换 4xx/5xx 的响应体）。
// 名字与 templ 组件 Toast 区分：组件渲染片段，本函数直接写响应。
func RenderToast(w http.ResponseWriter, r *http.Request, kind, message string) {
	Render(w, r, http.StatusOK, Toast(kind, message))
}

// OOBAttrs 构造 htmx out-of-band 替换属性。
func OOBAttrs(id string) map[string]any {
	return map[string]any{"hx-swap-oob": "outerHTML:#" + id}
}
