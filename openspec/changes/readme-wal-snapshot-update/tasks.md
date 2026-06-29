# Tasks: README WAL/Snapshot 描述更新

## Task List

- [x] 更新 README.md 第 43 行 WAL + Snapshot 描述
- [x] 更新 README.md 第 44 行 Prometheus 指标描述
- [x] 运行 guard 完成 build → verify 过渡

## Task Details

### Task 1: 更新 WAL + Snapshot 描述

**文件**: `README.md`
**位置**: 第 43 行
**变更**: 将 `WAL + Snapshot 恢复 - 崩溃恢复机制` 更新为 `WAL + Snapshot 恢复 - Group Commit fsync + 目录同步，零数据丢失`

### Task 2: 更新 Prometheus 指标描述

**文件**: `README.md`
**位置**: 第 44 行
**变更**: 在 `覆盖 HTTP/gRPC/撮合/Saga/Outbox/限流` 后添加 `/WAL`

### Task 3: 运行 build guard

**命令**: `"$COMET_BASH" "$COMET_GUARD" readme-wal-snapshot-update build --apply`
