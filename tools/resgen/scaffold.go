package main

// resource add：按资源类型裁剪目录、写入骨架文件与空规格。
//
// add 不生成代码——规格还是空的，go generate 会拒绝；
// 它只负责让"接下来填规格"这件事有一个明确的结构起点。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runResourceAdd 处理 `resgen resource add <域>/<资源> --type <类型>`。
// cwd 显式传入（命令行是进程 cwd；测试传临时模块目录）。
func runResourceAdd(arg, typ, cwd string) error {
	domain, name, ok := strings.Cut(arg, "/")
	if !ok {
		return fmt.Errorf("参数必须是 <域>/<资源> 形式，如 example/item，得到 %q", arg)
	}
	if !identRe.MatchString(domain) {
		return fmt.Errorf("域名 %q 必须是小写字母开头的标识符", domain)
	}
	if !identRe.MatchString(name) {
		return fmt.Errorf("资源名 %q 必须是小写字母开头的单数标识符", name)
	}
	if _, ok := ResourceTypes[typ]; !ok {
		return fmt.Errorf("--type %q 无效（可选：%s）", typ, strings.Join(typeNames(), " / "))
	}

	_, root, module, err := moduleContext(cwd)
	if err != nil {
		return err
	}

	dir := filepath.Join(root, "internal", domain, "resources", name)
	if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		return fmt.Errorf("%s 已存在且非空，拒绝覆盖", dir)
	}

	r := Resource{Domain: domain, Name: name, Type: typ, Table: name + "s"}
	vm := BuildVM(module, &Spec{Resource: r})

	// 目录裁剪：差别只在选用了哪几个部分（AGENTS.md 第 3 节）。
	dirs := []string{"port", "handler"}
	if vm.HasStore {
		dirs = append(dirs, "store")
	}
	if vm.HasActivity {
		dirs = append(dirs, "activity")
	}
	if vm.HasView {
		dirs = append(dirs, "view")
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}

	type out struct {
		path, tmpl string
		gofmt      bool
	}
	files := []out{
		{filepath.Join(dir, "resgen.yaml"), "spec_yaml.tmpl", false},
		{filepath.Join(dir, "doc.go"), "doc_go.tmpl", true},
		{filepath.Join(dir, "handler", "handler.go"), "handler_go.tmpl", true},
	}
	if vm.HasStore {
		files = append(files,
			out{filepath.Join(dir, "port", name+".go"), "model_go.tmpl", true},
			out{filepath.Join(dir, "store", "store.go"), "store_go.tmpl", true},
		)
	}
	if vm.HasView {
		files = append(files, out{filepath.Join(dir, "view", "view.templ"), "view_templ.tmpl", false})
	}

	for _, f := range files {
		src, err := render(f.tmpl, vm, f.gofmt)
		if err != nil {
			return err
		}
		if err := os.WriteFile(f.path, src, 0o644); err != nil {
			return err
		}
		fmt.Printf("  创建 %s\n", mustRel(root, f.path))
	}

	if err := refreshArchLint(root, domain, name); err != nil {
		return err
	}

	fmt.Printf(`
已创建 %s 资源（类型 %s）。下一步：

  1. 编辑 %s 填写规格：逐操作登记 kind、workflow 影响、幂等规则、审计规则。
     必填字段留空会让生成失败——这是设计出来的关卡。
  2. 仓库根运行 go generate ./...   生成操作接口 / 描述表 / activity 包装 / 契约测试（初始为红）。
  3. 实现 store.go（未导出类型，构造函数返回生成的接口）；填充契约测试断言，变绿才算完成。
  4. 在 cmd/server 装配（store/handler/activity 的构造与注册）。%s
  5. 依次跑：go build ./... && go-arch-lint check && workflowcheck ./... && go test ./...
`, name, typ, mustRel(root, filepath.Join(dir, "resgen.yaml")), viewHint(vm))
	return nil
}

func viewHint(vm VM) string {
	if vm.HasView {
		return "\n  6. view.templ 修改后运行 templ generate。"
	}
	return ""
}

func mustRel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}
