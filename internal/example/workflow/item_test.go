package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/AranjuezY/saas-template/internal/example/contract"
	itemport "github.com/AranjuezY/saas-template/internal/example/resources/item/port"
)

// lifecycleEnv 装配被测流程：用记录每次落库参数的假 activity 替换
// ApplyItemLifecycle 与 DeleteItem（无需数据库 / Temporal Server，
// 虚拟时钟自动推进定时器）。
type lifecycleEnv struct {
	env     *testsuite.TestWorkflowEnvironment
	applies *[]itemport.ApplyItemLifecycleInput
	deletes *[]itemport.DeleteItemInput
}

// newLifecycleEnvWithApply 允许注入 apply 的失败行为（错误信封与
// activity 侧 wrapActivityError 一致），用于容错路径测试。
func newLifecycleEnvWithApply(t *testing.T, applyHook func(in itemport.ApplyItemLifecycleInput) error) *lifecycleEnv {
	t.Helper()

	env := (&testsuite.WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ItemWorkflow)

	applies := &[]itemport.ApplyItemLifecycleInput{}
	env.RegisterActivityWithOptions(
		func(_ context.Context, in itemport.ApplyItemLifecycleInput) (itemport.ApplyItemLifecycleOutput, error) {
			if applyHook != nil {
				if err := applyHook(in); err != nil {
					return itemport.ApplyItemLifecycleOutput{}, err
				}
			}
			*applies = append(*applies, in)
			return itemport.ApplyItemLifecycleOutput{Item: itemport.Item{ID: in.ID, Stage: in.Stage}}, nil
		},
		activity.RegisterOptions{Name: "ApplyItemLifecycle"},
	)

	deletes := &[]itemport.DeleteItemInput{}
	env.RegisterActivityWithOptions(
		func(_ context.Context, in itemport.DeleteItemInput) error {
			*deletes = append(*deletes, in)
			return nil
		},
		activity.RegisterOptions{Name: "DeleteItem"},
	)

	return &lifecycleEnv{env: env, applies: applies, deletes: deletes}
}

func newLifecycleEnv(t *testing.T) *lifecycleEnv {
	return newLifecycleEnvWithApply(t, nil)
}

// updateResult 捕获一次 update 的拒绝 / 完成结果。
type updateResult struct {
	rejected  error
	completed error
}

// atUpdate 在虚拟时间 t（回调延迟）发起一次生命周期 update。
// 延迟不用 0：update 需要 workflow 已注册 handler（区别于可缓冲的信号）。
func (e *lifecycleEnv) atUpdate(t time.Duration, name string, args ...any) *updateResult {
	return e.atUpdateWithID(t, name, "", args...)
}

// atUpdateWithID 发起携带显式 Update ID 的 update（验证去重语义）。
func (e *lifecycleEnv) atUpdateWithID(t time.Duration, name, updateID string, args ...any) *updateResult {
	res := &updateResult{}
	e.env.RegisterDelayedCallback(func() {
		e.env.UpdateWorkflow(name, updateID, &testsuite.TestUpdateCallback{
			OnReject:   func(err error) { res.rejected = err },
			OnAccept:   func() {},
			OnComplete: func(_ any, err error) { res.completed = err },
		}, args...)
	}, t)
	return res
}

// parseExpiry 解析落库的 RFC3339 到期时间。
func parseExpiry(t *testing.T, in itemport.ApplyItemLifecycleInput) time.Time {
	t.Helper()
	exp, err := time.Parse(time.RFC3339, in.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at %q: %v", in.ExpiresAt, err)
	}
	return exp
}

// TestActivateThenAutoArchive 激活后无人处理，到期定时器触发自动归档。
func TestActivateThenAutoArchive(t *testing.T) {
	e := newLifecycleEnv(t)
	start := e.env.Now()

	act := e.atUpdate(time.Second, contract.UpdateActivate)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	if !e.env.IsWorkflowCompleted() {
		t.Fatal("workflow 未结束")
	}
	if act.rejected != nil || act.completed != nil {
		t.Fatalf("activate 被拒/失败: reject=%v complete=%v", act.rejected, act.completed)
	}
	var result string
	if err := e.env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if result != itemport.StageArchived {
		t.Fatalf("result = %q, want archived", result)
	}

	if len(*e.applies) != 2 {
		t.Fatalf("落库次数 = %d, want 2: %+v", len(*e.applies), *e.applies)
	}
	if (*e.applies)[0].Stage != itemport.StageActive || (*e.applies)[1].Stage != itemport.StageArchived {
		t.Fatalf("落库序列 = %+v, want [active archived]", *e.applies)
	}

	// 激活落库的到期时间 ≈ 激活时刻（t=1s） + DefaultValidity。
	exp := parseExpiry(t, (*e.applies)[0])
	if d := exp.Sub(start); d < itemport.DefaultValidity || d > itemport.DefaultValidity+2*time.Second {
		t.Errorf("expires_at 距开始 %v, want ≈ %v", d, itemport.DefaultValidity)
	}
	// 归档只能发生在到期时刻或之后（定时器驱动）。
	if e.env.Now().Before(exp) {
		t.Errorf("完成时刻 %v 早于到期 %v：定时器未生效", e.env.Now(), exp)
	}
}

