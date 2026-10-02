package workflow

import (
	"context"
	"fmt"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/AranjuezY/saas-template/internal/example/contract"
	itemport "github.com/AranjuezY/saas-template/internal/example/resources/item/port"
)

// Client 是 example 域编排的发起侧：把"业务想推进流程"翻译成对
// Temporal Server 的调用（启动 workflow、发信号、取消实例）。
//
// 它只依赖 contract 契约——按 workflow 类型名（字符串）启动，
// 而不是函数引用。发起侧与 workflow 定义在编译期解耦，只共享
// contract 里的契约字符串；名字的同步由注册时引用同一常量保证。
type Client struct {
	c    client.Client
	wait time.Duration // 等待短 workflow 完成的上限
}

// NewClient 包装一个已连接的 Temporal Client。
// waitTimeout 建议 5-15s：短命令 workflow 的总预算（含重试）。
func NewClient(c client.Client, waitTimeout time.Duration) *Client {
	return &Client{c: c, wait: waitTimeout}
}

// CreateItem 落库并拉起该条目的生命周期流程，返回完整记录。
// 调用方会阻塞到短 workflow 完成（生命周期流程继续在后台跑）。
func (cl *Client) CreateItem(ctx context.Context, in itemport.Item) (itemport.Item, error) {
	ctx, cancel := context.WithTimeout(ctx, cl.wait)
	defer cancel()

	run, err := cl.c.ExecuteWorkflow(ctx, cl.startOptions(), contract.WorkflowTypeCreate, in)
	if err != nil {
		return itemport.Item{}, fmt.Errorf("start %s: %w", contract.WorkflowTypeCreate, err)
	}
	var created itemport.Item
	if err := run.Get(ctx, &created); err != nil {
		return itemport.Item{}, fmt.Errorf("run %s: %w", contract.WorkflowTypeCreate, err)
	}
	return created, nil
}

// AdvanceStage 把阶段流转翻译成对流程实例的信号。
//
// currentStage 是数据库里的当前阶段：当目标流程从未启动（历史数据、
// SignalWithStart 自动拉起）时作为新实例的初始阶段。
// 目标为终态时自动改发 archive 信号——activate 不会接受终态值。
func (cl *Client) AdvanceStage(ctx context.Context, id int64, currentStage, nextStage string) error {
	signal, ok := signalForStage(nextStage)
	if !ok {
		return fmt.Errorf("unknown stage %q", nextStage)
	}

	ctx, cancel := context.WithTimeout(ctx, cl.wait)
	defer cancel()

	_, err := cl.c.SignalWithStartWorkflow(ctx,
		contract.ItemWorkflowID(id),
		signal,
		"",
		cl.startOptions(),
		contract.WorkflowTypeItem, id, currentStage,
	)
	if err != nil {
		return fmt.Errorf("signal %s for item %d: %w", signal, id, err)
	}
	// 信号已送达：阶段落库由 workflow 内的 activity 异步完成，
	// handler 随后短轮询数据库直至阶段可见。
	return nil
}

// CancelItem 走合规的取消流程：删除记录 + 取消生命周期实例。
func (cl *Client) CancelItem(ctx context.Context, id int64, reason string) error {
	ctx, cancel := context.WithTimeout(ctx, cl.wait)
	defer cancel()

	run, err := cl.c.ExecuteWorkflow(ctx, cl.startOptions(), contract.WorkflowTypeCancel, id, reason)
	if err != nil {
		return fmt.Errorf("start %s: %w", contract.WorkflowTypeCancel, err)
	}
	if err := run.Get(ctx, nil); err != nil {
		return fmt.Errorf("run %s: %w", contract.WorkflowTypeCancel, err)
	}
	return nil
}

func (cl *Client) startOptions() client.StartWorkflowOptions {
	return client.StartWorkflowOptions{TaskQueue: contract.TaskQueueWorkflows}
}

func signalForStage(stage string) (string, bool) {
	switch stage {
	case itemport.StageArchived:
		return contract.SignalArchive, true
	case itemport.StageDraft, itemport.StageActive:
		return contract.SignalActivate, true
	default:
		return "", false
	}
}
