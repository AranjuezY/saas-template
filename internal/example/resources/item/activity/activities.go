// Package activity 是 item 资源的 Temporal activity 实现：
// workflow 编排调用的全部副作用都在这里发生，本包再经 store 落库。
// 只有流程承载型资源才有本包；普通资源的写由 handler 直调 store。
package activity

import (
	"context"
	"database/sql"
	"errors"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/example/resources/item/store"
)

// TaskQueue 是本资源 activity 任务的队列名。
// workflow 侧用它路由任务，resources-worker 只 poll 这个队列。
const TaskQueue = "example.activities"

// Activities 把 item 的全部写操作封装为 Temporal activity。
type Activities struct {
	store *store.Store
}

// New 基于数据库连接构造 activity 集。
func New(database *sql.DB) *Activities {
	return &Activities{store: store.New(database)}
}

// Register 把所有 activity 注册到 worker。
func (a *Activities) Register(r worker.Registry) {
	r.RegisterActivity(a.CreateItem)
	r.RegisterActivity(a.UpdateItemStage)
	r.RegisterActivity(a.DeleteItem)
}

// CreateItem 落库一条条目记录，返回带 ID 的完整数据。
func (a *Activities) CreateItem(ctx context.Context, in port.Item) (port.Item, error) {
	i, err := a.store.Create(ctx, in)
	return i, wrapActivityError(err)
}

// UpdateItemStage 更新阶段，返回更新后的记录。
// 在生命周期 workflow 里由信号处理分支调用，是阶段流转唯一的写入口。
func (a *Activities) UpdateItemStage(ctx context.Context, id int64, stage string) (port.Item, error) {
	i, err := a.store.UpdateStage(ctx, id, stage)
	return i, wrapActivityError(err)
}

// DeleteItem 删除记录，仅被 CancelItemWorkflow 的收尾环节调用。
func (a *Activities) DeleteItem(ctx context.Context, id int64) error {
	return wrapActivityError(a.store.Delete(ctx, id))
}

// wrapActivityError 把业务错误标记为不可重试并保留用户可读消息；
// 其余错误原样返回，交给重试策略处理。
//
// 错误信封直接用 temporal SDK 的 ApplicationError——它跨进程传输后
// 仍是 *temporal.ApplicationError，handler 侧 errors.As 即可取回消息。
func wrapActivityError(err error) error {
	if err == nil {
		return nil
	}
	var ve port.ValidationError
	switch {
	case errors.As(err, &ve):
		return temporal.NewNonRetryableApplicationError(ve.Message, "validation", err)
	case errors.Is(err, port.ErrNotFound):
		return temporal.NewNonRetryableApplicationError("该条目已不存在", "not_found", err)
	default:
		return err
	}
}
