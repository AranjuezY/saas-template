package workflow

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/AranjuezY/saas-template/internal/example/contract"
	itemport "github.com/AranjuezY/saas-template/internal/example/resources/item/port"
)

// Client 是 example 域编排的发起侧：把"业务想推进流程"翻译成对
// Temporal Server 的调用（发更新、等结果）。
//
// 它只依赖 contract 契约——按 workflow 类型名（字符串）与确定性 ID
// item/{id} 寻址，而不是函数引用。发起侧与 workflow 定义在编译期解耦，
// 只共享 contract 里的契约字符串；名字的同步由注册时引用同一常量保证。
type Client struct {
	c    client.Client
	wait time.Duration // 等待更新完成的预算
}

// NewClient 包装一个已连接的 Temporal Client。
// waitTimeout 建议 5-15s：一次生命周期更新（含 activity 落库）的总预算。
func NewClient(c client.Client, waitTimeout time.Duration) *Client {
	return &Client{c: c, wait: waitTimeout}
}

// ActivateItem 启用条目：UpdateWithStart 激活 item/{id}，实例不存在时
// （draft 尚无流程）自动拉起；同步返回落库后的新快照，无需轮询数据库。
func (cl *Client) ActivateItem(ctx context.Context, in itemport.ActivateItemInput) (itemport.ActivateItemOutput, error) {
	var out itemport.ActivateItemOutput
	err := cl.updateLifecycle(ctx, in.ID, in.CurrentStage, in.CurrentExpiresAt,
		contract.UpdateActivate, nil, &out.Item)
	return out, err
}

// RenewItem 续期：流程把到期时间顺延一个 DefaultValidity 并重置定时器，
// 同步返回新快照。
func (cl *Client) RenewItem(ctx context.Context, in itemport.RenewItemInput) (itemport.RenewItemOutput, error) {
	var out itemport.RenewItemOutput
	err := cl.updateLifecycle(ctx, in.ID, in.CurrentStage, in.CurrentExpiresAt,
		contract.UpdateRenew, nil, &out.Item)
	return out, err
}

// ArchiveItem 提前归档：流程进入终态并结束，同步返回新快照。
func (cl *Client) ArchiveItem(ctx context.Context, in itemport.ArchiveItemInput) (itemport.ArchiveItemOutput, error) {
	var out itemport.ArchiveItemOutput
	err := cl.updateLifecycle(ctx, in.ID, in.CurrentStage, in.CurrentExpiresAt,
		contract.UpdateArchive, nil, &out.Item)
	return out, err
}

// DeleteItem 删除条目（Entity 模式的 decommission）：流程在实例内删行后
// 自行结束——构造上不可能留下孤儿实例。仅 active（有活实例）走此路径；
// draft / archived 由 handler 直删。
func (cl *Client) DeleteItem(ctx context.Context, in itemport.DeleteItemInput) error {
	// start 参数只是占位（draft / 无到期）：delete 更新与启动原子送达，
	// 新实例在动用快照之前就会先处理删除，快照内容不会被真正用到。
	return cl.updateLifecycle(ctx, in.ID, itemport.StageDraft, "",
		contract.UpdateDelete, []any{in.Reason}, nil)
}

// updateLifecycle 是生命周期更新的公共路径：UpdateWithStart 按确定性
// ID 幂等拉起（已在跑则直接更新，USE_EXISTING），等待更新完成（含
// activity 落库）后把 Handler 返回的快照带回给调用方。
// 请求超时 ≠ 取消：已受理的更新会被持久化执行，worker 恢复后补完成。
func (cl *Client) updateLifecycle(ctx context.Context, id int64, currentStage, currentExpiresAt, update string, updateArgs []any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, cl.wait)
	defer cancel()

	op := cl.c.NewWithStartWorkflowOperation(
		client.StartWorkflowOptions{
			ID:                       contract.ItemWorkflowID(id),
			TaskQueue:                contract.TaskQueueWorkflows,
			WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		},
		contract.WorkflowTypeItem, id, currentStage, currentExpiresAt,
	)

	handle, err := cl.c.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: op,
		UpdateOptions: client.UpdateWorkflowOptions{
			// UpdateID 由（操作 + 条目 + 调用方眼中的当前快照）确定性派生：
			// 同一输入的重放（双击、重试）在实例内按 ID 去重拿到缓存结果；
			// 落库成功后快照变化，新意图自然获得新 ID。不跨 Continue-As-New 续存。
			UpdateID:     fmt.Sprintf("%s-%d-%s-%s", update, id, currentStage, currentExpiresAt),
			UpdateName:   update,
			Args:         updateArgs,
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return fmt.Errorf("update %s for item %d: %w", update, id, err)
	}
	if err := handle.Get(ctx, result); err != nil {
		return fmt.Errorf("run %s for item %d: %w", update, id, err)
	}
	return nil
}
