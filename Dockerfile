ARG GO_IMAGE=golang:1.22-alpine
ARG RUNTIME_IMAGE=alpine:latest

# 多阶段构建 - 第一阶段：编译
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder

ARG TARGETOS=linux
ARG TARGETARCH

# 替换 Alpine 镜像源，提高国内构建稳定性
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

WORKDIR /src

ENV GO111MODULE=on \
    GOPROXY=https://goproxy.cn,https://mirrors.aliyun.com/goproxy/,direct \
    GOTOOLCHAIN=auto \
    CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 保持与仓库当前源码运行方式一致：从 web/ 目录构建主程序。
RUN cd /src/web && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /out/stock-web .

# 多阶段构建 - 第二阶段：运行
FROM ${RUNTIME_IMAGE}

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories && \
    apk --no-cache add ca-certificates tzdata wget && \
    addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser && \
    mkdir -p /app/static /app/data/database && \
    chown appuser:appuser /app /app/static /app/data /app/data/database

ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder --chown=appuser:appuser /out/stock-web /app/stock-web
COPY --from=builder --chown=appuser:appuser /src/web/static /app/static

USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/health || exit 1

CMD ["./stock-web"]
