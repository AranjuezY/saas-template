// Package port 定义 item 资源的对外契约：数据类型、业务规则、错误语义
// 与操作清单。port 是公共组件：任何层都可以依赖契约，只有装配碰实现。
// 跨资源传递的都是这里的快照类型，不带行为、不连数据库。
package port

import (
	"errors"
	"strings"
	"time"
)

// 生命周期阶段。
const (
	StageDraft    = "draft"    // 草稿（创建即入）
	StageActive   = "active"   // 启用
	StageArchived = "archived" // 归档（终态，流程结束）
)

// Stages 按生命周期顺序列出所有合法阶段。
var Stages = []string{StageDraft, StageActive, StageArchived}

// IsValidStage 判断阶段值是否合法。
func IsValidStage(stage string) bool {
	for _, s := range Stages {
		if s == stage {
			return true
		}
	}
	return false
}

// IsTerminalStage 判断阶段是否为终态（终态一到，workflow 结束）。
func IsTerminalStage(stage string) bool { return stage == StageArchived }

var (
	// ErrNotFound 表示目标记录不存在。
	ErrNotFound = errors.New("item not found")
)

// ValidationError 表示入参校验失败，Message 可直接展示给用户。
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Field + ": " + e.Message }

// Item 是一条条目记录的快照。
type Item struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Stage     string    `json:"stage"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Normalize 清理首尾空白并补全默认值。
func (i *Item) Normalize() {
	i.Title = strings.TrimSpace(i.Title)
	i.Stage = strings.TrimSpace(i.Stage)
	if i.Stage == "" {
		i.Stage = StageDraft
	}
}

// Validate 校验业务规则。
func (i Item) Validate() error {
	if i.Title == "" {
		return ValidationError{Field: "title", Message: "标题不能为空"}
	}
	if len([]rune(i.Title)) > 128 {
		return ValidationError{Field: "title", Message: "标题过长"}
	}
	if !IsValidStage(i.Stage) {
		return ValidationError{Field: "stage", Message: "未知的阶段"}
	}
	return nil
}
