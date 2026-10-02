package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 端到端测试：临时模块内走完 add → 填规格 → generate 全流程，
// 验证目录裁剪、生成物内容与确定性（同规格两次生成字节级一致）。

const filledSpec = `resource:
  domain: demo
  name: gadget
  type: plain
  table: gadgets
  workflow: "无：纯维护资源，不影响任何流程"

operations:
  - name: CreateGadget
    kinds: [write]
    workflow: "无：纯维护资源不影响流程"
    idempotency: "重复提交由唯一键拒绝，不重复生效"
    audit: "created_at 字段"
    input:
      - { name: title, type: string }
    output:
      - { name: gadget, type: Gadget }
  - name: DeleteGadget
    kinds: [write]
    workflow: "无：纯维护资源不影响流程"
    idempotency: "二次删除返回 ErrNotFound"
    audit: "RowsAffected 检查"
    input:
      - { name: id, type: int64 }
`

func TestResourceAddPlainScaffold(t *testing.T) {
	root := newTempModule(t)

	if err := runResourceAdd("demo/gadget", TypePlain, root); err != nil {
		t.Fatalf("resource add: %v", err)
	}

	dir := filepath.Join(root, "internal", "demo", "resources", "gadget")
	for _, f := range []string{
		"resgen.yaml", "doc.go",
		"port/gadget.go", "store/store.go", "handler/handler.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("缺少骨架文件 %s: %v", f, err)
		}
	}
	// plain 类型不应有 activity / view
	for _, d := range []string{"activity", "view"} {
		if _, err := os.Stat(filepath.Join(dir, d)); err == nil {
			t.Errorf("plain 资源不应创建 %s/ 目录", d)
		}
	}

	// doc.go 的 go:generate 指令相对路径固定四级向上
	doc := read(t, filepath.Join(dir, "doc.go"))
	if !strings.Contains(doc, "//go:generate go run ../../../../tools/resgen generate") {
		t.Errorf("doc.go 缺少 go:generate 指令:\n%s", doc)
	}

	// arch-lint 标记区已登记根组件
	arch := read(t, filepath.Join(root, ".go-arch-lint.yml"))
	if !strings.Contains(arch, "resgen-root-demo-gadget: { in: internal/demo/resources/gadget }") {
		t.Errorf(".go-arch-lint.yml 缺少根组件登记:\n%s", arch)
	}

	// 空规格必须被 generate 拒绝（留空即失败的设计关卡）
	if err := runGenerate(dir); err == nil {
		t.Error("空规格的 generate 应当失败")
	}
}

