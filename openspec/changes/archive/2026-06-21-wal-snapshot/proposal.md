# WAL + Snapshot + Crash Recovery

## Why

The matching engine currently holds order book state only in memory. A process crash (OOM, SIGKILL, machine reboot) causes all pending orders and trade history to be lost, requiring manual reconciliation. We need Write-Ahead Logging (WAL) and periodic Snapshots to ensure the matching service can recover to a consistent state after any crash, meeting the ROADMAP Phase 3 requirement for persistence and disaster recovery.

## What Changes

- **New WAL module** (`internal/matching/wal/`): Append-only log of all matching commands (submit order, cancel order) and trade events. Each entry is a framed binary record for O(1) tail reads.
- **New Snapshot module** (`internal/matching/snapshot/`): Async dump of the full per-symbol OrderBook state (all active orders with prices, quantities, sides) to disk. Triggered every N trades or T seconds.
- **Engine WAL injection**: Every `SubmitOrder` and `CancelOrder` call writes its command to WAL before execution. Trade results are also written to WAL after matching.
- **Startup recovery**: On startup, the engine loads the latest snapshot, then replays all WAL entries since that snapshot's LSN (Log Sequence Number), rebuilding the exact same order book state.
- **Docker volume mount**: WAL and snapshot files are stored under a configurable `data/wal/` directory (intended for docker volume mount) so they survive container restarts.

## Capabilities

### New Capabilities

- `matching-wal`: Write-Ahead Logging for the matching engine. Append-only binary log with framed records, LSN tracking, and WAL truncation after snapshot. Covers both command log (order submissions, cancellations) and event log (trade results).
- `matching-snapshot`: Async order book snapshot with configurable trade-count and time-based triggers. Snapshots include full order state for crash recovery replay.
- `matching-recovery`: Startup recovery logic that loads the latest snapshot and replays WAL entries to reconstruct the order book.

### Modified Capabilities

- (none — this change introduces new modules and modifies `matching-svc`, but does not change any existing requirement behavior)

## Impact

- **Code changes**:
  - New `internal/matching/wal/wal.go` — WAL implementation with file I/O
  - New `internal/matching/snapshot/snapshot.go` — Snapshot dump and load
  - Modified `internal/matching/engine/engine.go` — inject WAL writes
  - Modified `cmd/matching-svc/main.go` — startup recovery sequence
- **Data**: WAL files grow under `data/wal/` (docker volume). Snapshots stored under `data/snapshots/`.
- **Dependencies**: Standard library `os`, `bufio`, `encoding/binary`, `encoding/gob`. No new external dependencies.
- **Ops**: Requires persistent storage (docker volume) for WAL and snapshot files.