// TestRenewExtendsDeadline 续期顺延一个周期：第 6 天续期，到期从第 7 天
// 顺延到第 14 天，定时器重置——第 7 天不应归档。
func TestRenewExtendsDeadline(t *testing.T) {
	e := newLifecycleEnv(t)

	e.atUpdate(time.Second, contract.UpdateActivate)
	e.atUpdate(6*24*time.Hour, contract.UpdateRenew)
	// 第 6 天 + 23 小时：续期已生效、原定时器未到——此刻流程仍 active，
	// 且查询到的到期时间是顺延后的第 14 天。
	e.env.RegisterDelayedCallback(func() {
		var stage, expiry string
		if v, err := e.env.QueryWorkflow(contract.QueryCurrentStage); err != nil {
			t.Errorf("query current_stage: %v", err)
		} else if err := v.Get(&stage); err != nil {
			t.Errorf("decode current_stage: %v", err)
		}
		if stage != itemport.StageActive {
			t.Errorf("续期后 stage = %q, want active", stage)
		}
		if v, err := e.env.QueryWorkflow(contract.QueryExpiresAt); err != nil {
			t.Errorf("query expires_at: %v", err)
		} else if err := v.Get(&expiry); err != nil {
			t.Errorf("decode expires_at: %v", err)
		}
		if len(*e.applies) < 2 {
			t.Errorf("续期尚未落库: %+v", *e.applies)
		} else if expiry != (*e.applies)[1].ExpiresAt {
			t.Errorf("查询到期 %q != 续期落库 %q", expiry, (*e.applies)[1].ExpiresAt)
		}
	}, 6*24*time.Hour+23*time.Hour)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	var result string
	_ = e.env.GetWorkflowResult(&result)
	if result != itemport.StageArchived {
		t.Fatalf("result = %q, want archived", result)
	}
	if len(*e.applies) != 3 {
		t.Fatalf("落库次数 = %d, want 3: %+v", len(*e.applies), *e.applies)
	}
	first, second := parseExpiry(t, (*e.applies)[0]), parseExpiry(t, (*e.applies)[1])
	// 顺延语义：新到期 = 原到期 + 7 天。
	if d := second.Sub(first); d != itemport.DefaultValidity {
		t.Errorf("续期后到期 - 原到期 = %v, want %v", d, itemport.DefaultValidity)
	}
	// 完成时刻应不早于续期后到期（定时器确实被重置，而不是第 7 天就归档）。
	if e.env.Now().Before(second) {
		t.Errorf("完成时刻 %v 早于续期后到期 %v：定时器未重置", e.env.Now(), second)
	}
}

// TestEarlyArchive 提前归档：不等定时器，立即进入终态。
func TestEarlyArchive(t *testing.T) {
	e := newLifecycleEnv(t)
	start := e.env.Now()

	e.atUpdate(time.Second, contract.UpdateActivate)
	arc := e.atUpdate(2*24*time.Hour, contract.UpdateArchive)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	var result string
	_ = e.env.GetWorkflowResult(&result)
	if result != itemport.StageArchived {
		t.Fatalf("result = %q, want archived", result)
	}
	if arc.rejected != nil || arc.completed != nil {
		t.Errorf("archive 被拒/失败: reject=%v complete=%v", arc.rejected, arc.completed)
	}
	if len(*e.applies) != 2 || (*e.applies)[1].Stage != itemport.StageArchived {
		t.Fatalf("落库序列 = %+v, want [active archived]", *e.applies)
	}
	// 提前归档发生在第 2 天，远早于第 7 天到期。
	if e.env.Now().Sub(start) >= itemport.DefaultValidity {
		t.Errorf("完成时刻 %v 不应等到到期", e.env.Now())
	}
}

