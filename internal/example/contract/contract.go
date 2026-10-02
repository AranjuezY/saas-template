// Package contract 定义示例域（example）的跨进程契约：
// workflow 类型名、信号名、查询名、workflow ID 规则与任务队列。
//
// 发起侧（workflow/client.go）按这里的名字调用，定义侧（workflow 包）
// 注册时引用同一常量——名字契约单点定义，两侧永不漂移。
// contract 是公共组件：域内任何层都可以依赖契约，只有装配碰实现。
package contract

import "fmt"

// Workflow 类型名（注册名）。
const (
	WorkflowTypeCreate = "CreateItemWorkflow"
	WorkflowTypeItem   = "ItemWorkflow"
	WorkflowTypeCancel = "CancelItemWorkflow"
)

// 信号名。
const (
	SignalActivate = "activate" // 启用，进入 active
	SignalArchive  = "archive"  // 归档，进入 archived 终态
)

// QueryCurrentStage 返回流程当前所处阶段。
const QueryCurrentStage = "current_stage"

// TaskQueueWorkflows 是本域 workflow 任务的队列名。
const TaskQueueWorkflows = "example.workflows"

// ItemWorkflowID 返回某个条目对应的 workflow ID。
// 确定性 ID 是"信号 + 自动拉起"（SignalWithStart）的前提。
func ItemWorkflowID(id int64) string {
	return fmt.Sprintf("item/%d", id)
}
