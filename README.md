# Exchange Project - 简化版交易所撮合引擎

## 项目概述

这是一个基于 Go 语言实现的简化版交易所撮合引擎项目，适合作为 Bybit Golang 实习岗位的面试项目。

## 技术栈

- **语言**: Go 1.21+
- **Web 框架**: Gin
- **数据库**: MySQL + GORM
- **缓存**: Redis
- **RPC**: gRPC + Protobuf
- **容器化**: Docker + Docker Compose

## 核心功能

- [x] 撮合引擎 (Matching Engine)
- [x] 订单簿 (OrderBook)
- [x] 订单管理 (Order Service)
- [x] 用户账户 (User Service)
- [x] JWT 认证
- [x] RBAC 权限控制
- [x] 幂等设计
- [x] Cache-Aside 缓存模式
- [x] Redis 分布式锁
- [x] Redis Stream 通知
- [x] Worker Pool 并发处理
- [x] 统一错误码
- [x] 结构化日志

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
│   ├── user/                 # 用户服务实现
│   ├── order/                # 订单服务实现
│   ├── matching/             # 撮合引擎实现
│   │   ├── engine/           # 撮合引擎核心
│   │   ├── book/             # 订单簿
│   │   └── workerpool/       # Worker Pool
│   └── notify/               # 通知服务
├── migrations/                # 数据库迁移
└── scripts/                  # 工具脚本
```

## 快速开始

### 1. 启动依赖服务

```bash
docker-compose up -d mysql redis
```

### 2. 初始化数据库

```bash
mysql -h localhost -u root -p < migrations/001_init_schema.sql
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

- `POST /api/v1/auth/login` - 用户登录
- `POST /api/v1/auth/register` - 用户注册

### 订单

- `POST /api/v1/orders` - 创建订单 (需要 JWT)
- `POST /api/v1/orders/cancel` - 取消订单 (需要 JWT)
- `GET /api/v1/orders` - 订单列表 (需要 JWT)
- `GET /api/v1/orders/:order_id` - 获取订单 (需要 JWT)

### 余额

- `GET /api/v1/balance` - 获取余额 (需要 JWT)

### 订单簿

- `GET /orderbook/:symbol` - 获取订单簿

### 健康检查

- `GET /healthz` - 健康检查
- `GET /readyz` - 就绪检查

## 简历项目描述

```
【简化版交易所撮合引擎】
- 技术栈：Go / Gin / gRPC / MySQL / Redis / Docker
- 负责模块：撮合引擎、订单服务、API 网关
- 核心成果：
  - 实现内存撮合引擎，采用 sync.RWMutex 保护订单簿并发读写
  - 设计订单状态机（Pending -> PartialFilled -> Filled/Cancelled）
  - 实现订单幂等创建（Idempotency-Key + Redis 缓存 + 分布式锁）
  - 采用 Worker Pool + errgroup 并发处理撮合任务
  - 使用 Cache-Aside 模式缓存订单簿快照
  - 完成 JWT + RBAC 完整鉴权链路
  - 结构化日志贯穿 request_id / trace_id
  - gRPC 实现服务间强类型通信
  - Docker Compose 一键部署本地开发环境
```

## 面试八股对照

| 考点 | 在项目中的体现 |
|------|----------------|
| Goroutine/Channel | 撮合引擎并发处理、Channel 事件通知 |
| Mutex/RWMutex | 订单簿读写锁分离 |
| Context | gRPC 超时控制、请求取消 |
| panic/recover | 中间件 Recovery、goroutine 异常恢复 |
| defer | 函数退出时解锁、关闭 Channel、记录日志 |
| 内存分配优化 | sync.Pool 对象池、切片预分配 |
| 值传递 vs 指针 | 订单结构体传递优化 |
| Map 并发安全 | 订单簿用 RWMutex 保护 |
