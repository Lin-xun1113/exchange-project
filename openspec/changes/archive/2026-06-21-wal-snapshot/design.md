## Context

The matching engine (`internal/matching/engine/`) currently maintains all order book state in memory. Each symbol has a dedicated actor goroutine with its own `OrderBook` instance. Orders and trades are processed in-memory only. The only persistence today is async batch writes to MySQL via `TradeRepository.CreateBatch` — but those writes are fire-and-forget and lag behind real-time state. A crash loses all in-flight orders and any trades not yet flushed to the database.

We need to add WAL (Write-Ahead Logging) and Snapshots so that the engine can recover to a consistent state after any crash, including `kill -9`. The implementation must be consistent with the per-symbol actor model already in place.

## Goals / Non-Goals

**Goals:**
- WAL appends every matching command (submit order, cancel order) and trade result before/during execution
- Periodic async snapshots of the full order book state, triggered by trade count and time
- Startup recovery: load latest snapshot, then replay all WAL entries since that snapshot's LSN
- Storage under `data/wal/` and `data/snapshots/` (docker volume mount points)
- No external dependencies — pure stdlib file I/O with `encoding/gob` for serialization

**Non-Goals:**
- This change does NOT handle cross-node replication or Raft consensus — single-node crash recovery only
- It does NOT replace the MySQL trade persistence; MySQL writes remain for audit/query purposes
- Snapshot and WAL truncation (compaction) beyond "delete WAL entries older than latest snapshot" is out of scope
- Distributed transactions or two-phase commit are not addressed

## Decisions

### Decision 1: Binary framed format over JSON / protobuf

**Chosen**: `encoding/gob` binary encoding with 8-byte length prefix per record.

**Rationale**: `gob` handles Go types natively (including `decimal.Decimal` from `shopspring/decimal` via its `GobEncoder`/`GobDecoder` interface), requires no schema registration, and is significantly faster than JSON. The length-prefixed framing enables O(1) tail reads (seek to end) and easy record skipping during replay. JSON would be human-readable but larger on disk and slower to decode. Protobuf requires a `.proto` schema and code generation.

**Alternatives considered**:
- JSON: human-readable but 2-3x larger and slower
- Protobuf: requires code generation and schema, overkill for internal use

### Decision 2: One WAL file per symbol

**Chosen**: One WAL file per symbol under `data/wal/{symbol}.wal`.

**Rationale**: Under the per-symbol actor model, each symbol's actor processes commands sequentially. A single WAL file per symbol means no cross-symbol lock contention when appending, and during recovery we only replay the WAL for symbols that have snapshots. This also simplifies truncation — we delete WAL entries for a symbol by rewriting the file or truncating it.

**Alternatives considered**:
- Single shared WAL with cross-symbol mutex: adds contention, complicates truncation, and forces replay of all symbols even when only one crashed
- One WAL per actor with numeric suffixes (rotation): adds rotation complexity not needed for single-node crash recovery

### Decision 3: Snapshot triggers — trade count AND time, whichever comes first

**Chosen**: Snapshot is taken when either `MaxTradesPerSnapshot` (default 1000 trades) or `SnapshotInterval` (default 60s) elapses since the last snapshot, on a per-symbol basis.

**Rationale**: A single trigger (trade-count only or time-only) can produce unbounded snapshot intervals. Time-based alone means hot symbols with few trades never snapshot, making WAL grow large. Trade-count alone means high-frequency symbols snapshot too frequently. The AND/OR dual trigger keeps WAL bounded while ensuring regular snapshots even for low-activity symbols.

**Alternatives considered**:
- Fixed interval: too rigid, doesn't adapt to activity
- Only trade count: low-activity symbols never snapshot
- Only time-based: hot symbols generate huge WAL between snapshots

### Decision 4: Snapshot stored as `data/snapshots/{symbol}-{lsn}.snap`, latest symlink at `data/snapshots/{symbol}.latest`

**Chosen**: Each snapshot is a standalone file named with its ending LSN. A `.latest` symlink points to the most recent snapshot.

**Rationale**: Standalone files enable parallel access and easy deletion of old snapshots. The `.latest` symlink provides O(1) lookup for the most recent snapshot without scanning directories. The LSN in the filename makes it easy to determine which WAL entries are needed for replay.

