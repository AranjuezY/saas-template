package main

// 规格（resgen.yaml）的定义、解析与校验。
//
// 规格是 resgen 的唯一事实源：resource add 生成空规格，人填完后
// go generate 据此再生代码契约。任何必填字段留空都直接失败——
// 没想清楚幂等规则与审计规则，就不许生成代码。

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// 资源类型（--type / resource.type）。
const (
	TypeBound      = "bound"      // 绑定型：一个实体对应一个 workflow
	TypeCapability = "capability" // 能力型：短信、大模型等外部能力
	TypeReference  = "reference"  // 参照型：岗位表、税率表
	TypeTrigger    = "trigger"    // 触发型：变化要通知流程
	TypePlain      = "plain"      // 纯维护：不影响任何流程
)

// ResourceTypes 是资源类型的全集与含义（帮助文本与校验共用）。
var ResourceTypes = map[string]string{
	TypeBound:      "绑定型，一个实体对应一个 workflow",
	TypeCapability: "能力型，短信、大模型等外部能力",
	TypeReference:  "参照型，岗位表、税率表",
	TypeTrigger:    "触发型，变化要通知流程",
	TypePlain:      "纯维护，不影响任何流程",
}

// 操作类别（operations[].kinds），三类不再细分。
const (
	KindWrite      = "write"      // 写入事实（资源唯一写入点之内）
	KindLifecycle  = "lifecycle"  // 启动、推进或结束流程
	KindSideEffect = "sideeffect" // 外部副作用（邮件 / 通知 / 第三方系统）
)

// AllKinds 按固定顺序列出操作类别。
var AllKinds = []string{KindWrite, KindLifecycle, KindSideEffect}

// Spec 是 resgen.yaml 的顶层结构。
type Spec struct {
	Resource   Resource `yaml:"resource"`
	Operations []Op     `yaml:"operations"`
}

// Resource 是资源级规格。
type Resource struct {
	Domain   string `yaml:"domain"`   // 域名：internal/<domain>
	Name     string `yaml:"name"`     // 资源名（单数）：internal/<domain>/resources/<name>
	Type     string `yaml:"type"`     // bound | capability | reference | trigger | plain
	Table    string `yaml:"table"`    // 事实表名（capability 类型无）
	Workflow string `yaml:"workflow"` // 必填：对 workflow 的影响总述
}

// Op 是一个登记操作的规格。
type Op struct {
	Name        string   `yaml:"name"`        // 操作名（PascalCase，与对外入口同名）
	Kinds       []string `yaml:"kinds"`       // write | lifecycle | sideeffect，可叠加
	Activity    *bool    `yaml:"activity"`    // 仅 write/sideeffect：false = handler 直调 store，不生成 activity 包装（无编排通道）
	Workflow    string   `yaml:"workflow"`    // 必填：对 workflow 的影响
	Idempotency string   `yaml:"idempotency"` // 必填：幂等规则（重试不得重复生效）
	Audit       string   `yaml:"audit"`       // 必填：审计规则
	Input       []Field  `yaml:"input"`       // 输入字段（有序，≥1）
	Output      []Field  `yaml:"output"`      // 输出字段（有序，可为空）
}

// Field 是操作的输入/输出字段。input/output 用列表而非映射，保持声明顺序稳定。
type Field struct {
	Name string `yaml:"name"` // 小写 snake_case，生成 PascalCase 字段
	Type string `yaml:"type"` // Go 类型：string / int64 / time.Time / <快照类型> / []T
}

var (
	identRe  = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	opNameRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	tableRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	fieldRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// 类型白名单：原始类型 + time.Time + 同包快照类型（含切片形态）。
	typeRe = regexp.MustCompile(`^(\[\])?(string|int|int64|float64|bool|time\.Time|[A-Z][A-Za-z0-9]*)$`)
)

// StoreBearing 报告该类型是否有事实表与 store 子包（capability 无）。
func (r Resource) StoreBearing() bool { return r.Type != TypeCapability }

// Orchestrated 报告该类型的写路径是否经编排（绑定型、触发型）。
func (r Resource) Orchestrated() bool { return r.Type == TypeBound || r.Type == TypeTrigger }

// HasActivity 报告该类型是否拥有 activity 子包（仅绑定型）。
func (r Resource) HasActivity() bool { return r.Type == TypeBound }

// HasView 报告该类型是否有维护页面（绑定 / 参照 / 触发型）。
func (r Resource) HasView() bool {
	return r.Type == TypeBound || r.Type == TypeReference || r.Type == TypeTrigger
}

// HasKind 报告操作是否带某类别。
func (o Op) HasKind(kind string) bool {
	for _, k := range o.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// IsStoreOp 报告操作是否进入 port.Ops（写事实或外部副作用）。
func (o Op) IsStoreOp() bool { return o.HasKind(KindWrite) || o.HasKind(KindSideEffect) }

// IsLifecycleOp 报告操作是否进入 Orchestrator 接口。
func (o Op) IsLifecycleOp() bool { return o.HasKind(KindLifecycle) }

// HasActivityWrapper 报告操作是否生成 activity 包装：
// 仅 write/sideeffect 且未显式声明 activity: false（AGENTS.md 判定规则：
// 不依赖流程也能独立成立的写操作，handler 直调 store，无需编排通道）。
func (o Op) HasActivityWrapper() bool {
	if !o.IsStoreOp() {
		return false
	}
	return o.Activity == nil || *o.Activity
}

// LoadSpec 读取并解析一个 resgen.yaml。
func LoadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	return &s, nil
}

