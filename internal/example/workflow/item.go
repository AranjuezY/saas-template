package workflow

import (
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/AranjuezY/saas-template/internal/example/contract"
	itemport "github.com/AranjuezY/saas-template/internal/example/resources/item/port"
)

// ItemWorkflow 是条目生命周期流程——example 域唯一的 workflow，
// 实现 Temporal 的 Entity Workflow 模式：一个条目一个实例（实体 ID 即
// workflow ID item/{id}），所有状态转移经 Update 驱动。
//
// 语义：item 激活后带 DefaultValidity（默认 7 天）有效期。
//   - activate 更新：draft → active，expires_at = 现在 + 有效期，起到期定时器；
//   - renew 更新：到期时间 = max(当前到期, 现在) + 有效期，顺延一个周期并重置定时器；
//   - archive 更新：提前归档（终态），取消定时器，流程结束；
//   - 到期定时器触发：无人处理则自动归档（终态），流程结束；
//   - delete 更新：流程内删行后自行结束（decommission）——不存在
//     "外部取消 + 删行" 的两步组合，孤儿实例在构造上不可能出现。
//
// Update 提供请求-响应语义：Validator 同步拒绝非法请求（重复激活、
// 过后续期），Handler 落库后把新快照直接返回给发起方——handler 因此
// 无需轮询数据库等待异步落库。发起侧经 UpdateWithStart 按确定性 ID
// 懒拉起实例（draft 无承诺、无实例）。
//
// 协程纪律（Go SDK）：update handler 在自己的协程上运行，阻塞调用
// （activity）必须用 handler 收到的 wctx，不能借用主协程的 ctx；
// 定时器的重建只发生在主循环（timerDirty 标志传递意图）；跨协程唤醒
// 用带缓冲通道的非阻塞 SendAsync（可合并，不丢状态）。
//
// stage 与 expires_at 只经本流程的 activity 落库——HTTP 层无法绕过这里直接改。
//
// 重启自愈：start 参数携带数据库快照（当前阶段 + 到期时间）。active 且到期
// 时间已过 → 立即补归档，不宽限——过期条目必然归档这一不变量由此兜底。
//
// 历史治理：主循环在 Continue-As-New 建议出现时换新执行，状态随入参续传。
func ItemWorkflow(ctx workflow.Context, itemID int64, initialStage, initialExpiresAt string) (string, error) {
	stage := initialStage
	if !itemport.IsValidStage(stage) {
		stage = itemport.StageDraft
	}

	var deadline time.Time
	if initialExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, initialExpiresAt)
		if err != nil {
			workflow.GetLogger(ctx).Warn("invalid initial expires_at, ignored",
				"item_id", itemID, "expires_at", initialExpiresAt)
		} else {
			deadline = t
		}
	}

	// 流程视角的查询：不开数据库也能看到阶段与到期时间。
	_ = workflow.SetQueryHandler(ctx, contract.QueryCurrentStage, func() (string, error) {
		return stage, nil
	})
	_ = workflow.SetQueryHandler(ctx, contract.QueryExpiresAt, func() (string, error) {
		if deadline.IsZero() {
			return "", nil
		}
		return deadline.UTC().Format(time.RFC3339), nil
	})

	// apply 把阶段与到期时间落库，返回新快照。阻塞调用使用调用方协程的
	// 上下文（主循环传主 ctx，update handler 传它的 wctx）。
	// 错误直接返回给 update 发起方（用户可重试）；行已不存在时额外置
	// fatal——流程随后退出，不留孤儿 Running 实例。
	apply := func(wctx workflow.Context, stage, expiresAt string) (itemport.Item, error) {
		var out itemport.ApplyItemLifecycleOutput
		actx := resourceActivityOpts(wctx)
		err := workflow.ExecuteActivity(actx, itemActs.ApplyItemLifecycle,
			itemport.ApplyItemLifecycleInput{ID: itemID, Stage: stage, ExpiresAt: expiresAt},
		).Get(wctx, &out)
		return out.Item, err
	}
	var fatal error

	// 到期定时器：arm 先取消旧定时器再起新的。只在主协程调用——
	// update handler 通过 timerDirty 把"定时器需要重置"的意图传给主循环。
	var (
		timerFuture workflow.Future
		cancelTimer func()
		timerDirty  bool
	)
	arm := func(d time.Time) {
		if cancelTimer != nil {
			cancelTimer()
		}
		var tctx workflow.Context
		tctx, cancelTimer = workflow.WithCancel(ctx)
		timerFuture = workflow.NewTimer(tctx, d.Sub(workflow.Now(ctx)))
	}
	defer func() {
		if cancelTimer != nil {
			cancelTimer()
		}
	}()

	// wakeup 唤醒主循环：容量 1 的带缓冲通道 + 非阻塞 SendAsync。
	// 多次唤醒可安全合并——状态都在变量里，主循环每次醒来全量重估。
	wakeup := workflow.NewBufferedChannel(ctx, 1)
	wake := func() { wakeup.SendAsync(nil) }

	// archiveNow 归档并终结流程（仅主协程调用）。到期路径的失败一律上抛
	// （流程 Failed，Temporal UI 可见）：到期归档绝不能静默跳过。行已不
	// 存在（与删除更新竞态 / 行被外部删掉）则视为已终结，正常退出。
	archiveNow := func() error {
		item, err := apply(ctx, itemport.StageArchived, "")
		if err == nil {
			stage = item.Stage
			if cancelTimer != nil {
				cancelTimer()
			}
			return nil
		}
		if isNotFound(err) {
			stage = itemport.StageArchived
			return nil
		}
		return fmt.Errorf("archive item %d: %w", itemID, err)
	}

	// deleteRow 是删除的 decommission 路径：流程内删行，随后主循环退出。
	// 删除失败（非 not_found）返回给发起方——删除必须可见；行已不存在
	// 视为目标已达成（幂等重放），同样正常结束。
	var deleted bool
	deleteRow := func(wctx workflow.Context, reason string) error {
		err := workflow.ExecuteActivity(resourceActivityOpts(wctx), itemActs.DeleteItem,
			itemport.DeleteItemInput{ID: itemID, Reason: reason}).Get(wctx, nil)
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("delete item %d: %w", itemID, err)
		}
		deleted = true
		if cancelTimer != nil {
			cancelTimer()
		}
		wake()
		return nil
	}

	// 生命周期 Update：同步校验（Validator，只读）+ 落库返回新快照（Handler）。
	// 幂等统一规则：目标已达成则返回当前快照，冲突则拒绝。
	// 快照从流程内存重建（stage / expires_at 与库一致——本流程是唯一写者）；
	// title 等资料字段不在流程内，handler 渲染前自行回读全量行。
	snapshot := func() itemport.Item {
		var exp *time.Time
		if !deadline.IsZero() {
			t := deadline.UTC()
			exp = &t
		}
		return itemport.Item{ID: itemID, Stage: stage, ExpiresAt: exp}
	}

	_ = workflow.SetUpdateHandlerWithOptions(ctx, contract.UpdateActivate,
		func(wctx workflow.Context) (itemport.Item, error) {
			if stage == itemport.StageActive {
				return snapshot(), nil // 目标已达成：返回当前快照，不重算到期
			}
			next := workflow.Now(wctx).Add(itemport.DefaultValidity)
			item, err := apply(wctx, itemport.StageActive, next.UTC().Format(time.RFC3339))
			if err != nil {
				if isNotFound(err) {
					fatal = err
					wake()
				}
				return itemport.Item{}, err
			}
			stage = item.Stage
			deadline = next
			timerDirty = true
			wake()
			return item, nil
		},
		workflow.UpdateHandlerOptions{}, // 无拒绝条件
	)

	// rejection 是业务拒绝的错误信封：与 activity 侧 wrapActivityError 同款
	// （NonRetryable + "validation"），跨进程传输后 handler 侧 writeError
	// 能直接取回用户可读消息。
	rejection := func(msg string) error {
		return temporal.NewNonRetryableApplicationError(msg, "validation", nil)
	}

	_ = workflow.SetUpdateHandlerWithOptions(ctx, contract.UpdateRenew,
		func(wctx workflow.Context) (itemport.Item, error) {
			if stage != itemport.StageActive || !deadline.After(workflow.Now(wctx)) {
				return itemport.Item{}, rejection("只有未到期的启用条目才能续期")
			}
			base := deadline
			if base.Before(workflow.Now(wctx)) {
				base = workflow.Now(wctx)
			}
			next := base.Add(itemport.DefaultValidity)
			item, err := apply(wctx, itemport.StageActive, next.UTC().Format(time.RFC3339))
			if err != nil {
				if isNotFound(err) {
					fatal = err
					wake()
				}
				return itemport.Item{}, err
			}
			stage = item.Stage
			deadline = next
			timerDirty = true // 旧定时器作废，主循环重置到新到期时间
			wake()
			return item, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(wctx workflow.Context) error {
				if stage != itemport.StageActive || !deadline.After(workflow.Now(wctx)) {
					return rejection("只有未到期的启用条目才能续期")
				}
				return nil
			},
		})

	_ = workflow.SetUpdateHandlerWithOptions(ctx, contract.UpdateArchive,
		func(wctx workflow.Context) (itemport.Item, error) {
			item, err := apply(wctx, itemport.StageArchived, "")
			if err != nil {
				if isNotFound(err) {
					fatal = err
					wake()
				}
				return itemport.Item{}, err
			}
			stage = item.Stage
			if cancelTimer != nil {
				cancelTimer()
			}
			wake()
			return item, nil
		},
		workflow.UpdateHandlerOptions{},
	)

	_ = workflow.SetUpdateHandlerWithOptions(ctx, contract.UpdateDelete,
		func(wctx workflow.Context, reason string) error {
			return deleteRow(wctx, reason)
		},
		workflow.UpdateHandlerOptions{},
	)

	switch stage {
	case itemport.StageArchived:
		// 历史数据兜底：终态即结束。
		return stage, nil
	case itemport.StageActive:
		if deadline.IsZero() {
			// active 却无到期时间（异常 / 历史数据）：自愈为一个有效期。
			deadline = workflow.Now(ctx).Add(itemport.DefaultValidity)
			workflow.GetLogger(ctx).Warn("active item without expiry, self-healed",
				"item_id", itemID)
		} else if !deadline.After(workflow.Now(ctx)) {
			// 补归档：到期未处理（流程曾被中断后重拉起），不宽限。
			if err := archiveNow(); err != nil {
				return stage, err
			}
			// 伴随启动送达的 update（如 delete）在首个 yield 已有机会执行；
			// 此处等所有已受理 handler 跑完再返回，避免吞掉它们。
			_ = workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) })
			return stage, nil
		}
		arm(deadline)
	case itemport.StageDraft:
		// draft 无承诺：不起定时器，等 update。
	}

	finished := func() bool { return deleted || itemport.IsTerminalStage(stage) }

	for !finished() {
		// 每轮重建 Selector：定时器 future 每次 Select 至多用一次，
		// update 重置定时器后必须换上新的 future。
		selector := workflow.NewSelector(ctx)
		var expired bool

		// 取消分支：workflow 被外部取消（运维操作）时，ctx.Done() 收到值，
		// Select 得以返回，循环退出并以上下文错误关闭流程。
		// 注意 Selector.Select(ctx) 不会自动观察取消——没有这一支，
		// 流程会在取消请求送达后继续阻塞，留下孤儿 Running 实例。
		selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
		})

		// update 完成后的唤醒分支：本身无事可做，Select 返回后
		// 主循环按 timerDirty 重建定时器、重估全部状态。
		selector.AddReceive(wakeup, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
		})

		if timerFuture != nil {
			f := timerFuture
			selector.AddFuture(f, func(fut workflow.Future) {
				// 被取消的定时器以 canceled 错误完成，不算到期。
				if fut.Get(ctx, nil) == nil {
					expired = true
				}
			})
		}

		selector.Select(ctx) // 阻塞直到 update 唤醒 / 到期 / 取消其一

		if fatal != nil {
			return stage, fatal
		}
		if ctx.Err() != nil {
			return stage, ctx.Err()
		}
		// update 刚重置过到期时间：先重建定时器。旧定时器即使已经触发，
		// 其到期语义也被顺延覆盖（下方的当前到期复核兜底）。
		if timerDirty && !finished() {
			arm(deadline)
			timerDirty = false
			expired = false
		}
		if expired && !deadline.After(workflow.Now(ctx)) {
			// 到期无人处理：自动归档（终态）。以当前到期时间复核——
			// 到期后瞬间的续期竞态已被 Validator 拒绝，这里双保险。
			if err := archiveNow(); err != nil {
				return stage, err
			}
		}
		if finished() {
			break
		}

		// Continue-As-New：历史增长到建议阈值时换新执行，快照随入参续传。
		// 两条铁律：只能在主流程（而非 update handler）里做；做之前确认
		// 所有已受理 handler 已跑完（未处理的更新随旧历史丢失）。
		if workflow.GetInfo(ctx).GetContinueAsNewSuggested() && workflow.AllHandlersFinished(ctx) {
			expiry := ""
			if !deadline.IsZero() {
				expiry = deadline.UTC().Format(time.RFC3339)
			}
			return "", workflow.NewContinueAsNewError(ctx, ItemWorkflow, itemID, stage, expiry)
		}
	}
	return stage, nil
}

// isNotFound 判断 activity 是否因目标行不存在而失败
// （activity 侧 wrapActivityError 标记的 not_found 错误信封）。
func isNotFound(err error) bool {
	var ae *temporal.ApplicationError
	return errors.As(err, &ae) && ae.Type() == "not_found"
}