// TestDuplicateActivateReturnsSnapshot 重复激活幂等：目标已达成返回当前
// 快照（不重算到期、不落库、不重起定时器）。
func TestDuplicateActivateReturnsSnapshot(t *testing.T) {
	e := newLifecycleEnv(t)

	e.atUpdate(time.Second, contract.UpdateActivate)
	dup := e.atUpdate(24*time.Hour, contract.UpdateActivate)
	e.atUpdate(2*24*time.Hour, contract.UpdateArchive)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	if dup.rejected != nil {
		t.Errorf("重复 activate 应返回快照而非拒绝: %v", dup.rejected)
	}
	if dup.completed != nil {
		t.Errorf("重复 activate 应成功: %v", dup.completed)
	}
	if len(*e.applies) != 2 || (*e.applies)[0].Stage != itemport.StageActive || (*e.applies)[1].Stage != itemport.StageArchived {
		t.Fatalf("落库序列 = %+v, want [active archived]（重复激活不落库）", *e.applies)
	}
}

// TestRenewRejectedOnDraft draft 无到期可续：Validator 直接拒绝。
func TestRenewRejectedOnDraft(t *testing.T) {
	e := newLifecycleEnv(t)

	renew := e.atUpdate(time.Second, contract.UpdateRenew)
	e.atUpdate(2*24*time.Hour, contract.UpdateArchive)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	if renew.rejected == nil {
		t.Error("draft 上续期应被拒绝")
	}
	if len(*e.applies) != 1 { // 仅收尾的 archive
		t.Fatalf("落库序列 = %+v, want 仅 [archived]", *e.applies)
	}
}

// TestDeleteUpdate 删除是 decommission 更新：流程内删行后
// 自行结束，不走归档，也不需要外部取消。
func TestDeleteUpdate(t *testing.T) {
	e := newLifecycleEnv(t)

	e.atUpdate(time.Second, contract.UpdateActivate)
	del := e.atUpdate(24*time.Hour, contract.UpdateDelete, "from test")
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	if !e.env.IsWorkflowCompleted() {
		t.Fatal("delete 后 workflow 未结束")
	}
	var result string
	if err := e.env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("delete 应正常完成: %v", err)
	}
	if del.rejected != nil || del.completed != nil {
		t.Errorf("delete 被拒/失败: reject=%v complete=%v", del.rejected, del.completed)
	}
	if len(*e.deletes) != 1 || (*e.deletes)[0].ID != 1 || (*e.deletes)[0].Reason != "from test" {
		t.Fatalf("删除落库 = %+v, want 1 次（reason=from test）", *e.deletes)
	}
	// 删除不走归档：只有 activate 一次生命周期落库。
	if len(*e.applies) != 1 || (*e.applies)[0].Stage != itemport.StageActive {
		t.Fatalf("落库序列 = %+v, want 仅 [active]（删除不归档）", *e.applies)
	}
}

// TestUpdateIDDedup 同一 Update ID 的重放在实例内被去重：
// 第二次拿到缓存结果，不再顺延——双击 / 同输入重试不会重复续期。
func TestUpdateIDDedup(t *testing.T) {
	e := newLifecycleEnv(t)

	e.atUpdate(time.Second, contract.UpdateActivate)
	first := e.atUpdateWithID(2*24*time.Hour, contract.UpdateRenew, "renew-1-active-D1")
	second := e.atUpdateWithID(3*24*time.Hour, contract.UpdateRenew, "renew-1-active-D1")
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	if first.rejected != nil || first.completed != nil {
		t.Errorf("首次续期失败: reject=%v complete=%v", first.rejected, first.completed)
	}
	if second.rejected != nil || second.completed != nil {
		t.Errorf("重放应拿缓存结果而非报错: reject=%v complete=%v", second.rejected, second.completed)
	}
	// 仅一次续期落库：[activate, renew, 到期归档]
	if len(*e.applies) != 3 {
		t.Fatalf("落库次数 = %d, want 3（重放不落库）: %+v", len(*e.applies), *e.applies)
	}
	if d := parseExpiry(t, (*e.applies)[1]).Sub(parseExpiry(t, (*e.applies)[0])); d != itemport.DefaultValidity {
		t.Errorf("续期顺延 = %v, want 恰一个 %v（重放未重复顺延）", d, itemport.DefaultValidity)
	}
}

