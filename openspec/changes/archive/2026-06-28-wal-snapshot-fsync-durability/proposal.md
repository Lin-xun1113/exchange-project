# WAL and Snapshot Durability Fix

## Why

The current WAL (Write-Ahead Log) and Snapshot implementations have a critical durability flaw: they only flush to the OS page cache (via `bufio.Writer.Flush()`) but never call `file.Sync()` to force data to disk. This means a power failure, kernel panic, or container hard-kill can result in data loss. For a trading system where data integrity is paramount, this is unacceptable. Additionally, Snapshot's `os.Rename` operation lacks a corresponding directory sync, leaving parent directory metadata at risk.

## What Changes

- **WAL SyncMode**: Introduce configurable fsync strategies (`SyncNone`, `SyncAlways`, `SyncByCount`, `SyncByDuration`) with Group Commit as the default to balance durability and performance
- **WAL Append fsync**: After `writer.Flush()`, call `file.Sync()` when sync condition is met
- **Snapshot Save fsync**: Add `file.Sync()` before close and `dir.Sync()` after rename to ensure atomic durability
- **New Metrics**: Add `matching_wal_fsync_seconds`, `matching_wal_pending_entries`, `matching_wal_group_size` for observability
- **Config Propagation**: Wire SyncMode from `cmd/matching-svc/main.go` through `engine.Config` to `WALManager`
- **Documentation**: Update WAL/Snapshot recovery interview doc to reflect correct behavior

## Capabilities

### New Capabilities

- `wal-durability`: Configurable fsync strategies for WAL to ensure crash-safe persistence
- `snapshot-durability`: Atomic snapshot saves with file and directory sync for guaranteed durability

### Modified Capabilities

- `prometheus-metrics`: Add new WAL fsync and Group Commit metrics to the existing WAL Metrics requirement

## Impact

- **Modified Files**:
  - `internal/matching/wal/wal.go`: Add SyncMode type, fields, shouldSync logic, and fsync calls
  - `internal/matching/snapshot/snapshot.go`: Add file.Sync() and dir.Sync() to Save()
  - `internal/matching/engine/engine.go`: Propagate SyncMode in Config and WALManager
  - `cmd/matching-svc/main.go`: Inject SyncMode configuration
  - `pkg/metrics/metrics.go`: Register new fsync metrics
  - `docs/interview/03-wal-snapshot-recovery.md`: Update documentation
- **New Tests**: `internal/matching/wal/wal_test.go` for fsync and Group Commit scenarios
- **Performance**: Group Commit (1ms window) limits worst-case data loss to 1ms while maintaining throughput
