## 1. WAL Module

- [x] 1.1 Create `internal/matching/wal/` directory and `wal.go`
- [x] 1.2 Define WAL entry types: `EntryType` (command, cancel, trade), `Entry` struct with LSN, Type, Payload
- [x] 1.3 Implement `WALWriter` interface and `WAL` struct with file handle, mutex, LSN counter
- [x] 1.4 Implement `NewWAL(symbol, dir)` — creates/opens WAL file, reads last LSN
- [x] 1.5 Implement `Append(entry)` — binary framed write with atomic LSN assignment
- [x] 1.6 Implement `LastLSN()` — O(1) tail read by seeking to end
- [x] 1.7 Implement `Replay(fromLSN, handler)` — read entries from fromLSN+1, call handler for each
- [x] 1.8 Implement `Truncate(upToLSN)` — remove entries ≤ upToLSN from WAL file
- [x] 1.9 Register `decimal.Decimal` GobEncoder/GobDecoder (shopspring/decimal built-in)
- [x] 1.10 Add `Close()` method for graceful shutdown flush

## 2. Snapshot Module

- [x] 2.1 Create `internal/matching/snapshot/` directory and `snapshot.go`
- [x] 2.2 Define `Snapshot` struct with Symbol, EndingLSN, Bids, Asks ([]OrderState)
- [x] 2.3 Define `OrderState` struct with all fields from `OrderInBook`
- [x] 2.4 Implement `Take(book *OrderBook, walLSN uint64)` — serialize order book state
- [x] 2.5 Implement `Save(snap, symbol, dir)` — write to temp file, atomic rename, update symlink
- [x] 2.6 Implement `Load(symbol, dir)` — read from symlink target, return Snapshot
- [x] 2.7 Implement `LatestSnapshotPath(symbol, dir)` — resolve symlink to get latest snapshot file
- [x] 2.8 Add `Restore(snap, book)` — re-insert orders into order book for recovery

## 3. Snapshot Trigger Integration

- [x] 3.1 Add snapshot config to engine `Config`: `SnapshotInterval`, `MaxTradesPerSnapshot`, `SnapshotDir`
- [ ] 3.2 Add per-symbol trade counter and timer in engine
- [ ] 3.3 After each `MatchResult` with trades, increment counter and check trigger
- [ ] 3.4 Implement async snapshot goroutine per symbol that checks timer and counter
- [ ] 3.5 On snapshot trigger: take snapshot, save to disk, truncate WAL, reset counter/timer
- [ ] 3.6 Add Prometheus histogram `matching_wal_append_seconds` for WAL latency (align with Phase 4 metrics)

## 4. Engine WAL Injection

- [x] 4.1 Define `WALWriter` interface: `Append(entry)`, `LastLSN()`, `Close()`
- [x] 4.2 Add `walWriter WALWriter` field to `Matcher` struct
- [x] 4.3 Add `SetWALWriter(walWriter WALWriter)` method
- [x] 4.4 Inject WAL write in `SubmitOrder`: write command entry before `dispatch()`
- [x] 4.5 Inject WAL write in `CancelOrder`: write cancel entry before sending to cancelCh
- [x] 4.6 Inject WAL write in `persistTrades`: write trade entry after matching, before async DB write
- [x] 4.7 Pass WAL LSN to snapshot trigger logic

## 5. Startup Recovery in main.go

- [x] 5.1 Load config: `WALDir`, `SnapshotDir`, `SnapshotInterval`, `MaxTradesPerSnapshot`
- [x] 5.2 Before creating matcher, scan `SnapshotDir` for all `*.latest` symlinks
- [x] 5.3 For each symbol with a snapshot: load snapshot, create actor with restored book
- [x] 5.4 Open WAL for each symbol, replay entries from snapshot LSN+1 to current LastLSN
- [x] 5.5 For symbols without snapshot: create actor with empty book, replay all WAL entries
- [x] 5.6 Log recovery summary: symbols recovered, entries replayed, time elapsed
- [x] 5.7 Create matcher with recovered actors, set WAL writer and snapshot config
- [x] 5.8 On graceful shutdown: flush WAL, take final snapshot (optional)

## 6. Testing & Verification

- [ ] 6.1 Write unit tests for WAL: append, LastLSN, Replay, Truncate
- [ ] 6.2 Write unit tests for Snapshot: Take, Save, Load, Restore
- [ ] 6.3 Write integration test: submit orders → crash → restart → verify order book state
- [ ] 6.4 Verify recovery summary log output