// TestCatchUpArchiveOnRestart 重启自愈：active 且到期时间已过的快照启动，
// 立即补归档，不宽限。
func TestCatchUpArchiveOnRestart(t *testing.T) {
	e := newLifecycleEnv(t)
	start := e.env.Now()

	past := start.Add(-time.Hour).UTC().Format(time.RFC3339)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageActive, past)

	var result string
	_ = e.env.GetWorkflowResult(&result)
	if result != itemport.StageArchived {
		t.Fatalf("result = %q, want archived（补归档）", result)
	}
	if len(*e.applies) != 1 || (*e.applies)[0].Stage != itemport.StageArchived {
		t.Fatalf("落库序列 = %+v, want [archived]", *e.applies)
	}
	if e.env.Now().Sub(start) > time.Minute {
		t.Errorf("补归档应立即发生，完成时刻 %v", e.env.Now())
	}
}

// TestCatchUpArchiveRowVanished 补归档遇 not_found（行已被删除更新 /
// 外部删掉）：视为已终结，流程正常结束而不是 Failed。
func TestCatchUpArchiveRowVanished(t *testing.T) {
	e := newLifecycleEnvWithApply(t, func(in itemport.ApplyItemLifecycleInput) error {
		if in.Stage == itemport.StageArchived {
			return temporal.NewNonRetryableApplicationError("该条目已不存在", "not_found", nil)
		}
		return nil
	})
	start := e.env.Now()

	past := start.Add(-time.Hour).UTC().Format(time.RFC3339)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageActive, past)

	var result string
	if err := e.env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("行已不存在应正常结束: %v", err)
	}
	if result != itemport.StageArchived {
		t.Errorf("result = %q, want archived（视为已终结）", result)
	}
}

// TestContinueAsNewCarriesSnapshot Continue-As-New：建议出现时换新执行，
// 快照（阶段 + 到期时间）随入参完整续传。
// 注：Go SDK 测试环境不透明链接根 workflow 的 CAN（仅子 workflow 会重跑），
// 因此这里断言 CAN 错误本身携带的入参，而非链条最终结果。
func TestContinueAsNewCarriesSnapshot(t *testing.T) {
	e := newLifecycleEnv(t)
	start := e.env.Now()

	// 模拟历史达到建议阈值：事件处理完即触发 Continue-As-New 检查。
	e.env.SetContinueAsNewSuggested(true)

	e.atUpdate(time.Second, contract.UpdateActivate)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	var result string
	err := e.env.GetWorkflowResult(&result)
	if err == nil {
		t.Fatal("建议出现时应 Continue-As-New 而非继续等待")
	}
	var canErr *workflow.ContinueAsNewError
	if !errors.As(err, &canErr) {
		t.Fatalf("err = %v, want ContinueAsNewError", err)
	}

	// 续传入参 = (itemID, stage, expires_at)：快照必须完整，
	// 新执行才能重建定时器与补归档语义。
	var (
		itemID    int64
		stage     string
		expiresAt string
	)
	if err := converter.GetDefaultDataConverter().FromPayloads(canErr.Input, &itemID, &stage, &expiresAt); err != nil {
		t.Fatalf("decode CAN input: %v", err)
	}
	if itemID != 1 || stage != itemport.StageActive {
		t.Errorf("CAN 入参 = (%d, %q), want (1, active)", itemID, stage)
	}
	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("CAN expires_at %q 不可解析: %v", expiresAt, err)
	}
	if d := exp.Sub(start); d < itemport.DefaultValidity || d > itemport.DefaultValidity+2*time.Second {
		t.Errorf("CAN expires_at 距开始 %v, want ≈ %v（到期时间跨执行续传）", d, itemport.DefaultValidity)
	}
}

// TestCancelEndsWorkflow 外部取消（运维操作）：ctx.Done 分支
// 唤醒等待循环，流程以取消错误结束，不留孤儿 Running 实例。
func TestCancelEndsWorkflow(t *testing.T) {
	e := newLifecycleEnv(t)

	e.atUpdate(time.Second, contract.UpdateActivate)
	e.env.RegisterDelayedCallback(func() {
		e.env.CancelWorkflow()
	}, 24*time.Hour)
	e.env.ExecuteWorkflow(ItemWorkflow, int64(1), itemport.StageDraft, "")

	if !e.env.IsWorkflowCompleted() {
		t.Fatal("取消后 workflow 未结束（孤儿 Running）")
	}
	var result string
	err := e.env.GetWorkflowResult(&result)
	if err == nil || !temporal.IsCanceledError(err) {
		t.Fatalf("取消后 err = %v, want canceled", err)
	}
}
