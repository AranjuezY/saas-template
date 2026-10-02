# AGENTS.md — saas-template 约束说明

## 1. 模板是什么

单域最小可运行骨架：Go、templ、htmx、SQLite（业务数据）、Temporal（流程编排）。
派生项目时替换 `internal/example` 为你的首个域，其余全部保留。

## 2. 核心抽象（先理解这四个词，再改代码）

- **资源（resource）**：拥有"事实"的对象，有唯一写入点。
- **Workflow**：记录"尚未兑现的承诺"，即在等待谁、等到何时、结果不同如何走。
- **绑定实体**：有自己长期 Workflow 执行的资源实体，其 ID 决定 Workflow ID。
- **非绑定资源**：没有自己的流程，但被 Workflow 调用、读取或触发。

一句话：**资源记事实，Workflow 记承诺。**

## 3. 资源规格（每个域必须按此切分）

```
internal/<域>/
├── contract/          跨进程契约：workflow 类型名、信号名、ID 规则、队列
└── workflow/          workflow 定义 + 发起侧客户端（按名调用，不 import 定义）
    └── resources/<资源>/
        ├── port/      对外契约：类型、校验、错误、操作清单
        ├── store/     事实唯一写入点
        ├── activity/  Temporal 副作用（仅绑定实体拥有）
        ├── handler/   HTTP 处理 + Orchestrator 消费者接口
        └── view/      templ 组件
```

- 非绑定资源**没有** activity 子包，handler 直调 store——差别只在于选用了哪几个部分。
- 判断一个资源是否绑定，看 port 里有没有编排契约、目录里有没有 activity。
- 共享基础件只有三个：`internal/shared/{db,ui,temporalclient}`，不承载业务。
- 骨架与代码契约由 `tools/resgen` 从规格（资源根 `resgen.yaml`）生成：
  `resgen resource add <域>/<资源> --type <类型>` 建目录骨架，`go generate ./...`
  生成 `ops_gen.go`（操作接口 + 输入输出类型 + 操作描述表）、
  Orchestrator 接口、activity 包装（仅 bound）与契约测试骨架（初始为红，
  填断言变绿才算实现完成）。规格留空必填字段即拒绝生成。
  `*_gen.go` 不得手改（重生成会覆盖）；`contract_gen_test.go` 仅首次生成，之后人工维护。

## 4. 判断规则

**要不要用 Workflow？** 问：这件事的完成是否依赖别的人或系统在以后行动？
- 否 → 处理器直接调用存储，不经过 Temporal。
- 是 → Workflow。

**要不要做成 Activity？** 问：这项操作不依赖流程也能独立成立吗？
- 能 → 处理器直接调用存储。
- 只有流程推进时才发生 → Activity。

**同一对象的两面**：资料类事实由资源操作写入；流程拥有的字段（如阶段）只能由对应 Activity 写入，页面不得直接改。

## 5. 不变量（违反即错误，CI 拦截）

1. 每个可变事实只有一个写入点（store → db 是唯一通路）。
2. Workflow 代码必须确定性；副作用只能通过 Activity（workflowcheck 检查）。
3. 依赖方向按 `.go-arch-lint.yml` 执行（arch-lint 检查）。
4. 会产生副作用的操作必须幂等；重试不能重复生效。
5. 跨资源传递的是当时的快照，而不是实时引用。
6. **操作登记先于实现**：写入、生命周期与副作用类操作必须在 port 的
   `Operations` 清单声明（handler 有守卫测试：编排入口不登记即失败）。
   清单由 resgen 从规格生成——改清单 = 改 `resgen.yaml` 后 `go generate`。
7. 生成物 `*_templ.go` 必须与 `.templ` 同提交，`*_gen.go` 必须与规格同步
   （CI 用 `go generate ./...` + `git diff --exit-code` 检查）。

## 6. 已知陷阱（都踩过，别再踩）

- 子流程必须显式 `ParentClosePolicy: Abandon`——默认 Terminate 会随短命令父流程被杀。
- `Selector.Select(ctx)` 不观察取消——信号等待循环必须注册 `ctx.Done()` 分支，
  否则取消请求送达后流程永远阻塞，留下孤儿 Running 实例。
- 重置业务库但保留 Temporal 历史时，自增 ID 会与已完成 workflow 撞 ID。
- 候选类写请求超时 ≠ 取消：ExecuteWorkflow 是持久化启动，worker 恢复后会补执行。

## 7. 先问再做

- 修改 contract 中的类型名、ID 规则或信号约定。
- 改变 Workflow 的状态转移，或新增状态。
- 修改数据库结构。
- 引入新依赖或新的进程。
