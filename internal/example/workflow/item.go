package workflow

import (
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"

	"github.com/AranjuezY/saas-template/internal/example/contract"
	itemport "github.com/AranjuezY/saas-template/internal/example/resources/item/port"
)

// CreateItemWorkflow 是"新增条目"的入口 workflow（短命令型）。
//
// 职责只有两步：
//  1. 调 CreateItem activity 落库拿到 ID；
//  2. 以确定性 ID item/{id} 拉起该条目的生命周期流程（子 workflow），
//     只等待"已启动"确认而不等它跑完。
func CreateItemWorkflow(ctx workflow.Context, in itemport.Item) (itemport.Item, error) {
	ctx = resourceActivityOpts(ctx)

	var created itemport.CreateItemOutput
	if err := workflow.ExecuteActivity(ctx, itemActs.CreateItem, itemport.CreateItemInput{Item: in}).Get(ctx, &created); err != nil {
		return itemport.Item{}, err
	}

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: contract.ItemWorkflowID(created.Item.ID),
		// 父流程（短命令）结束后子流程必须继续跑：
		// Temporal 对子流程默认 ParentClosePolicy 是 Terminate，
		// 这里必须显式 Abandon，否则生命周期流程会随创建命令一起被终止。
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})
	child := workflow.ExecuteChildWorkflow(childCtx, ItemWorkflow, created.Item.ID, created.Item.Stage)
	var childWE workflow.Execution
	if err := child.GetChildWorkflowExecution().Get(ctx, &childWE); err != nil {
		return itemport.Item{}, err
	}
	return created.Item, nil
}

// ItemWorkflow 是单个条目的生命周期流程（长时编排，骨架）。
//
// 启动后立即进入信号等待循环，阶段流转全部由外部信号驱动；
// 每次流转通过 UpdateItemStage activity 落库——本 workflow 是
// 条目阶段数据的唯一写者，HTTP 层无法绕过这里直接改库。
//
// 信号契约（定义在 example/contract）：
//   - activate: 启用，进入 active
//   - archive:  归档，进入 archived 终态，流程结束
func ItemWorkflow(ctx workflow.Context, itemID int64, initialStage string) (string, error) {
	stage := initialStage
	if !itemport.IsValidStage(stage) {
		stage = itemport.StageDraft
	}

	// current_stage 查询：不开数据库也能看到流程视角的阶段。
	_ = workflow.SetQueryHandler(ctx, contract.QueryCurrentStage, func() (string, error) {
		return stage, nil
	})

	activate := workflow.GetSignalChannel(ctx, contract.SignalActivate)
	archive := workflow.GetSignalChannel(ctx, contract.SignalArchive)

	actx := resourceActivityOpts(ctx)

	selector := workflow.NewSelector(ctx)

	// 取消分支：workflow 被外部取消（如 CancelItemWorkflow）时，
	// ctx.Done() 收到值，Select 得以返回，循环退出并以上下文错误关闭流程。
	// 注意 Selector.Select(ctx) 不会自动观察取消——没有这一支，
	// 流程会在取消请求送达后继续阻塞，留下孤儿 Running 实例。
	selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, _ bool) {
		c.Receive(ctx, nil)
	})

	selector.AddReceive(activate, func(c workflow.ReceiveChannel, _ bool) {
		var payload string
		c.Receive(ctx, &payload)
		var updated itemport.UpdateItemStageOutput
		if err := workflow.ExecuteActivity(actx, itemActs.UpdateItemStage,
			itemport.UpdateItemStageInput{ID: itemID, Stage: itemport.StageActive}).Get(ctx, &updated); err != nil {
			workflow.GetLogger(ctx).Error("activate activity failed", "item_id", itemID, "err", err)
			return
		}
		stage = updated.Item.Stage
		_ = payload
	})
	selector.AddReceive(archive, func(c workflow.ReceiveChannel, _ bool) {
		var payload string
		c.Receive(ctx, &payload)
		var updated itemport.UpdateItemStageOutput
		if err := workflow.ExecuteActivity(actx, itemActs.UpdateItemStage,
			itemport.UpdateItemStageInput{ID: itemID, Stage: itemport.StageArchived}).Get(ctx, &updated); err != nil {
			workflow.GetLogger(ctx).Error("archive activity failed", "item_id", itemID, "err", err)
			return
		}
		stage = updated.Item.Stage
		_ = payload
	})

	for !itemport.IsTerminalStage(stage) {
		selector.Select(ctx) // 阻塞直到某个信号到达
		if ctx.Err() != nil {
			return stage, ctx.Err()
		}
	}
	return stage, nil
}

// CancelItemWorkflow 是"删除条目"的合规形态（短命令型）。
//
// 手动删除流程承载型资源不允许直接删行——那会留下孤儿流程并丢失历史。
// 这里的顺序是：
//  1. 请求取消该条目的生命周期 workflow（若已结束/不存在则忽略）；
//  2. DeleteItem activity 删除记录，全程留下审计轨迹。
func CancelItemWorkflow(ctx workflow.Context, itemID int64, reason string) error {
	ctx = resourceActivityOpts(ctx)

	// best-effort：流程可能早已终态或从未启动（例如历史数据）。
	if err := workflow.RequestCancelExternalWorkflow(ctx, contract.ItemWorkflowID(itemID), "").Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Info("cancel item workflow skipped",
			"item_id", itemID, "reason", reason, "err", err)
	}

	return workflow.ExecuteActivity(ctx, itemActs.DeleteItem,
		itemport.DeleteItemInput{ID: itemID, Reason: reason}).Get(ctx, nil)
}
