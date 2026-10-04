# 多阶段构建：builder 拉取锁定的前端资产并编译静态二进制，运行时仅剩单个可执行文件。
# 基础镜像走 ARG，便于在国内服务器上用镜像仓库覆盖（见 .github/workflows/deploy.yml）。

ARG GOIMAGE=golang:1.26-bookworm
FROM ${GOIMAGE} AS build

# 依赖代理同样可覆盖；默认官方源
ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPROXY=${GOPROXY}

WORKDIR /src

# 先拷贝模块定义，利用层缓存下载依赖
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# htmx（版本 + SHA256 锁定，仓库不提交现成库，见 web/setup.sh）
RUN ./web/setup.sh

RUN go build -trimpath -ldflags "-s -w" -o /out/server ./cmd/server

# 运行时：零包安装，纯拷贝二进制（CGO 关闭，静态链接）
ARG RUNTIMEIMAGE=debian:bookworm-slim
FROM ${RUNTIMEIMAGE}
WORKDIR /app
COPY --from=build /out/server /app/server
ENV ADDR=:8090 \
    DB_PATH=/app/data/app.db
EXPOSE 8090
ENTRYPOINT ["/app/server"]
