// Package workflow 存放 example 域的所有 Temporal workflow 定义（编排骨架）
// 与发起侧客户端。
//
// 依赖只有三个：contract 契约（类型名、信号名、workflow ID 规则、队列）、
// port 类型、activity 符号（运行时按队列路由到 resources-worker 执行）。
// 定义注册与按名启动引用 contract 的同一常量，两侧永不漂移。
package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/AranjuezY/saas-template/internal/example/contract"
	itemactivity "github.com/AranjuezY/saas-template/internal/example/resources/item/activity"
)

// activityTimeout 是单个 activity 的执行上限。
const activityTimeout = 10 * time.Second

// itemActs 仅为在编排代码中引用 activity 方法名。
// Temporal 按注册名把任务路由到 resources-worker，这些方法值在
// workflow-worker 进程内永远不会被真正调用，零值接收者是安全的。
var itemActs = new(itemactivity.Activities)

// resourceActivityOpts 返回调用资源 activity 的通用选项：
// 走资源队列、限时、基础设施错误自动重试。
// 业务错误（校验失败 / 记录不存在）由 activity 侧标记为不可重试。
func resourceActivityOpts(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue:              itemactivity.TaskQueue,
		StartToCloseTimeout:    activityTimeout,
		ScheduleToCloseTimeout: 2 * activityTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
			InitialInterval: 500 * time.Millisecond,
			MaximumInterval: 5 * time.Second,
		},
	})
}

// RegisterAll 把本域全部 workflow 注册到 worker。由 workflow-worker 调用。
// 显式使用 contract 里的类型名注册：发起侧按同一常量按名启动。
func RegisterAll(r worker.Registry) {
	r.RegisterWorkflowWithOptions(CreateItemWorkflow,
		workflow.RegisterOptions{Name: contract.WorkflowTypeCreate})
	r.RegisterWorkflowWithOptions(ItemWorkflow,
		workflow.RegisterOptions{Name: contract.WorkflowTypeItem})
	r.RegisterWorkflowWithOptions(CancelItemWorkflow,
		workflow.RegisterOptions{Name: contract.WorkflowTypeCancel})
}
