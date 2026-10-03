package main

// 模板资产与渲染视图模型。
//
// 模板只做最简单的取值，所有派生数据（PascalCase、Go 字符串字面量、
// 方法签名）都在视图模型里预先算好——这既是模板可读性的要求，
// 也是确定性输出的要求：同一规格必须产出字节级相同的代码。

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"strings"
	"text/template"

	"strconv"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// render 用命名模板渲染视图模型，返回 gofmt 后的源码。
// 输出必须是合法 Go 代码（templ/YAML 模板除外，调用方单独处理）。
func render(name string, data any, gofmt bool) ([]byte, error) {
	tmpl, err := template.New(name).ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("解析模板 %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, fmt.Errorf("渲染模板 %s: %w", name, err)
	}
	src := buf.Bytes()
	if gofmt {
		formatted, err := format.Source(src)
		if err != nil {
			return nil, fmt.Errorf("模板 %s 产出非法 Go 代码: %w\n--- 产出 ---\n%s", name, err, src)
		}
		src = formatted
	}
	return src, nil
}

// 常见缩略词保持全大写，避免 GoName 生成 ItemId 这类不符合惯例的名字。
var initialisms = map[string]string{
	"id":   "ID",
	"url":  "URL",
	"api":  "API",
	"http": "HTTP",
	"sms":  "SMS",
}

// goName 把 snake_case 字段名转成导出的 Go 字段名。
func goName(snake string) string {
	parts := strings.Split(snake, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if up, ok := initialisms[p]; ok {
			b.WriteString(up)
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// modelType 由资源名得到快照类型名（item -> Item）。
func modelType(name string) string { return strings.ToUpper(name[:1]) + name[1:] }

// VM 是全部模板共享的视图模型。
type VM struct {
	Module string // go.mod 里的 module 路径
	Domain string
	Name   string

	NameGo   string // 快照类型名（Item）
	Table    string
	Type     string
	TypeDesc string

	// 类型决定的目录/生成物开关
	HasStore     bool // port/store 子包 + 约定读操作
	HasActivity  bool // activity/activity_gen.go（仅绑定型）
	HasView      bool // view/view.templ
	Orchestrated bool // handler 持有 Orchestrator

	Workflow string // 资源级"对 workflow 的影响"

	Ops          []OpVM // 全部登记操作（规格顺序）
	StoreOps     []OpVM // write/sideeffect：进入 port.Ops
	ActivityOps  []OpVM // write/sideeffect 且未标 activity:false：生成 activity 包装
	LifecycleOps []OpVM // lifecycle：进入 Orchestrator

	NeedTime bool // 任一字段使用 time.Time（决定 ops_gen 是否 import time）
}

// OpVM 是单个操作的视图模型。
type OpVM struct {
	Name string

	Kinds    []string
	KindTags string // 注释用："write + lifecycle"
	KindExpr string // 代码用：[]OpKind{OpWrite, OpLifecycle}

	Workflow    string // 原文（注释用）
	Idempotency string
	Audit       string

	WorkflowQ    string // Go 字符串字面量（表用）
	IdempotencyQ string
	AuditQ       string
	NameQ        string

	Input  []FieldVM
	Output []FieldVM

	InputType  string // CreateItemInput
	OutputType string // CreateItemOutput
	HasOutput  bool

	// SigPort 在 port 包内使用（无前缀）；SigQualified 在其它包使用（port. 前缀）。
	SigPort      string
	SigQualified string
}

// FieldVM 是输入/输出字段的视图模型。
type FieldVM struct {
	GoName   string
	JSONName string
	Type     string
}

// BuildVM 从规格构造视图模型。
func BuildVM(module string, s *Spec) VM {
	r := s.Resource
	vm := VM{
		Module:       module,
		Domain:       r.Domain,
		Name:         r.Name,
		NameGo:       modelType(r.Name),
		Table:        r.Table,
		Type:         r.Type,
		TypeDesc:     ResourceTypes[r.Type],
		HasStore:     r.StoreBearing(),
		HasActivity:  r.HasActivity(),
		HasView:      r.HasView(),
		Orchestrated: r.Orchestrated(),
		Workflow:     r.Workflow,
	}

	for _, op := range s.Operations {
		o := buildOpVM(op)
		vm.Ops = append(vm.Ops, o)
		if op.IsStoreOp() {
			vm.StoreOps = append(vm.StoreOps, o)
		}
		if op.HasActivityWrapper() {
			vm.ActivityOps = append(vm.ActivityOps, o)
		}
		if op.IsLifecycleOp() {
			vm.LifecycleOps = append(vm.LifecycleOps, o)
		}
	}
	vm.NeedTime = specNeedsTime(s)
	return vm
}

func buildOpVM(op Op) OpVM {
	kinds := append([]string(nil), op.Kinds...)

	var constNames []string
	for _, k := range kinds {
		constNames = append(constNames, kindConstName(k))
	}

	o := OpVM{
		Name:         op.Name,
		Kinds:        kinds,
		KindTags:     strings.Join(kinds, " + "),
		KindExpr:     "[]OpKind{" + strings.Join(constNames, ", ") + "}",
		Workflow:     op.Workflow,
		Idempotency:  op.Idempotency,
		Audit:        op.Audit,
		WorkflowQ:    strconv.Quote(op.Workflow),
		IdempotencyQ: strconv.Quote(op.Idempotency),
		AuditQ:       strconv.Quote(op.Audit),
		NameQ:        strconv.Quote(op.Name),
		InputType:    op.Name + "Input",
		OutputType:   op.Name + "Output",
		HasOutput:    len(op.Output) > 0,
		Input:        buildFieldVM(op.Input),
		Output:       buildFieldVM(op.Output),
	}

	// 签名：有输出 -> (out, error)；无输出 -> error。
	if o.HasOutput {
		o.SigPort = fmt.Sprintf("%s(ctx context.Context, in %s) (%s, error)", o.Name, o.InputType, o.OutputType)
		o.SigQualified = fmt.Sprintf("%s(ctx context.Context, in port.%s) (port.%s, error)", o.Name, o.InputType, o.OutputType)
	} else {
		o.SigPort = fmt.Sprintf("%s(ctx context.Context, in %s) error", o.Name, o.InputType)
		o.SigQualified = fmt.Sprintf("%s(ctx context.Context, in port.%s) error", o.Name, o.InputType)
	}
	return o
}

func buildFieldVM(fields []Field) []FieldVM {
	out := make([]FieldVM, 0, len(fields))
	for _, f := range fields {
		out = append(out, FieldVM{GoName: goName(f.Name), JSONName: f.Name, Type: f.Type})
	}
	return out
}

func specNeedsTime(s *Spec) bool {
	for _, op := range s.Operations {
		for _, f := range append(op.Input, op.Output...) {
			if strings.Contains(f.Type, "time.Time") {
				return true
			}
		}
	}
	return false
}

func kindConstName(kind string) string {
	switch kind {
	case KindWrite:
		return "OpWrite"
	case KindLifecycle:
		return "OpLifecycle"
	case KindSideEffect:
		return "OpSideEffect"
	}
	return "OpKind(" + strconv.Quote(kind) + ")"
}
