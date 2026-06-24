# Exchange Project - 加密资产现货撮合引擎

## 项目概述

这是一个基于 Go 语言实现的加密资产现货撮合引擎项目。项目采用微服务架构，包含 API Gateway、User Service、Order Service、Matching Service 四个核心服务，通过 gRPC 实现强类型服务间通信。

## 技术栈

| 分类 | 技术 |
|------|------|
| 语言 | Go 1.21+ |
| Web 框架 | Gin |
| RPC | gRPC + Protobuf |
| 数据库 | MySQL + GORM |
| 缓存 | Redis (go-redis/redis/v8) |
| 监控 | Prometheus |
| 日志 | zap |
| 容器化 | Docker + Docker Compose |

## 核心功能

- [x] 撮合引擎 (Matching Engine) - Per-Symbol Actor 模型
- [x] 订单簿 (OrderBook) - 价格优先 + 时间优先
- [x] **订单类型**: 支持 Limit / Market / IOC / FOK 四种类型
- [x] 订单管理 (Order Service)
- [x] 用户账户 (User Service)
- [x] JWT 认证 (HS256)
- [x] RBAC 权限控制 (admin/trader/viewer)
- [x] 幂等设计 (Redis + MySQL 三级防护)
- [x] Cache-Aside 缓存模式 (5min TTL)
- [x] Redis 分布式锁 (SetNX)
- [x] Redis Stream 事件分发 (Consumer Group)
- [x] Worker Pool 并发处理 (背压策略)
- [x] gRPC 拦截器 (超时/重试/熔断)
- [x] 统一错误码
- [x] 结构化日志 (request_id 链路)

## 支持的订单类型

本项目支持 **4 种订单类型**，覆盖主流交易所的核心功能：

| 类型 | 名称 | 说明 |
|------|------|------|
| `limit` | 限价单 | 价格优先 + 时间优先,未成交部分挂入订单簿 |
| `market` | 市价单 | 以对手方最优价格立即成交,剩余撤销 |
| `ioc` | Immediate-Or-Cancel | 即时成交或取消,不允许挂单 |
| `fok` | Fill-Or-Kill | 全部成交或全部取消,不允许部分成交 |

### 限价单 (Limit Order)

```
用户以指定价格下单,若当时无法完全成交,则未成交部分进入订单簿等待。

撮合规则:
- 买单: 按价格降序 (价格越高越优先)
- 卖单: 按价格升序 (价格越低越优先)
- 同价位: 按时间升序 (先到先得,纳秒级 FIFO)
```

**示例**:
```
买单: [price=100, qty=10] → 匹配价格 ≤ 100 的卖单
卖单: [price=101, qty=10] → 匹配价格 ≥ 101 的买单
```

### 市价单 (Market Order)

```
无价格限制,直接以对手方最优价格成交,适用于追求快速成交的场景。

特点:
- 无需指定价格 (price 字段可设为 0)
- 立即与订单簿中对侧最优价格撮合
- 无法成交部分直接撤销 (UnfilledQty)
```

**示例**:
```
市场上有卖单: [price=100, qty=5], [price=101, qty=5]
市价买单: [qty=8] → 成交 5@100 + 3@101, 撤销剩余 2
```

### IOC 单 (Immediate-Or-Cancel)

```
立即尝试成交,未成交部分立即取消,不允许挂单等待。

特点:
- 部分成交可接受
- 未成交数量立即撤销,不入订单簿
- 适用于需要流动性但不愿影响盘口的情况
```

**示例**:
```
订单簿卖单: [price=100, qty=3]
IOC 买单: [price=105, qty=10] → 成交 3@100, 撤销剩余 7
```

### FOK 单 (Fill-Or-Kill)

```
必须全部成交,否则全部取消。适用于大单需要一次性吃光盘口。

特点:
- 必须全部成交
- 无法全部成交时整个订单取消
- 对盘口深度要求高,不适合流动性差的交易对

回滚机制:
- 若无法全部成交,自动回滚已撮合的交易
- 保证账户余额状态不变
```

**示例**:
```
订单簿卖单: [price=100, qty=5], [price=101, qty=5] (总共 10)
FOK 买单: [price=102, qty=10] → 全部成交 (5@100 + 5@101) ✓
FOK 买单: [price=102, qty=15] → 全部撤销 (盘口不足) ✗
```

### 订单类型对比

```
┌──────────┬────────────┬────────────┬────────────────────────┐
│ 订单类型  │ 价格限制   │ 部分成交   │ 未成交部分处理          │
├──────────┼────────────┼────────────┼────────────────────────┤
│ limit    │ ✓ 必须指定  │ ✓ 允许    │ 进入订单簿              │
│ market   │ ✗ 无需指定 │ ✓ 允许    │ 直接撤销               │
│ ioc      │ ✓ 必须指定  │ ✓ 允许    │ 直接撤销               │
│ fok      │ ✓ 必须指定  │ ✗ 不允许  │ 全部撤销 + 回滚        │
└──────────┴────────────┴────────────┴────────────────────────┘
```

### 订单状态流转

