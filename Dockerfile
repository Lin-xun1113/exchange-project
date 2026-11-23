FROM golang:1.25-alpine AS builder
WORKDIR /src

RUN CGO_ENABLED=0 go install github.com/grpc-ecosystem/grpc-health-probe@v0.4.37

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ \
    ./cmd/gateway ./cmd/user-svc ./cmd/order-svc ./cmd/matching-svc

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app
WORKDIR /app
EXPOSE 9100

FROM runtime AS gateway
COPY --from=builder /out/gateway /usr/local/bin/gateway
USER app
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 \
    CMD ["wget", "-q", "-T", "2", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"]
CMD ["gateway"]

FROM runtime AS grpc-runtime
COPY --from=builder /go/bin/grpc-health-probe /usr/local/bin/grpc_health_probe

FROM grpc-runtime AS user-svc
COPY --from=builder /out/user-svc /usr/local/bin/user-svc
USER app
EXPOSE 50051
HEALTHCHECK --interval=5s --timeout=5s --start-period=5s --retries=5 \
    CMD ["grpc_health_probe", "-addr=127.0.0.1:50051"]
CMD ["user-svc"]

FROM grpc-runtime AS order-svc
COPY --from=builder /out/order-svc /usr/local/bin/order-svc
USER app
EXPOSE 50052
HEALTHCHECK --interval=5s --timeout=5s --start-period=5s --retries=5 \
    CMD ["grpc_health_probe", "-addr=127.0.0.1:50052"]
CMD ["order-svc"]

FROM grpc-runtime AS matching-svc
COPY --from=builder /out/matching-svc /usr/local/bin/matching-svc
RUN mkdir -p /data/wal /data/snapshot && chown -R app:app /data
VOLUME ["/data"]
ENV WAL_DIR=/data/wal SNAPSHOT_DIR=/data/snapshot
USER app
EXPOSE 50053
HEALTHCHECK --interval=5s --timeout=5s --start-period=10s --retries=5 \
    CMD ["grpc_health_probe", "-addr=127.0.0.1:50053"]
CMD ["matching-svc"]
