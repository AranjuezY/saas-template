package port

// 操作类别。可叠加：如"新增条目"既是写入也启动生命周期。
type OpKind string

const (
	OpWrite     OpKind = "write"     // 写入事实（资源唯一写入点之内）
	OpLifecycle OpKind = "lifecycle" // 启动、推进或结束流程
	OpEffect    OpKind = "effect"    // 外部副作用（邮件 / 通知 / 第三方系统）
)

// Operation 是资源操作清单的一项。
type Operation struct {
	Name           string   // 操作名，与对外入口同名
	Kinds          []OpKind // 类别
	WorkflowImpact string   // 对 workflow 的影响；空 = 不影响任何流程
}

// Operations 是 item 资源的操作清单。
//
// 规则（见 AGENTS.md 判断规则）：写入、生命周期与副作用类操作
// 必须登记，且登记先于实现——新增操作前先在此声明影响面，再写代码。
// 只读操作不必登记。handler 侧有守卫测试：编排入口的每个方法
// 都必须出现在本清单中。
var Operations = []Operation{
	{
		Name:           "CreateItem",
		Kinds:          []OpKind{OpWrite, OpLifecycle},
		WorkflowImpact: "落库后以 item/{id} 拉起生命周期流程（Abandon 子流程）",
	},
	{
		Name:           "AdvanceStage",
		Kinds:          []OpKind{OpLifecycle},
		WorkflowImpact: "向 item/{id} 发 activate / archive 信号；阶段只能由流程内的 activity 落库",
	},
	{
		Name:           "CancelItem",
		Kinds:          []OpKind{OpWrite, OpLifecycle},
		WorkflowImpact: "请求取消 item/{id} 后删除记录；后置：流程必须到达 Canceled，不留孤儿实例",
	},
	{
		Name:           "UpdateItemStage",
		Kinds:          []OpKind{OpWrite},
		WorkflowImpact: "仅被生命周期流程的信号分支调用，是阶段的唯一写入口",
	},
}