```
┌──────────┐     ┌────────────────┐     ┌────────┐
│ Pending  │ ──▶ │ PartialFilled  │ ──▶ │ Filled │  (终态)
└──────────┘     └────────────────┘     └────────┘
       │                │
       ▼                ▼
┌──────────────┐  ┌───────────┐
│  Cancelled   │  │ Rejected  │  (终态)
└──────────────┘  └───────────┘

状态说明:
- Pending: 订单已提交,等待撮合
- PartialFilled: 部分成交
- Filled: 全部成交 (终态)
- Cancelled: 用户主动撤销 (终态)
- Rejected: 被系统拒绝 (终态)
```

## 项目结构

```
exchange-project/
├── cmd/                      # 服务入口
│   ├── gateway/              # API Gateway
│   ├── user-svc/             # 用户服务
│   ├── order-svc/            # 订单服务
│   └── matching-svc/         # 撮合引擎服务
├── api/
│   └── proto/                # Protobuf 定义
├── pkg/                      # 公共包
│   ├── config/               # 配置管理
│   ├── errors/               # 统一错误码
│   ├── logger/               # 结构化日志
│   └── response/             # 统一响应
├── internal/
│   ├── gateway/              # API Gateway 实现
│   │   ├── handler/         # HTTP Handler
│   │   └── middleware/       # JWT / 限流中间件
│   ├── user/                 # 用户服务
│   │   ├── repository/      # User Repository (含缓存)
│   │   └── service/         # User Service (含分布式锁)
│   ├── order/                # 订单服务
│   │   ├── repository/      # Order/Trade Repository
│   │   └── service/         # Order Service (幂等)
│   ├── matching/             # 撮合引擎
│   │   ├── engine/          # Matcher + Actor
│   │   ├── book/            # OrderBook (SkipList)
│   │   ├── server/          # gRPC Server
│   │   └── workerpool/      # Worker Pool
│   └── notify/               # 通知服务
│       └── consumer/         # Redis Stream Consumer
├── migrations/                # 数据库迁移
└── scripts/                  # 工具脚本
```

## 数据流架构

```
┌─────────────────────────────────────────────────────────────────┐
│                         HTTP Client                             │
└─────────────────────────────┬───────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  API Gateway (Gin) → JWT Middleware → Rate Limit                │
└─────────────────────────────┬───────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌───────────────┐    ┌───────────────┐    ┌───────────────┐
│ User Service  │    │ Order Service │    │Matching Svc  │
│ (gRPC)        │    │ (gRPC)        │    │ (gRPC)        │
└───────┬───────┘    └───────┬───────┘    └───────┬───────┘
        │                    │                    │
        └────────────┬───────┘                    │
                     ▼                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Redis / MySQL                               │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐│
│  │ Cache-Aside │  │ 分布式锁    │  │ Redis Stream (事件)     ││
│  │ (5min TTL)  │  │ (SetNX)     │  │ Consumer Group         ││
│  └─────────────┘  └─────────────┘  └─────────────────────────┘│
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ MySQL: users / orders / trades (GORM + 事务)               ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Matching Engine                                                 │
│  Matcher → Per-Symbol Actor → OrderBook (SkipList)              │
│  价格优先 + 时间优先 (纳秒级 FIFO)                              │
└─────────────────────────────────────────────────────────────────┘
```

## 快速开始

### 1. 启动依赖服务

```bash
docker-compose up -d mysql redis
```

### 2. 初始化数据库 (Docker 自动执行)

```bash
# migrations 目录下的 SQL 文件会在 docker-compose up 时自动执行
# 无需手动执行
```

### 3. 启动服务

```bash
# 启动 API Gateway
go run cmd/gateway/main.go

# 启动 User Service
go run cmd/user-svc/main.go

# 启动 Order Service
go run cmd/order-svc/main.go

# 启动 Matching Service
go run cmd/matching-svc/main.go
```

## API 接口

### 认证

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | `/api/v1/auth/login` | 用户登录 |
| POST | `/api/v1/auth/register` | 用户注册 |

### 订单

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | `/api/v1/orders` | 创建订单 (需要 JWT) |
| POST | `/api/v1/orders/cancel` | 取消订单 (需要 JWT) |
| GET | `/api/v1/orders` | 订单列表 (需要 JWT) |
| GET | `/api/v1/orders/:order_id` | 获取订单 (需要 JWT) |

### 余额

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/api/v1/balance` | 获取余额 (需要 JWT) |

### 订单簿

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/orderbook/:symbol` | 获取订单簿 |

### 健康检查

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/healthz` | 健康检查 |
| GET | `/readyz` | 就绪检查 |

## Redis 使用场景

| 场景 | Key Pattern | TTL | 说明 |
|------|-------------|-----|------|
| 用户缓存 | `user:{id}` | 5min | Cache-Aside |
| 订单缓存 | `order:{orderID}` | 5min | Cache-Aside |
| 幂等 Key | `idempotency:order:{key}` | 24h | 防重复下单 |
| 分布式锁 | `lock:*:{resource}` | 10s | SetNX |
| 事件流 | `events` Stream | 持久 | Redis Stream |