func TestResourceAddBoundScaffold(t *testing.T) {
	root := newTempModule(t)

	if err := runResourceAdd("demo/engine", TypeBound, root); err != nil {
		t.Fatalf("resource add: %v", err)
	}

	dir := filepath.Join(root, "internal", "demo", "resources", "engine")
	for _, d := range []string{"port", "store", "activity", "handler", "view"} {
		if _, err := os.Stat(filepath.Join(dir, d)); err != nil {
			t.Errorf("bound 资源缺少 %s/ 目录: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "view", "view.templ")); err != nil {
		t.Error("bound 资源应有 view.templ 骨架")
	}
}

func TestResourceAddCapabilityScaffold(t *testing.T) {
	root := newTempModule(t)

	if err := runResourceAdd("demo/sms", TypeCapability, root); err != nil {
		t.Fatalf("resource add: %v", err)
	}

	dir := filepath.Join(root, "internal", "demo", "resources", "sms")
	for _, d := range []string{"store", "activity", "view"} {
		if _, err := os.Stat(filepath.Join(dir, d)); err == nil {
			t.Errorf("capability 资源不应创建 %s/ 目录", d)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "port")); err != nil {
		t.Error("capability 资源应有 port/")
	}
	if _, err := os.Stat(filepath.Join(dir, "handler")); err != nil {
		t.Error("capability 资源应有 handler/")
	}
}

func TestGenerateOutputsAndDeterminism(t *testing.T) {
	root := newTempModule(t)
	if err := runResourceAdd("demo/gadget", TypePlain, root); err != nil {
		t.Fatalf("resource add: %v", err)
	}
	dir := filepath.Join(root, "internal", "demo", "resources", "gadget")
	write(t, filepath.Join(dir, "resgen.yaml"), filledSpec)

	if err := runGenerate(dir); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// 生成物齐全
	ops := read(t, filepath.Join(dir, "port", "ops_gen.go"))
	contract := read(t, filepath.Join(dir, "store", "contract_gen_test.go"))

	// 操作描述表：两类操作 + 幂等/审计栏
	for _, want := range []string{
		`Name:           "CreateGadget"`,
		`Name:           "DeleteGadget"`,
		`Idempotency:    "重复提交由唯一键拒绝，不重复生效"`,
		`Audit:          "created_at 字段"`,
		"[]OpKind{OpWrite}",
	} {
		if !strings.Contains(ops, want) {
			t.Errorf("ops_gen.go 缺少 %q:\n%s", want, ops)
		}
	}

	// 操作接口：write 操作 + 约定读操作
	for _, want := range []string{
		"type Ops interface",
		"CreateGadget(ctx context.Context, in CreateGadgetInput) (CreateGadgetOutput, error)",
		"DeleteGadget(ctx context.Context, in DeleteGadgetInput) error",
		"List(ctx context.Context) ([]Gadget, error)",
		"Get(ctx context.Context, id int64) (Gadget, error)",
	} {
		if !strings.Contains(ops, want) {
			t.Errorf("ops_gen.go 缺少 %q:\n%s", want, ops)
		}
	}

	// plain 资源不生成 orchestrator 与 activity
	if _, err := os.Stat(filepath.Join(dir, "handler", "orchestrator_gen.go")); err == nil {
		t.Error("plain 资源（无 lifecycle 操作）不应生成 orchestrator_gen.go")
	}
	if _, err := os.Stat(filepath.Join(dir, "activity", "activity_gen.go")); err == nil {
		t.Error("plain 资源不应生成 activity_gen.go")
	}

	// 契约测试骨架为红：含未填充哨兵
	if !strings.Contains(contract, `t.Error("契约断言未填充：CreateGadget`) {
		t.Errorf("契约测试应为初始失败状态:\n%s", contract)
	}

	// 确定性：再跑一次，所有文件字节不变
	snapshot := map[string]string{}
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			if data, e := os.ReadFile(p); e == nil {
				snapshot[p] = string(data)
			}
		}
		return nil
	})
	if err := runGenerate(dir); err != nil {
		t.Fatalf("二次 generate: %v", err)
	}
	for p, want := range snapshot {
		if got := read(t, p); got != want {
			t.Errorf("%s 二次生成后发生变化（违反确定性）", p)
		}
	}

	// 契约测试人工填充后不再被覆盖
	write(t, filepath.Join(dir, "store", "contract_gen_test.go"),
		strings.Replace(contract, `t.Error("契约断言未填充：CreateGadget（填充断言后删除本行）")`, "// 已填充", 1))
	if err := runGenerate(dir); err != nil {
		t.Fatalf("三次 generate: %v", err)
	}
	if got := read(t, filepath.Join(dir, "store", "contract_gen_test.go")); got == contract {
		t.Error("契约测试应只生成一次，之后不被覆盖")
	}
}

