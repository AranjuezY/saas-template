// Command resgen 是 saas-template 的资源生成器（text/template 实现）。
//
// 规格（资源根目录的 resgen.yaml）是唯一事实源：
//
//	resgen resource add <域>/<资源> --type <类型>   只建骨架与空规格
//	（人工填写规格：每个操作的 Kind、Workflow 影响、幂等规则、审计规则）
//	go generate ./...                              从规格再生代码契约
//
// 生成物：操作接口与输入输出类型（port/ops_gen.go）、操作描述表、
// Activity 包装（仅绑定型）、契约测试骨架（仅首次生成，初始为红）。
// 同一规格产出字节级相同的代码；CI 用 git diff 校验生成物与规格同步。
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "resgen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "resource":
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return runResourceCmd(args[1:], cwd)
	case "generate":
		if len(args) > 2 {
			return fmt.Errorf("generate 只接受一个可选的资源目录参数")
		}
		dir := "."
		if len(args) == 2 {
			dir = args[1]
		}
		return runGenerate(dir)
	default:
		return fmt.Errorf("未知子命令 %q", args[0])
	}
}

func runResourceCmd(args []string, cwd string) error {
	if len(args) < 2 || args[0] != "add" {
		return fmt.Errorf("用法: resgen resource add <域>/<资源> [--type bound|capability|reference|trigger|plain]")
	}

	fs := flag.NewFlagSet("resource add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	typ := fs.String("type", TypePlain, "资源类型（默认 plain：多数资源是纯维护，最严格的类型要显式选择）")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("多余参数: %v", fs.Args())
	}
	return runResourceAdd(args[1], *typ, cwd)
}

func usage() error {
	return fmt.Errorf(`用法:
  resgen resource add <域>/<资源> [--type %s]
  resgen generate [资源目录]   # 通常不直接运行，由资源根 doc.go 的 go:generate 驱动`,
		strings.Join(typeNames(), "|"))
}
