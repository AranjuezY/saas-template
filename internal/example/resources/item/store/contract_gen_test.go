// 契约测试 —— resgen 仅首次生成本文件，之后人工维护（不会被重新生成覆盖）。
// 断言已按规格（resgen.yaml）的幂等 / 审计规则填充；规格新增操作时参照补充。
package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/AranjuezY/saas-template/internal/example/resources/item/port"
	"github.com/AranjuezY/saas-template/internal/shared/db"
)

// newContractStore 为契约测试构造被测实现（临时库 + 生成的操作接口）。
func newContractStore(t *testing.T) port.Ops {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "contract.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database)
}

func TestContractCreateItem(t *testing.T) {
	st := newContractStore(t)
	ctx := context.Background()

	out, err := st.CreateItem(ctx, port.CreateItemInput{Item: port.Item{Title: " contract "}})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// 审计规则：created_at / updated_at 字段留存。
	if out.Item.ID == 0 || out.Item.CreatedAt.IsZero() || out.Item.UpdatedAt.IsZero() {
		t.Errorf("审计字段缺失: %+v", out.Item)
	}
	if out.Item.Title != "contract" {
		t.Errorf("title 未规范化: %q", out.Item.Title)
	}

	// 幂等规则：落库本身产生新 ID，重复执行表现为新增而非覆盖——
	// store 层不做去重，重复执行的收敛由 workflow 启动语义（持久化启动）保证。
	dup, err := st.CreateItem(ctx, port.CreateItemInput{Item: port.Item{Title: "contract"}})
	if err != nil {
		t.Fatalf("重复 CreateItem: %v", err)
	}
	if dup.Item.ID == out.Item.ID {
		t.Errorf("重复落库复用了 ID %d，应新增记录", out.Item.ID)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("记录数 = %d, want 2（重复执行表现为新增）", len(list))
	}
}

func TestContractUpdateItemStage(t *testing.T) {
	st := newContractStore(t)
	ctx := context.Background()

	created, err := st.CreateItem(ctx, port.CreateItemInput{Item: port.Item{Title: "t"}})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	updated, err := st.UpdateItemStage(ctx, port.UpdateItemStageInput{ID: created.Item.ID, Stage: port.StageActive})
	if err != nil {
		t.Fatalf("UpdateItemStage: %v", err)
	}
	if updated.Item.Stage != port.StageActive {
		t.Errorf("stage = %q, want active", updated.Item.Stage)
	}

	// 幂等规则：UPDATE 到相同值幂等——重试不报错、事实不变。
	same, err := st.UpdateItemStage(ctx, port.UpdateItemStageInput{ID: created.Item.ID, Stage: port.StageActive})
	if err != nil {
		t.Fatalf("同值重放 UpdateItemStage: %v", err)
	}
	if same.Item.Stage != port.StageActive {
		t.Errorf("同值重放后 stage = %q, want active", same.Item.Stage)
	}
	if same.Item.UpdatedAt.Before(updated.Item.UpdatedAt) {
		t.Error("同值重放不应使 updated_at 倒退")
	}

	// 幂等规则：目标行不存在返回 ErrNotFound，重试不重复生效。
	_, err = st.UpdateItemStage(ctx, port.UpdateItemStageInput{ID: 424242, Stage: port.StageActive})
	if !errors.Is(err, port.ErrNotFound) {
		t.Errorf("缺失目标 err = %v, want ErrNotFound", err)
	}

	// 审计规则：updated_at 字段随阶段更新推进（秒级精度下断言"不早于"）。
	if same.Item.UpdatedAt.Before(created.Item.UpdatedAt) {
		t.Error("阶段更新后 updated_at 不应早于 created_at")
	}
}

func TestContractDeleteItem(t *testing.T) {
	st := newContractStore(t)
	ctx := context.Background()

	created, err := st.CreateItem(ctx, port.CreateItemInput{Item: port.Item{Title: "t"}})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// 审计规则：RowsAffected 检查——删除已有记录成功，缺失记录给出明确哨兵。
	if err := st.DeleteItem(ctx, port.DeleteItemInput{ID: created.Item.ID, Reason: "contract test"}); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	// 幂等规则：二次删除返回 ErrNotFound，不重复生效。
	err = st.DeleteItem(ctx, port.DeleteItemInput{ID: created.Item.ID, Reason: "contract test"})
	if !errors.Is(err, port.ErrNotFound) {
		t.Errorf("二次删除 err = %v, want ErrNotFound", err)
	}

	if _, err := st.Get(ctx, created.Item.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("删除后 Get err = %v, want ErrNotFound", err)
	}
}
