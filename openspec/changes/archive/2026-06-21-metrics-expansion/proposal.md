## Why

当前 exchange-project 仅覆盖 4 类基础指标（HTTP/gRPC 请求），缺乏撮合引擎、订单生命周期、WAL 操作和 Saga 状态流转的可观测性。阶段 4 ⑨ 要求将指标扩展到 ~15 类，覆盖撮合/订单/余额/限流全链路，以便在生产环境中进行性能分析、故障定位和容量规划。

## What Changes

- 新增 **11 类业务指标**：订单创建/取消/成交率、订单簿深度/最优报价、撮合延迟/成交计数、WAL 追加耗时、Saga 状态转移/重试、限流拦截
- 新增 **2 类 gRPC 服务端指标**：替换原有的简单 Counter 为带 code 标签的 Counter + Histogram
- 新增 **1 类链路指标**：trace exporter 导出结果计数
- 编写 `docs/metrics.md` 说明每个指标的用途和查询示例

## Capabilities

### New Capabilities
- `prometheus-metrics`: 覆盖交易系统全链路的 Prometheus 指标暴露能力，包含订单、撮合、限流、Saga、gRPC 服务端五类子能力

### Modified Capabilities
- （无现有 spec 的 REQUIREMENT 变更，实现层面扩展）

## Impact

- **新增依赖**：`github.com/prometheus/client_golang/prometheus/promauto`（已引入）
- **改动文件**：
  - `pkg/metrics/metrics.go` — 扩展指标注册
  - `internal/matching/engine/engine.go` — 注入撮合延迟直方图
  - `internal/matching/wal/wal.go` — 注入 WAL append 耗时
  - `internal/order/saga/order_saga.go` — 注入 Saga 状态转移指标（如已实现）
  - `internal/gateway/middleware/ratelimit_redis.go` — 注入限流拦截计数（如已实现）
  - 新建 `docs/metrics.md` — 指标说明文档
