# Design: README WAL/Snapshot 描述更新

## 概述

根据 `2026-06-28-wal-snapshot-fsync-durability` 归档改动，README 需要反映以下更新：

## WAL + Snapshot 恢复描述更新

**当前描述：**
```
- [x] **WAL + Snapshot 恢复** - 崩溃恢复机制
```

**更新后描述：**
```
- [x] **WAL + Snapshot 恢复** - Group Commit fsync + 目录同步，零数据丢失
```

说明：新增了 Group Commit（组提交）fsync 机制，确保 WAL 写入在崩溃后不丢失，同时 Snapshot Rename 前会同步目录元数据。

## Prometheus 指标描述更新

**当前描述：**
```
- [x] **Prometheus 指标扩展** - 覆盖 HTTP/gRPC/撮合/Saga/Outbox/限流
```

**更新后描述：**
```
- [x] **Prometheus 指标扩展** - 覆盖 HTTP/gRPC/撮合/Saga/Outbox/限流/WAL
```

说明：`pkg/metrics/metrics.go` 已新增 4 个 WAL 指标：
- `matching_wal_append_seconds` - WAL 追加耗时
- `matching_wal_fsync_seconds` - fsync 耗时
- `matching_wal_pending_entries` - 待同步条目数
- `matching_wal_group_size` - 组提交大小
