## Context

当前 `pkg/metrics/metrics.go` 仅覆盖 4 类基础指标（HTTP/gRPC 客户端），缺少撮合引擎、订单生命周期、Saga 状态流转和限流的可见性。阶段 4 ⑨ 要求从 4 类扩展到 ~15 类，覆盖撮合/订单/余额/限流全链路。

## Goals / Non-Goals

**Goals:**
- 扩展 `pkg/metrics/metrics.go` 新增 15+ Prometheus 指标
- 在撮合引擎 `engine.go` 中注入撮合延迟直方图
- 在 order service 中注入订单创建/取消/成交率指标
- 在限流中间件中注入拦截计数
- 为 WAL/Saga（未来实现）预留指标注册位
- 编写 `docs/metrics.md` 说明每个指标的用途

**Non-Goals:**
- 不修改指标暴露端点（保持 `/metrics`）
- 不实现 WAL 和 Saga 本身（WAL/Saga 变更在独立变更中）
- 不实现 Grafana Dashboard（留作后续）

## Decisions

### Decision 1: 使用 prometheus/promauto 自动注册指标

**选项：**
- `prometheus.MustRegister` — 手动注册，需要维护 registerer
- `promauto.New*` — 在 init 时自动注册，无需额外管理

**选择：** `promauto`，与现有代码风格一致（`pkg/metrics/metrics.go` 已使用），简化注册逻辑。

### Decision 2: 指标注入方式

**选项 A：** 在每个业务模块独立创建指标变量（如 `internal/matching/metrics/metrics.go`）
**选项 B：** 集中在 `pkg/metrics/metrics.go` 中注册，所有模块通过 `GetMetrics()` 获取

**选择：** 选项 B。集中管理避免重复注册，API 风格统一，也便于后续接入 Prometheus scrape 配置。

### Decision 3: Histogram Bucket 选择

撮合延迟需要亚毫秒级精度，选用：
```go
Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .5, 1}
```

### Decision 4: WAL/Saga 指标占位

由于 WAL 和 Saga 尚未实现，在 `metrics.go` 中预留结构体字段和方法签名但不调用 `promauto`。WAL/Saga 变更完成后在对应模块中调用即可。

## Risks / Trade-offs

| 风险 | 影响 | 缓解 |
|------|------|------|
| 指标过多增加内存 | Prometheus 客户端每个指标占用 ~1KB | ~15 类指标开销可忽略 |
| WAL/Saga 指标无法立即验证 | 待 WAL/Saga 实现后才能采集 | 预留注册位，实现时开箱即用 |
| 标签基数爆炸（symbol） | symbol 过多时 cardinality 过高 | symbol 已做白名单/业务约束，无风险 |