**Alternatives considered**:
- Single snapshot file overwritten in place: risky — crash during write corrupts the only copy
- SQLite or embedded DB for snapshots: adds a dependency

### Decision 5: WAL entries include LSN, entry type, and serialized payload

**Chosen**: Each WAL record is:
```
[8-byte LSN][1-byte type][4-byte payload length][payload bytes]
```

**Rationale**: LSN enables deterministic ordering and recovery point identification. Entry type distinguishes command vs trade entries. Length prefix enables skipping corrupted records. The structure is self-describing and supports partial replay.

**Alternatives considered**:
- No LSN: makes truncation and replay ordering ambiguous
- Type as first payload byte: less explicit, requires manual parsing

### Decision 6: Engine WAL injection via interface, not direct import

**Chosen**: `Matcher` holds a `WALWriter` interface. `WALWriter.Append(entry)` is called from within `dispatch()` (for commands) and `persistTrades()` (for trade results). The `WALWriter` implementation is instantiated in `main.go`.

**Rationale**: Keeps the engine testable (easy mock), decouples WAL logic from matching logic, and allows the WAL to be a no-op in tests. Passing the WAL into `NewMatcher` is cleaner than having the engine directly instantiate it.

**Alternatives considered**:
- Direct import of WAL package in engine: tight coupling, hard to test
- Global singleton: not idiomatic Go, makes testing harder

### Decision 7: Recovery runs before engine accepts requests

**Chosen**: In `main.go`, the recovery sequence runs at startup before the gRPC server begins accepting connections: (1) load latest snapshot for each symbol, (2) set the actor's order book state, (3) replay WAL entries, (4) start gRPC server.

**Rationale**: Ensures the engine is in a consistent state before serving traffic. Starting the gRPC server only after recovery prevents stale reads.

**Alternatives considered**:
- Lazy recovery on first request: leaves window where requests see empty or partial state
- Recovery in background: complex, requires request queuing

## Risks / Trade-offs

| Risk | Mitigation |
|------|------------|
| WAL file grows indefinitely if snapshots fail or disk fills up | Monitor disk usage; snapshots are best-effort async. WAL growth bounded by snapshot frequency. |
| Crash during snapshot write corrupts snapshot file | Snapshots are written to a temp file then renamed (atomic). If rename fails, the previous snapshot remains valid. |
| Replay of corrupted WAL entry crashes recovery | Each WAL record is length-framed. On read error, skip to next record. If all records corrupt, fall back to empty state (aggressive but safe). |
| Order of WAL entries vs snapshot is wrong causing replay to produce wrong state | WAL appends happen synchronously before/during command processing. LSN is assigned at append time. Snapshot records its ending LSN. Replay replays entries from snapshot's ending LSN + 1 to current end. |
| Long recovery time with large WAL | Snapshot every 1000 trades or 60s keeps WAL bounded. Recovery time is proportional to WAL size since snapshot. Target: <1s for 100MB WAL. |
| Actor's order book replaced during recovery but actor is already running | Recovery populates the actor's `book` field before the actor goroutine starts processing commands. Actor goroutine starts only after recovery completes for that symbol. |

## Migration Plan

1. **Deploy** the new `matching-svc` binary alongside existing binary.
2. **Mount** `data/wal/` and `data/snapshots/` volumes (empty on first deploy).
3. Start the service — recovery runs on empty state (no snapshot/WAL), engine starts fresh.
4. Snapshots begin accumulating. WAL entries are written.
5. **Verify** recovery by sending `kill -9`, restarting, and checking order book state matches pre-crash.
6. **Monitor** WAL/snapshot disk usage.

**Rollback**: Revert to the previous binary. Since WAL/snapshot are new features, rollback simply means the new binary is replaced with old. WAL files remain on disk but are ignored by the old binary. Snapshots can be deleted.

## Open Questions

- Should WAL be fsync'd on every append, or rely on OS buffering? Current design uses `bufio.Writer` with periodic flush (every 100 records or 100ms). This is a balance between durability and throughput. For full durability, we could add a `Sync()` call on shutdown.
- Do we need a grace period on shutdown to flush the last WAL entries? The current design writes WAL entries synchronously in the actor's command path, so graceful shutdown should flush naturally. `bufio.Writer` will flush on `Close()`.
- Should we add a Prometheus metric for WAL append latency (`matching_wal_append_seconds`)? Yes — align with ROADMAP Phase 4 metric expansion.
