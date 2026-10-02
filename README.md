# saas-template

单域最小可运行骨架：**Go + templ + htmx + SQLite + Temporal**，
端口-适配器分层，依赖方向由机器检查。

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

gonew 会重写全部 import 路径。派生后做三件事：

1. 把 `internal/example` 改名为你的域（`contract`、`workflow`、`resources/item` 同步改名），
   替换 `resources/item` 为你的首个资源；store 的迁移文件在 `internal/shared/db/migrations/`。
2. 若资源**没有跨时间状态机**：删掉它的 `activity/` 子包与域内 workflow，
   handler 直调 store（结构即答案）。
3. 按需拆分进程：`cmd/server` 是单进程开发形态（HTTP + 双 worker 同进程），
  拆分时把三段各搬进 `cmd/webserver`、`cmd/workflow-worker`、`cmd/resources-worker`。

## 本地运行

前置：Go 1.26+、temporal CLI（`brew install temporal`）。

```bash
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate   # 生成 *_templ.go
temporal server start-dev                                   # 终端 1：Temporal :7233，UI :8233
go run ./cmd/server                                         # 终端 2：应用 :8090
```

环境变量：`ADDR`、`DB_PATH`（默认 data/app.db）、`TEMPORAL_ADDRESS`（默认 localhost:7233）、`TEMPORAL_NAMESPACE`（默认 default）。

## CI 检查（.github/workflows/ci.yml）

1. `templ generate` + `git diff --exit-code` —— 生成物与提交一致
2. `go-arch-lint check` —— 依赖方向（规则见 `.go-arch-lint.yml`，通配符按域匹配，加域不用改）
3. `workflowcheck ./internal/...` —— workflow 确定性（禁 time.Now / IO 等）
4. `go vet` + `go build` + `go test` —— 测试用假编排，不需要 Temporal Server

## 给 AI 的约束

见 [AGENTS.md](AGENTS.md)：资源规格、分层约定、判断规则、不变量与已知陷阱。
