# saas-template

单域最小可运行骨架：**Go + templ + htmx + SQLite + Temporal**，
端口-适配器分层，依赖方向由机器检查，资源骨架由 `tools/resgen` 生成。

```
浏览器 ──HTTP──> server ──读──> SQLite
                  │
                  └─写/信号─> Temporal Server
                                ├─> workflow-worker   编排（确定性，不碰库）
                                └─> resources-worker  副作用（activity → store）
```

## 从模板派生新项目

```bash
go run golang.org/x/tools/cmd/gonew@latest github.com/AranjuezY/saas-template example.com/you/yourapp
cd yourapp
```

gonew 会重写全部 import 路径（包括 go:generate 指令里的相对路径不受影响）。
派生后做三件事：

1. 用 resgen 建首个资源（见下），替换 `internal/example` 为你的域
   （`contract`、`workflow`、`resources/item` 同步改名）；
   store 的迁移文件在 `internal/shared/db/migrations/`。
2. 若资源**没有跨时间状态机**：`--type plain`，handler 直调 store（结构即答案）。
3. 按需拆分进程：`cmd/server` 是单进程开发形态（HTTP + 双 worker 同进程），
  拆分时把三段各搬进 `cmd/webserver`、`cmd/workflow-worker`、`cmd/resources-worker`。

## 资源生成器 resgen

规格（资源根目录 `resgen.yaml`）是唯一事实源，代码契约由此再生。
完整流程六步：

```bash
# 1. 创建骨架（--type 默认 plain：多数资源是纯维护，最严格的类型要显式选择）
go run ./tools/resgen resource add <域>/<资源> --type <bound|capability|reference|trigger|plain>
```

| 取值         | 含义                    | 生成目录                                |
| ----------- | ----------------------- | ------------------------------------- |
| bound      | 绑定型，一个实体对应一个 workflow | port/store/activity/handler/view     |
| capability | 能力型，短信、大模型等外部能力   | port/handler                         |
| reference  | 参照型，岗位表、税率表        | port/store/handler/view              |
| trigger    | 触发型，变化要通知流程        | port/store/handler/view              |
| plain      | 纯维护，不影响任何流程        | port/store/handler                   |

```text
2. 填写规格：逐操作登记 kind（write / lifecycle / sideeffect）、workflow 影响、
   幂等规则、审计规则。留空的必填字段会让 go generate 失败——这是设计出来的关卡。
3. go generate ./...：生成操作接口与输入输出类型（ops_gen.go）、操作描述表、
   Activity 包装（仅 bound）、契约测试骨架（初始为红）。
4. 手写实现：store.go 实现未导出类型，构造函数返回生成的接口；
   填充契约测试断言，变绿才算完成。
5. 更新边界配置：子包落通配路径内不用改 .go-arch-lint.yml；
   资源根组件由 resgen 自动登记到标记区。
6. 跑检查（见下）。
```

`internal/example/resources/item` 是 resgen 的第一个验证对象：
规格见其 `resgen.yaml`，`*_gen.go` 与 `contract_gen_test.go` 均由规格驱动。
同一规格产出字节级相同的代码；规格与实现谁改了没同步，CI 的 diff 检查会拦下。

## 本地运行

前置：Go 1.26+、temporal CLI（`brew install temporal`）。

```bash
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate   # 生成 *_templ.go
temporal server start-dev                                   # 终端 1：Temporal :7233，UI :8233
go run ./cmd/server                                         # 终端 2：应用 :8090
```

环境变量：`ADDR`、`DB_PATH`（默认 data/app.db）、`TEMPORAL_ADDRESS`（默认 localhost:7233）、`TEMPORAL_NAMESPACE`（默认 default）。

## CI 检查（.github/workflows/ci.yml）

1. `templ generate` + `go generate ./...` + `git diff --exit-code` —— 生成物与提交一致
2. `go-arch-lint check` —— 依赖方向（规则见 `.go-arch-lint.yml`，通配符按域匹配，加域不用改）
3. `workflowcheck ./internal/...` —— workflow 确定性（禁 time.Now / IO 等）
4. `go vet` + `go build` + `go test` —— 测试用假编排，不需要 Temporal Server

## 给 AI 的约束

见 [AGENTS.md](AGENTS.md)：资源规格、分层约定、判断规则、不变量与已知陷阱。
