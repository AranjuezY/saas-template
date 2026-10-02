package handler

import (
	"reflect"
	"testing"

	itemport "github.com/AranjuezY/saas-template/internal/example/resources/item/port"
)

// TestLifecycleOperationsRegistered 守卫"先登记、后实现"：
// 编排入口（Orchestrator）的每个方法都必须出现在 port.Operations——
// 写入、生命周期与副作用类操作不登记就实现，这里直接失败。
func TestLifecycleOperationsRegistered(t *testing.T) {
	inventory := map[string]bool{}
	for _, op := range itemport.Operations {
		if op.Name == "" {
			t.Error("port.Operations 存在空名字条目")
		}
		if inventory[op.Name] {
			t.Errorf("port.Operations 存在重复条目 %q", op.Name)
		}
		inventory[op.Name] = true

		for _, k := range op.Kinds {
			switch k {
			case itemport.OpWrite, itemport.OpLifecycle, itemport.OpEffect:
			default:
				t.Errorf("操作 %q 使用未知类别 %q", op.Name, k)
			}
		}
	}

	orch := reflect.TypeOf((*Orchestrator)(nil)).Elem()
	for i := 0; i < orch.NumMethod(); i++ {
		name := orch.Method(i).Name
		if !inventory[name] {
			t.Errorf("Orchestrator.%s 未登记于 port.Operations——写入/生命周期/副作用操作必须先登记后实现", name)
		}
	}
}