func TestGenerateBoundOutputs(t *testing.T) {
	root := newTempModule(t)
	if err := runResourceAdd("demo/engine", TypeBound, root); err != nil {
		t.Fatalf("resource add: %v", err)
	}
	dir := filepath.Join(root, "internal", "demo", "resources", "engine")

	spec := `resource:
  domain: demo
  name: engine
  type: bound
  table: engines
  workflow: "每个引擎实体对应一个生命周期流程（ID: engine/{id}）"

operations:
  - name: CreateEngine
    kinds: [write, lifecycle]
    workflow: "落库后以 engine/{id} 拉起生命周期流程（Abandon 子流程）"
    idempotency: "ExecuteWorkflow 持久化启动，重试不重复落库"
    audit: "created_at + workflow 历史"
    input:
      - { name: engine, type: Engine }
    output:
      - { name: engine, type: Engine }
  - name: AdvanceStage
    kinds: [lifecycle]
    workflow: "向 engine/{id} 发信号；阶段只能由流程内 activity 落库"
    idempotency: "重复信号写相同值，不产生新事实"
    audit: "workflow 事件历史"
    input:
      - { name: id, type: int64 }
      - { name: current_stage, type: string }
      - { name: next_stage, type: string }
  - name: DeleteEngine
    kinds: [write, lifecycle]
    workflow: "删除的合规形态：先取消流程再删记录"
    idempotency: "二次删除返回 ErrNotFound"
    audit: "workflow 历史 + RowsAffected"
    input:
      - { name: id, type: int64 }
      - { name: reason, type: string }
`
	write(t, filepath.Join(dir, "resgen.yaml"), spec)

	if err := runGenerate(dir); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// bound：orchestrator + activity 均生成
	orch := read(t, filepath.Join(dir, "handler", "orchestrator_gen.go"))
	act := read(t, filepath.Join(dir, "activity", "activity_gen.go"))

	for _, want := range []string{
		"type Orchestrator interface",
		"CreateEngine(ctx context.Context, in port.CreateEngineInput) (port.CreateEngineOutput, error)",
		"AdvanceStage(ctx context.Context, in port.AdvanceStageInput) error",
		"DeleteEngine(ctx context.Context, in port.DeleteEngineInput) error",
	} {
		if !strings.Contains(orch, want) {
			t.Errorf("orchestrator_gen.go 缺少 %q:\n%s", want, orch)
		}
	}

	for _, want := range []string{
		`const TaskQueue = "demo.engine.activities"`,
		"func (a *Activities) CreateEngine(ctx context.Context, in port.CreateEngineInput) (port.CreateEngineOutput, error)",
		"func (a *Activities) DeleteEngine(ctx context.Context, in port.DeleteEngineInput) error",
		"r.RegisterActivity(a.CreateEngine)",
		"r.RegisterActivity(a.DeleteEngine)",
	} {
		if !strings.Contains(act, want) {
			t.Errorf("activity_gen.go 缺少 %q:\n%s", want, act)
		}
	}
	// AdvanceStage 是 lifecycle-only：不进 activity
	if strings.Contains(act, "AdvanceStage") {
		t.Errorf("lifecycle-only 操作不应包装成 activity:\n%s", act)
	}

	// 输入字段名转 PascalCase + 缩略词（空白不敏感，避开 gofmt 对齐）
	opsGen := read(t, filepath.Join(dir, "port", "ops_gen.go"))
	flat := strings.Join(strings.Fields(opsGen), " ")
	for _, want := range []string{"ID int64", "CurrentStage string", "NextStage string", "Reason string"} {
		if !strings.Contains(flat, want) {
			t.Errorf("输入类型字段名转换错误，缺少 %q:\n%s", want, opsGen)
		}
	}
}

func TestGenerateLocationMismatch(t *testing.T) {
	root := newTempModule(t)
	if err := runResourceAdd("demo/gadget", TypePlain, root); err != nil {
		t.Fatalf("resource add: %v", err)
	}
	dir := filepath.Join(root, "internal", "demo", "resources", "gadget")
	write(t, filepath.Join(dir, "resgen.yaml"), filledSpec)

	// 篡改 domain 制造位置不一致
	bad := strings.Replace(filledSpec, "domain: demo", "domain: other", 1)
	write(t, filepath.Join(dir, "resgen.yaml"), bad)
	if err := runGenerate(dir); err == nil {
		t.Fatal("规格位置与声明不一致时应失败")
	}
}

func TestArchLintRegionSortedAndIdempotent(t *testing.T) {
	root := newTempModule(t)
	for _, args := range [][2]string{{"demo", "beta"}, {"demo", "alpha"}, {"crm", "lead"}} {
		if err := refreshArchLint(root, args[0], args[1]); err != nil {
			t.Fatalf("refreshArchLint: %v", err)
		}
	}
	text := read(t, filepath.Join(root, ".go-arch-lint.yml"))

	// 条目按名字排序，锚点之外的内容原样保留
	lines := regionEntries(t, text)
	if len(lines) != 3 {
		t.Fatalf("标记区应有 3 条，得到 %d", len(lines))
	}
	for i := 1; i < len(lines); i++ {
		if lines[i-1] > lines[i] {
			t.Errorf("标记区条目未排序: %v", lines)
		}
	}
	if !strings.Contains(text, "commonComponents: [port]") {
		t.Errorf("锚点行应保持原样:\n%s", text)
	}

	// 幂等：重复刷新不改文件
	if err := refreshArchLint(root, "demo", "alpha"); err != nil {
		t.Fatalf("再次 refreshArchLint: %v", err)
	}
	if got := read(t, filepath.Join(root, ".go-arch-lint.yml")); got != text {
		t.Errorf("重复刷新应无变化")
	}
}

func regionEntries(t *testing.T, text string) []string {
	t.Helper()
	begin := strings.Index(text, archBegin)
	end := strings.Index(text, archEnd)
	if begin < 0 || end < begin {
		t.Fatalf("缺少标记区:\n%s", text)
	}
	var out []string
	for _, line := range strings.Split(text[begin+len(archBegin):end], "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}
