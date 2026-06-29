# Proposal: README WAL/Snapshot 描述更新

## 动机

README 中关于 WAL/Snapshot 恢复的描述未反映近期 `2026-06-28-wal-snapshot-fsync-durability` 改动的内容。该改动新增了 fsync durability 机制，需要在 README 中体现。

## 目标

更新 README.md 中 WAL + Snapshot 恢复相关的描述，确保与最新实现一致。

## 范围

- 更新 README.md 第 43 行 WAL + Snapshot 描述
- 可选：更新 Prometheus 指标描述（如果 `pkg/metrics/metrics.go` 改动涉及新增指标）

## 约束

- 仅修改 README.md，不涉及代码逻辑变更
- 保持现有文档风格一致
