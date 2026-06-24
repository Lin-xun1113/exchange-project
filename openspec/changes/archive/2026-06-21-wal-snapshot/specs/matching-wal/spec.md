# Matching WAL

## Purpose

Provides Write-Ahead Logging for the matching engine, appending every matching command and trade result to an append-only binary log before execution. WAL enables crash recovery by providing a complete sequence of state changes that can be replayed to reconstruct the order book.

## Requirements

### Requirement: WAL-001 — WAL appends order submission commands

The WAL SHALL record every order submission command (SubmitOrder) before the matching engine processes it. Each WAL entry SHALL contain the order's symbol, order ID, user ID, side, order type, price, quantity, and a monotonically increasing LSN (Log Sequence Number). The WAL entry SHALL be written synchronously to disk before the command is dispatched to the symbol's actor.

#### Scenario: SubmitOrder writes WAL entry before dispatch

- **WHEN** `SubmitOrder` is called with a valid order
- **THEN** a WAL entry of type `command` is appended to the symbol's WAL file before the command is sent to the actor's command channel

#### Scenario: WAL entry contains all order fields

- **WHEN** `SubmitOrder` is called with symbol="BTCUSDT", orderID="ORD001", userID=42, side=buy, price=50000.00, quantity=1.5
- **THEN** the WAL entry encodes all fields and the LSN is greater than the previous entry's LSN

### Requirement: WAL-002 — WAL appends cancel commands

The WAL SHALL record every cancel command (CancelOrder) before the cancellation is dispatched to the symbol's actor. Each WAL entry SHALL contain the symbol, order ID, and LSN.

#### Scenario: CancelOrder writes WAL entry before dispatch

- **WHEN** `CancelOrder` is called for an existing order
- **THEN** a WAL entry of type `cancel` is appended to the symbol's WAL file before the cancel command is sent to the actor's cancel channel

### Requirement: WAL-003 — WAL appends trade results

The WAL SHALL record every trade result after the matching engine completes processing of an order. Each WAL entry SHALL contain the LSN, all trade details (trade ID, buy order ID, sell order ID, price, quantity, symbol), and the corresponding order ID.

#### Scenario: Trade result written to WAL after matching

- **WHEN** a `SubmitOrder` results in one or more trades
- **THEN** a WAL entry of type `trade` is appended to the symbol's WAL file after `persistTrades` is called, containing all trade details

#### Scenario: No WAL trade entry when no trades occur

- **WHEN** a `SubmitOrder` results in zero trades (order added to book only)
- **THEN** no WAL trade entry is written for this order

### Requirement: WAL-004 — WAL uses binary framed format with LSN

Each WAL record SHALL use a binary framed format: `[8-byte LSN (big-endian uint64)][1-byte type][4-byte payload length (big-endian uint32)][gob-encoded payload]`. The LSN SHALL be assigned atomically at append time using an incrementing counter per symbol WAL file.

#### Scenario: WAL file is readable in order

- **WHEN** a WAL file is opened and records are read sequentially
- **THEN** each record's LSN is greater than the previous record's LSN

#### Scenario: Length prefix enables record skipping

- **WHEN** a WAL file is read and a record is corrupted
- **THEN** the reader can skip the corrupted record by reading the next record's length prefix and advancing the file pointer

### Requirement: WAL-005 — WAL truncation after snapshot

After a snapshot is taken and confirmed written, the WAL SHALL be truncated at the snapshot's ending LSN. All WAL entries with LSN less than or equal to the snapshot's ending LSN SHALL be removed from the WAL file.

#### Scenario: WAL truncated after snapshot

- **WHEN** a snapshot for symbol "BTCUSDT" is written with ending LSN 500
- **THEN** all WAL entries with LSN ≤ 500 are removed from `data/wal/BTCUSDT.wal`

### Requirement: WAL-006 — WAL supports tail read (O(1) last LSN)

The WAL SHALL support O(1) retrieval of the last (highest) LSN by seeking to the end of the file and reading the last complete record backwards. This is used at startup to determine the current WAL end LSN.

#### Scenario: Last LSN retrieved in O(1)

- **WHEN** `LastLSN()` is called on a WAL with N records
- **THEN** the operation completes in O(1) time by seeking to the file end and reading backward, returning the LSN of the last record
