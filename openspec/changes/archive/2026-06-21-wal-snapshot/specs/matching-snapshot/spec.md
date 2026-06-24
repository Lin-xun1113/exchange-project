# Matching Snapshot

## Purpose

Provides periodic async snapshots of the order book state, enabling crash recovery to reconstruct the order book from a known good point-in-time state plus WAL replay.

## Requirements

### Requirement: SNAP-001 — Snapshot captures full order book state

The snapshot SHALL capture the complete state of a symbol's order book at the time of snapshot. The snapshot SHALL include: symbol name, LSN (the last WAL LSN at snapshot time), all active bids with their price, quantity, order ID, user ID, and timestamp, and all active asks with the same fields.

#### Scenario: Snapshot includes all active orders

- **WHEN** a snapshot is taken for symbol "BTCUSDT" with 10 active bids and 5 active asks
- **THEN** the snapshot file contains all 15 orders with their full state

#### Scenario: Snapshot includes LSN

- **WHEN** a snapshot is taken while WAL LSN is 1234
- **THEN** the snapshot records LSN 1234 as its ending LSN

### Requirement: SNAP-002 — Snapshot triggered by trade count

The snapshot SHALL be triggered automatically when the number of trades executed for a symbol exceeds `MaxTradesPerSnapshot` (default: 1000) since the last snapshot. The trade counter SHALL be reset to zero after each snapshot.

#### Scenario: Snapshot triggered at trade count threshold

- **WHEN** 1000 trades have been executed for symbol "BTCUSDT" since the last snapshot
- **THEN** a new snapshot is triggered for "BTCUSDT"

#### Scenario: Trade counter reset after snapshot

- **WHEN** a snapshot is taken for "BTCUSDT"
- **THEN** the trade counter for "BTCUSDT" resets to 0

### Requirement: SNAP-003 — Snapshot triggered by time interval

The snapshot SHALL be triggered automatically when `SnapshotInterval` (default: 60 seconds) elapses since the last snapshot for a symbol. The timer SHALL be reset after each snapshot.

#### Scenario: Snapshot triggered by time interval

- **WHEN** 60 seconds have elapsed since the last snapshot for "BTCUSDT" and no snapshot has been taken in that interval
- **THEN** a new snapshot is triggered for "BTCUSDT"

### Requirement: SNAP-004 — Snapshot written atomically

The snapshot SHALL be written to a temporary file first, then renamed to the final path. This ensures that if the process crashes during snapshot write, the previous snapshot remains valid and is not corrupted.

#### Scenario: Crash during snapshot write leaves previous snapshot intact

- **WHEN** a snapshot write is in progress and the process crashes
- **THEN** the previous snapshot file at `data/snapshots/{symbol}.latest` remains valid and readable

#### Scenario: Snapshot rename is atomic on POSIX

- **WHEN** the temporary snapshot file has been fully written
- **THEN** it is renamed to the final path and the `.latest` symlink is updated

### Requirement: SNAP-005 — Snapshot named with LSN, symlink points to latest

The snapshot file SHALL be named `{symbol}-{lsn}.snap` (e.g., `BTCUSDT-5000.snap`). A symlink `data/snapshots/{symbol}.latest` SHALL point to the most recent snapshot file. The symlink target uses the absolute path of the snapshot file.

#### Scenario: Latest snapshot identified via symlink

- **WHEN** the latest snapshot for "BTCUSDT" has LSN 5000
- **THEN** `data/snapshots/BTCUSDT.latest` points to `data/snapshots/BTCUSDT-5000.snap`

### Requirement: SNAP-006 — Snapshot loading restores order book state

Loading a snapshot SHALL restore the order book to exactly the state captured at snapshot time. All active orders from the snapshot SHALL be re-inserted into the order book in the same order and with the same prices, quantities, and statuses.

#### Scenario: Order book restored from snapshot

- **WHEN** a snapshot with LSN 5000 is loaded
- **THEN** the order book contains exactly the orders that were present at LSN 5000, with the same prices, quantities, and statuses
