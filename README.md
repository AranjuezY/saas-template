# saas-template

单域最小可运行骨架：**Go + templ + htmx + SQLite + Temporal**，
端口-适配器分层，依赖方向由机器检查，资源骨架由 `tools/resgen` 生成。

```
浏览器 ──HTTP──> server ──读──> SQLite
                  │
                  └─写/更新─> Temporal Server
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

## 前端（web/）：零 Node，库不入库

样式 = daisyUI 5 组件类 + Tailwind v4 按需工具类；交互 = htmx（HTML over the
wire，无客户端应用）。**仓库不提交任何第三方库**——现成的东西由脚本按
版本 + SHA256 拉取，产物提交入库：

```bash
web/setup.sh     # clone 后唯一必做：拉 htmx（51KB，go:embed 依赖）
web/build.sh     # 仅改样式时：拉 daisyUI 插件 + tailwind standalone CLI 并构建
```

| 内容 | 位置 | 入库？ |
| --- | --- | --- |
| 样式源（`app.css`：主题/扫描源声明） | `web/` | ✓ |
| 构建产物 `app.css`（约 54KB，gzip 后 9KB） | `internal/shared/ui/assets/` | ✓（与 `*_templ.go` 同一哲学） |
| htmx 运行时库 / daisyUI 插件 / tailwind standalone 二进制 | 本地缓存（gitignore） | ✗ 按需拉取 |

两个已知坑（`app.css` 注释里也有）：

- 必须保留 `@import "tailwindcss" source(none)` 并显式声明
  `@source "../internal/"`——自动源检测会把缓存目录里的 daisyUI 源文件
  也当模板扫描，产物从 54KB 膨胀到 376KB；
- htmx.min.js 被 gitignore 后，Tailwind 扫描同样会跳过它（v4 尊重
  .gitignore）——这是刻意的：压缩 JS 里的普通单词会污染工具类候选集。

## CI 检查（.github/workflows/ci.yml）

1. `templ generate` + `go generate ./...` + `git diff --exit-code` —— 生成物与提交一致
2. `go-arch-lint check` —— 依赖方向（规则见 `.go-arch-lint.yml`，通配符按域匹配，加域不用改）
3. `workflowcheck ./internal/...` —— workflow 确定性（禁 time.Now / IO 等）
4. `go vet` + `go build` + `go test` —— 测试用假编排，不需要 Temporal Server

## 自动部署（.github/workflows/deploy.yml）

自建 Gitea 上 push `main` → CI 全绿后自动 `docker build` + `compose up`
（SQLite 落命名卷，重部署不丢数据），最后 `/healthz` 健康检查。Temporal 复用
宿主机既有栈，namespace 由流水线幂等注册。GitHub 上同仓库**只跑 CI 不部署**
（deploy.yml 按 owner 判断跳过）。注意：进程启动即校验 Temporal 连接，
Temporal 不可达时容器会重启循环、恢复后自愈。

**派生新项目时改 4 处专属配置**（不改会与本模板互相覆盖）：

| 位置 | 改什么 |
| --- | --- |
| `deploy.yml` + `deploy/compose.yml` | 镜像名 / 容器名 `saas-template` → 项目名 |
| `deploy/compose.yml` | 端口 `8090`（同机多项目需错开） |
| `deploy.yml` + `deploy/compose.yml` | Temporal namespace → 项目名 |

回滚：每次部署保留 sha 标签镜像，`docker tag <旧sha> saas-template:latest`
后重新 `compose up -d` 即可。

其余为**机器相关一次性配置**（换服务器才动）：`deploy.yml` 里的镜像仓库源
与 admin-tools 镜像、Dockerfile 的 `GOIMAGE`/`RUNTIMEIMAGE` 覆盖参数；
runner 侧的 act-job 镜像与 toolcache 缓存卷需随 Go 版本同步升级。

## 给 AI 的约束

见 [AGENTS.md](AGENTS.md)：资源规格、分层约定、判断规则、不变量与已知陷阱。
