# Matching Recovery

## Purpose

Provides startup crash recovery for the matching engine, loading the latest snapshot for each symbol and replaying WAL entries to reconstruct the order book to its exact pre-crash state.

## Requirements

### Requirement: REC-001 — Recovery loads latest snapshot on startup

On startup, the matching engine SHALL load the latest snapshot for each symbol by reading the `data/snapshots/{symbol}.latest` symlink and loading the target snapshot file. If no snapshot exists for a symbol, the engine SHALL start with an empty order book for that symbol.

#### Scenario: Startup with existing snapshot

- **WHEN** the matching service starts and `data/snapshots/BTCUSDT.latest` exists and points to `BTCUSDT-5000.snap`
- **THEN** the engine loads the BTCUSDT order book from that snapshot

#### Scenario: Startup with no snapshot

- **WHEN** the matching service starts and no snapshot exists for symbol "ETHUSDT"
- **THEN** the engine creates an empty order book for "ETHUSDT"

### Requirement: REC-002 — Recovery replays WAL entries since snapshot LSN

After loading the latest snapshot, the engine SHALL replay all WAL entries with LSN greater than the snapshot's ending LSN. Entries SHALL be replayed in LSN order (ascending). After replay, the order book SHALL be in exactly the same state as before the crash.

#### Scenario: WAL replay restores post-snapshot state

- **WHEN** snapshot has ending LSN 5000 and WAL contains entries with LSN 5001 through 5010
- **THEN** after replaying WAL entries 5001-5010, the order book matches the state at LSN 5010

#### Scenario: WAL replay in LSN order

- **WHEN** WAL entries arrive out of order in the file (should not happen, but handled)
- **THEN** entries are replayed in ascending LSN order

### Requirement: REC-003 — Recovery runs before accepting requests

The recovery sequence SHALL complete before the gRPC server starts accepting incoming requests. The matching engine SHALL not process any `SubmitOrder` or `CancelOrder` commands until recovery is complete for all symbols.

#### Scenario: gRPC server starts after recovery

- **WHEN** the matching service starts with a snapshot and 1000 WAL entries to replay
- **THEN** the gRPC server begins accepting connections only after all WAL entries have been replayed

### Requirement: REC-004 — Recovery reports status and errors

The recovery process SHALL log the progress (symbol, snapshot LSN, WAL entries replayed, time taken) and SHALL report any errors (corrupted snapshot, corrupted WAL entry) without crashing. Errors SHALL be logged and recovery SHALL attempt to continue with the next entry or symbol.

#### Scenario: Corrupted WAL entry is skipped

- **WHEN** a WAL entry at LSN 5005 is corrupted (invalid length prefix)
- **THEN** recovery logs an error, skips the corrupted entry, and continues replaying subsequent entries

#### Scenario: Recovery summary logged

- **WHEN** recovery completes for all symbols
- **THEN** a summary log entry is written with total symbols recovered, total WAL entries replayed, and total time elapsed
