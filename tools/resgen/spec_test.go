package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validSpec 返回一份完整合法的规格（测试基线）。
func validSpec() *Spec {
	return &Spec{
		Resource: Resource{
			Domain: "example", Name: "item", Type: TypeBound,
			Table: "items", Workflow: "每个实体对应一个生命周期流程",
		},
		Operations: []Op{
			{
				Name: "CreateItem", Kinds: []string{KindWrite, KindLifecycle},
				Workflow: "落库后拉起子流程", Idempotency: "workflow 启动去重", Audit: "created_at",
				Input:  []Field{{Name: "item", Type: "Item"}},
				Output: []Field{{Name: "item", Type: "Item"}},
			},
		},
	}
}

func TestValidateAcceptsCompleteSpec(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("完整规格应通过校验: %v", err)
	}
}

func TestValidateRequiresEveryField(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Spec)
		want string
	}{
		{"资源级 workflow 为空", func(s *Spec) { s.Resource.Workflow = "" }, `resource.workflow 为空`},
		{"操作 workflow 为空", func(s *Spec) { s.Operations[0].Workflow = "" }, "workflow 为空"},
		{"幂等规则为空", func(s *Spec) { s.Operations[0].Idempotency = "" }, "idempotency 为空"},
		{"审计规则为空", func(s *Spec) { s.Operations[0].Audit = "" }, "audit 为空"},
		{"无操作", func(s *Spec) { s.Operations = nil }, "operations 为空"},
		{"input 为空", func(s *Spec) { s.Operations[0].Input = nil }, "input 为空"},
		{"未知类型", func(s *Spec) { s.Resource.Type = "magic" }, "resource.type"},
		{"未知 kind", func(s *Spec) { s.Operations[0].Kinds = []string{"read"} }, "未知 kind"},
		{"kinds 为空", func(s *Spec) { s.Operations[0].Kinds = nil }, "kinds 为空"},
		{"重复操作名", func(s *Spec) {
			s.Operations = append(s.Operations, s.Operations[0])
		}, "重复的操作名"},
		{"保留名", func(s *Spec) { s.Operations[0].Name = "List" }, "保留名"},
		{"非法字段类型", func(s *Spec) {
			s.Operations[0].Input = []Field{{Name: "a", Type: "map[string]int"}}
		}, "非法"},
		{"非法表名", func(s *Spec) { s.Resource.Table = "Items!" }, "resource.table"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSpec()
			tc.mut(s)
			err := s.Validate()
			if err == nil {
				t.Fatal("应校验失败")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息应含 %q，得到: %v", tc.want, err)
			}
		})
	}
}

func TestTypeCapabilities(t *testing.T) {
	cases := []struct {
		typ                          string
		store, activity, view, orch  bool
	}{
		{TypeBound, true, true, true, true},
		{TypeCapability, false, false, false, false},
		{TypeReference, true, false, true, false},
		{TypeTrigger, true, false, true, true},
		{TypePlain, true, false, false, false},
	}
	for _, tc := range cases {
		r := Resource{Type: tc.typ}
		if got := r.StoreBearing(); got != tc.store {
			t.Errorf("%s StoreBearing = %v", tc.typ, got)
		}
		if got := r.HasActivity(); got != tc.activity {
			t.Errorf("%s HasActivity = %v", tc.typ, got)
		}
		if got := r.HasView(); got != tc.view {
			t.Errorf("%s HasView = %v", tc.typ, got)
		}
		if got := r.Orchestrated(); got != tc.orch {
			t.Errorf("%s Orchestrated = %v", tc.typ, got)
		}
	}
}

func TestGoName(t *testing.T) {
	cases := map[string]string{
		"id":            "ID",
		"title":         "Title",
		"current_stage": "CurrentStage",
		"next_stage":    "NextStage",
		"url":           "URL",
	}
	for in, want := range cases {
		if got := goName(in); got != want {
			t.Errorf("goName(%q) = %q, want %q", in, got, want)
		}
	}
}

// newTempModule 构造一个带 go.mod 与 .go-arch-lint.yml（含锚点）的临时模块。
func newTempModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/demo\n\ngo 1.26\n")
	write(t, filepath.Join(root, ".go-arch-lint.yml"), `version: 3
workdir: .

components:
  port: { in: internal/*/resources/*/port }

commonComponents: [port]

deps:
  port:
    mayDependOn: []
`)
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