// Validate 校验规格完整性。规则与 AGENTS.md 对齐：登记先于实现，
// 写入 / 生命周期 / 副作用操作的幂等规则与审计规则必填。
func (s *Spec) Validate() error {
	var errs []string
	r := s.Resource

	if !identRe.MatchString(r.Domain) {
		errs = append(errs, "resource.domain 必须是小写字母开头的标识符（如 example）")
	}
	if !identRe.MatchString(r.Name) {
		errs = append(errs, "resource.name 必须是小写字母开头的单数标识符（如 item）")
	}
	_, typeOK := ResourceTypes[r.Type]
	if !typeOK {
		errs = append(errs, fmt.Sprintf("resource.type %q 无效（可选：%s）", r.Type, strings.Join(typeNames(), " / ")))
	}
	if strings.TrimSpace(r.Workflow) == "" {
		errs = append(errs, `resource.workflow 为空——"对 workflow 的影响"必须显式填写；纯维护资源也写明"无：不影响任何流程"`)
	}
	if typeOK && r.StoreBearing() && !tableRe.MatchString(r.Table) {
		errs = append(errs, fmt.Sprintf("resource.table %q 无效（小写字母开头的表名）", r.Table))
	}

	if len(s.Operations) == 0 {
		errs = append(errs, "operations 为空——至少登记一个写入 / 生命周期 / 副作用操作（只读操作不登记）")
	}

	seen := map[string]bool{}
	for i, op := range s.Operations {
		prefix := fmt.Sprintf("operations[%d]", i)
		if op.Name != "" {
			prefix += " " + op.Name
		}

		switch {
		case op.Name == "":
			errs = append(errs, prefix+": name 为空")
		case !opNameRe.MatchString(op.Name):
			errs = append(errs, fmt.Sprintf("%s: name %q 必须是 PascalCase", prefix, op.Name))
		case seen[op.Name]:
			errs = append(errs, fmt.Sprintf("%s: 重复的操作名", prefix))
		}
		seen[op.Name] = true

		if typeOK && r.StoreBearing() && (op.Name == "List" || op.Name == "Get") {
			errs = append(errs, fmt.Sprintf("%s: List / Get 是约定读操作保留名，不可登记", prefix))
		}

		// activity 开关只对进入 store 的操作（write / sideeffect）有意义。
		if op.Activity != nil && !op.IsStoreOp() {
			errs = append(errs, fmt.Sprintf("%s: activity 仅可标注于 write/sideeffect 操作（lifecycle 操作本就没有 activity 包装）", prefix))
		}

		if len(op.Kinds) == 0 {
			errs = append(errs, prefix+": kinds 为空")
		}
		kindSeen := map[string]bool{}
		for _, k := range op.Kinds {
			switch k {
			case KindWrite, KindLifecycle, KindSideEffect:
				if kindSeen[k] {
					errs = append(errs, fmt.Sprintf("%s: kinds 重复 %q", prefix, k))
				}
				kindSeen[k] = true
			default:
				errs = append(errs, fmt.Sprintf("%s: 未知 kind %q（可选：write / lifecycle / sideeffect）", prefix, k))
			}
		}

		for _, f := range []struct{ field, val string }{
			{"workflow", op.Workflow},
			{"idempotency", op.Idempotency},
			{"audit", op.Audit},
		} {
			if strings.TrimSpace(f.val) == "" {
				errs = append(errs, fmt.Sprintf("%s: %s 为空——规格不完整，拒绝生成（先填幂等与审计规则）", prefix, f.field))
			}
		}

		if len(op.Input) == 0 {
			errs = append(errs, prefix+": input 为空（登记操作至少有一个输入字段）")
		}
		errs = append(errs, validateFields(prefix+".input", op.Input)...)
		errs = append(errs, validateFields(prefix+".output", op.Output)...)
	}

	if len(errs) > 0 {
		return fmt.Errorf("规格不完整或非法：\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func validateFields(where string, fields []Field) []string {
	var errs []string
	seen := map[string]bool{}
	for i, f := range fields {
		prefix := fmt.Sprintf("%s[%d]", where, i)
		if !fieldRe.MatchString(f.Name) {
			errs = append(errs, fmt.Sprintf("%s: 字段名 %q 必须是小写 snake_case", prefix, f.Name))
		} else if seen[f.Name] {
			errs = append(errs, fmt.Sprintf("%s: 重复字段 %q", prefix, f.Name))
		}
		seen[f.Name] = true

		if !typeRe.MatchString(f.Type) {
			errs = append(errs, fmt.Sprintf("%s: 类型 %q 非法（允许：原始类型 / time.Time / 同包快照类型，可加 [] 前缀）", prefix, f.Type))
		}
	}
	return errs
}

func typeNames() []string {
	return []string{TypeBound, TypeCapability, TypeReference, TypeTrigger, TypePlain}
}
