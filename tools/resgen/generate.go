package main

// generate：读规格 → 渲染模板 → go/format → 确定性落盘。
//
// 由资源根 doc.go 的 go:generate 驱动（cwd 即资源根）。
// 产物全部可重复生成：同一规格产出字节级相同的代码，
// CI 用 `go generate ./... && git diff --exit-code` 保证生成物与规格同步。

import (
	"fmt"
	"os"
	"path/filepath"
)

// runGenerate 处理 `resgen generate [目录]`（目录默认为当前目录）。
func runGenerate(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	specPath := filepath.Join(abs, "resgen.yaml")
	spec, err := LoadSpec(specPath)
	if err != nil {
		return fmt.Errorf("读取规格失败：%w（先运行 resgen resource add）", err)
	}
	if err := spec.Validate(); err != nil {
		return err
	}

	rel, root, module, err := moduleContext(abs)
	if err != nil {
		return err
	}
	// 规格位置必须与 domain/name 声明一致，防止复制粘贴出漂移的规格。
	expected := fmt.Sprintf("internal/%s/resources/%s", spec.Resource.Domain, spec.Resource.Name)
	if rel != expected {
		return fmt.Errorf("规格位于 %s，但 resource.domain/name 声明为 %s——两者必须一致", rel, expected)
	}

	vm := BuildVM(module, spec)
	changed := false

	// 1. port/ops_gen.go：操作接口 + 输入输出类型 + 操作描述表。
	p := filepath.Join(abs, "port", "ops_gen.go")
	c, err := writeGenerated(root, p, "ops_gen_go.tmpl", vm, true)
	if err != nil {
		return err
	}
	changed = changed || c

	// 2. handler/orchestrator_gen.go：存在 lifecycle 操作时生成消费者侧编排接口。
	if len(vm.LifecycleOps) > 0 {
		p := filepath.Join(abs, "handler", "orchestrator_gen.go")
		c, err := writeGenerated(root, p, "orchestrator_gen_go.tmpl", vm, true)
		if err != nil {
			return err
		}
		changed = changed || c
	}

	// 3. activity/activity_gen.go：仅绑定型（判断规则见 AGENTS.md：目录里有 activity 即绑定）。
	if vm.HasActivity {
		p := filepath.Join(abs, "activity", "activity_gen.go")
		c, err := writeGenerated(root, p, "activity_gen_go.tmpl", vm, true)
		if err != nil {
			return err
		}
		changed = changed || c
	}

	// 4. store/contract_gen_test.go：仅首次生成（断言由人工填充，之后不再覆盖）。
	if vm.HasStore && len(vm.StoreOps) > 0 {
		p := filepath.Join(abs, "store", "contract_gen_test.go")
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			src, err := render("contract_gen_test_go.tmpl", vm, true)
			if err != nil {
				return err
			}
			if err := os.WriteFile(p, src, 0o644); err != nil {
				return err
			}
			fmt.Printf("  新建 %s（契约测试骨架，初始为红——填充断言后变绿）\n", mustRel(root, p))
		}
	}

	// 5. .go-arch-lint.yml 标记区：登记资源根组件（子包落通配路径内无需登记）。
	if err := refreshArchLint(root, spec.Resource.Domain, spec.Resource.Name); err != nil {
		return err
	}

	if !changed {
		fmt.Println("  生成物与规格一致，无变更")
	}
	return nil
}

// writeGenerated 渲染并写入一个生成物；内容未变时不落盘（避免 mtime 噪音）。
func writeGenerated(root, path, tmpl string, vm VM, gofmt bool) (bool, error) {
	src, err := render(tmpl, vm, gofmt)
	if err != nil {
		return false, err
	}
	if old, err := os.ReadFile(path); err == nil && string(old) == string(src) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return false, err
	}
	fmt.Printf("  生成 %s\n", mustRel(root, path))
	return true, nil
}
