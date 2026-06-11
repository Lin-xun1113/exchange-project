# Stage 1: Builder
FROM golang:1.21-alpine AS builder

# 安装必要工具
RUN apk add --no-cache git make protoc

# 设置工作目录
WORKDIR /app

# 复制 go mod 文件
COPY go.mod go.sum* ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 编译 API Gateway
RUN CGO_ENABLED=0 GOOS=linux go build -o /gateway ./cmd/gateway

# 编译 User Service
RUN CGO_ENABLED=0 GOOS=linux go build -o /user-svc ./cmd/user-svc

# 编译 Order Service
RUN CGO_ENABLED=0 GOOS=linux go build -o /order-svc ./cmd/order-svc

# 编译 Matching Service
RUN CGO_ENABLED=0 GOOS=linux go build -o /matching-svc ./cmd/matching-svc

# ============= API Gateway =============
FROM alpine:3.19 AS gateway

WORKDIR /app

# 安装 CA 证书
RUN apk add --no-cache ca-certificates tzdata

# 复制二进制文件
COPY --from=builder /gateway .

# 复制配置文件
COPY pkg/errors ./pkg/errors

# 暴露端口
EXPOSE 8080

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1

# 启动命令
CMD ["./gateway"]

# ============= User Service =============
FROM alpine:3.19 AS user-svc

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /user-svc .

EXPOSE 50051

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD nc -z localhost 50051 || exit 1

CMD ["./user-svc"]

# ============= Order Service =============
FROM alpine:3.19 AS order-svc

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /order-svc .

EXPOSE 50052

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD nc -z localhost 50052 || exit 1

CMD ["./order-svc"]

# ============= Matching Service =============
FROM alpine:3.19 AS matching-svc

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /matching-svc .

EXPOSE 50053

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD nc -z localhost 50053 || exit 1

CMD ["./matching-svc"]
