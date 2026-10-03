// Package contract 定义示例域（example）的跨进程契约：
// workflow 类型名、update 名、查询名、workflow ID 规则与任务队列。
//
// 发起侧（workflow/client.go）按这里的名字调用，定义侧（workflow 包）
// 注册时引用同一常量——名字契约单点定义，两侧永不漂移。
// contract 是公共组件：域内任何层都可以依赖契约，只有装配碰实现。
package contract

import "fmt"

// Workflow 类型名（注册名）。全域只有这一个 workflow：
// 条目生命周期流程——activate 起有效期定时器，renew 顺延，archive 或到期归档。
const WorkflowTypeItem = "ItemWorkflow"

// Update 名。生命周期操作是请求-响应语义（同步校验 + 返回新快照），
// 经 UpdateWithStart 发起：实例不存在时按确定性 ID 自动拉起。
const (
	UpdateActivate = "activate" // 启用：draft → active，起 DefaultValidity 到期定时器
	UpdateRenew    = "renew"    // 续期：到期时间顺延一个 DefaultValidity，定时器重置
	UpdateArchive  = "archive"  // 归档：提前归档进入终态，流程结束
	UpdateDelete   = "delete"   // 删除：流程内删行后自行结束（Entity 模式的 decommission）
)

// 查询名。
const (
	QueryCurrentStage = "current_stage" // 返回流程当前所处阶段
	QueryExpiresAt    = "expires_at"    // 返回流程视角的到期时间（RFC3339，无到期为空串）
)

// TaskQueueWorkflows 是本域 workflow 任务的队列名。
const TaskQueueWorkflows = "example.workflows"

// ItemWorkflowID 返回某个条目对应的 workflow ID。
// 确定性 ID 是"更新 + 自动拉起"（UpdateWithStart）的前提。
func ItemWorkflowID(id int64) string {
	return fmt.Sprintf("item/%d", id)
}
