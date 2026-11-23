# Exchange Project · 现货撮合与结算

Go 1.25 现货交易所演示项目。Gateway 提供 HTTP 接口，通过 gRPC 调用用户、订单和撮合服务。

## 启动与验证

```bash
docker compose up -d --build --wait
bash scripts/e2e.sh
```

网关使用 `http://localhost:8080`，Jaeger 使用 `http://localhost:16686`。每个服务在容器内的 `:9100/metrics` 提供 Prometheus 指标。MySQL 在宿主机使用 `3308`。

新版 MySQL schema 和撮合持久化格式需要使用新卷。`docker compose down -v` 会清除该 Compose 项目的数据库、Redis 和撮合数据，仅在确认可以清空演示环境时使用。

```bash
go build ./...
go vet ./...
go test ./...

docker compose up -d --wait mysql
TEST_MYSQL_DSN='root:password@tcp(127.0.0.1:3308)/' go test -race ./internal/user/... ./internal/order/...
go test -race ./internal/matching/... ./internal/gateway/...
```

E2E 需要 `curl` 和 `jq`，默认访问本地网关，可用 `GATEWAY_URL` 覆盖。脚本会重启当前 Compose 项目的 `matching-svc`，并短暂停止 `order-svc` 验证就绪检查。

## 演示交易台

先停止占用相同宿主机端口的后端栈，再用独立的 Compose 项目启动：

```bash
COMPOSE_PROJECT_NAME=exchange-demo docker compose --profile demo up -d --build
```

种子写入成功后，打开 `http://localhost:8088`，使用 `demo` / `demo1234` 登录。首启写入七天历史成交、实时订单簿和演示委托；完成后不再自动生成成交。默认 profile 不启用种子或 Web，仍可独立运行 E2E。

演示种子要求新卷；识别到已写入的历史成交会直接退出，不会补写实时订单簿。若初始化中断或未看到 `demo seed complete`，请改用新的 Compose 项目名，不要清除仍需保留的卷。

## 服务边界

| 服务 | 责任 |
|---|---|
| Gateway | JWT、RBAC、Redis 令牌桶限流、HTTP 与 gRPC 转换 |
| User | 用户与角色、按币种的可用和冻结余额、幂等资金流水 |
| Order | 订单和成交记录、冻结与撮合编排、成交结算与余量释放 |
| Matching | 每个交易对一个 Actor、价格优先与同价 FIFO、WAL 与快照 |

订单与首条 Outbox 任务同事务提交。工作器完成下游 RPC 后，在同一事务中更新业务状态、确认当前任务并写入后续任务。买单冻结 `限价 × 数量`，成交以 maker 价格结算，买方收回差价。卖单冻结标的币种的数量。订单状态与资金结算异步推进。

撮合 Actor 将命令追加至 WAL、更新簿，再批量同步 WAL 后返回结果。完整快照保留挂簿订单及结果缓存。恢复可从上一有效快照重放后续 WAL。

挂簿订单的结果常驻，终结订单的结果默认保留最近 100,000 个。撮合 RPC 的重试幂等性受此缓存范围约束。Outbox 步骤达到重试上限后会标记 `dead`，保留失败原因。

单次提交最多生成 512 笔成交。超过此上限的提交不会改变订单簿，订单工作流会按未成交余量释放冻结资金。

## 订单与金额

默认交易对为 `BTC/USDT,ETH/USDT`，可用 `SYMBOLS` 配置。HTTP 路径中使用 `BTC-USDT`。价格、数量和余额以 JSON 字符串传输，例如 `"20000"` 和 `"0.3"`。价格和数量最多八位小数。

| 类型 | 余量与成交规则 |
|---|---|
| `limit` | 以限价或更优价格成交，剩余挂簿 |
| `market` | 仅支持卖单，按买盘价格成交，剩余释放 |
| `ioc` | 限价立即成交，剩余释放 |
| `fok` | 先检查可成交数量，足量才撮合 |

订单状态为 `pending`、`open`、`partial_filled`、`filled`、`cancelled` 或 `rejected`。不收手续费，允许自成交。充值接口仅供演示。

## HTTP 接口

| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/api/v1/auth/register` | 注册 |
| POST | `/api/v1/auth/login` | 登录并获取 JWT |
| POST | `/api/v1/account/deposit` | 充值，需要 `Idempotency-Key` |
| GET | `/api/v1/account/balances` | 查询各币种余额 |
| POST | `/api/v1/orders` | 下单，需要 `Idempotency-Key` |
| DELETE | `/api/v1/orders/:order_id` | 请求撤单 |
| GET | `/api/v1/orders/:order_id` | 查询自己的订单 |
| GET | `/api/v1/orders` | 游标分页查询自己的订单 |
| GET | `/api/v1/trades` | 游标分页查询自己的成交 |
| GET | `/api/v1/market/orderbook/:symbol` | 查询公开盘口 |
| GET | `/api/v1/market/trades/:symbol` | 查询公开成交，`limit` 默认 50、最大 200 |
| GET | `/api/v1/market/candles/:symbol` | 查询 K 线，`interval` 默认 `15m`，`limit` 默认 300、最大 1000 |
| GET | `/api/v1/market/tickers` | 查询 24 小时行情，可用逗号分隔的 `symbols` 筛选 |
| GET | `/healthz` | 查询网关存活状态 |
| GET | `/readyz` | 检查三个 gRPC 服务与 Redis |

账户、订单和成交接口使用 `Authorization: Bearer <token>`。客户端可用同一 `Idempotency-Key` 与相同下单参数重试。
