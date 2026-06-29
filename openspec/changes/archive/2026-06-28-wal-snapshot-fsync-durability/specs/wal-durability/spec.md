# wal-durability

WAL durability through configurable fsync strategies ensures crash-safe persistence of write-ahead log entries.

## ADDED Requirements

### Requirement: WAL SyncMode Configuration
The WAL SHALL support configurable synchronization modes to balance durability and performance.

#### Scenario: SyncMode types available
- **WHEN** a WAL instance is created
- **THEN** SyncMode must be one of: `SyncNone` (no fsync), `SyncAlways` (every write), `SyncByCount` (every N writes), `SyncByDuration` (after D time elapsed since last sync)

### Requirement: SyncNone Mode
When SyncMode is `SyncNone`, the WAL SHALL NOT call fsync after appending entries.

#### Scenario: SyncNone skips fsync
- **WHEN** SyncMode is `SyncNone` and an entry is appended
- **THEN** only `writer.Flush()` is called; `file.Sync()` is NOT called

### Requirement: SyncAlways Mode
When SyncMode is `SyncAlways`, the WAL SHALL call fsync after every append.

#### Scenario: SyncAlways triggers fsync every write
- **WHEN** SyncMode is `SyncAlways` and an entry is appended
- **THEN** `file.Sync()` is called after `writer.Flush()`

### Requirement: SyncByCount Mode
When SyncMode is `SyncByCount` with threshold N, the WAL SHALL call fsync after N entries have been appended since the last fsync.

#### Scenario: SyncByCount triggers at threshold
- **WHEN** SyncMode is `SyncByCount` with threshold 100 and fewer than 100 entries have been appended since last fsync
- **THEN** fsync is NOT called
- **AND WHEN** 100 entries have accumulated since last fsync
- **THEN** fsync is called and pending counter resets to 0

### Requirement: SyncByDuration Mode (Group Commit)
When SyncMode is `SyncByDuration` with interval D, the WAL SHALL call fsync if D milliseconds have elapsed since the last fsync.

#### Scenario: Group Commit triggers on time interval
- **WHEN** SyncMode is `SyncByDuration` with interval 1ms and fewer than 1ms have passed since last fsync
- **THEN** fsync is NOT called
- **AND WHEN** more than 1ms have elapsed since last fsync
- **THEN** fsync is called and timer resets

### Requirement: WAL Fsync Latency Metrics
The system SHALL expose fsync latency metrics for monitoring WAL performance.

#### Scenario: Fsync latency recorded on sync
- **WHEN** a WAL fsync operation completes successfully
- **THEN** `matching_wal_fsync_seconds{status="success"}` observes the fsync duration
- **AND WHEN** a WAL fsync operation fails
- **THEN** `matching_wal_fsync_seconds{status="failure"}` observes the fsync duration

### Requirement: WAL Pending Entries Gauge
The system SHALL expose the count of unflushed entries for backpressure monitoring.

#### Scenario: Pending entries count exposed
- **WHEN** a WAL entry is appended
- **THEN** `matching_wal_pending_entries` gauge is incremented
- **AND WHEN** an fsync completes successfully
- **THEN** `matching_wal_pending_entries` gauge is decremented by the number of entries synced

### Requirement: WAL Group Size Histogram
The system SHALL expose the number of entries batched per fsync for Group Commit tuning.

#### Scenario: Group size recorded
- **WHEN** a WAL fsync operation completes with N entries pending
- **THEN** `matching_wal_group_size` observes the value N

### Requirement: SyncMode Propagation
The SyncMode SHALL be configurable at WAL creation time and propagated through the engine configuration.

#### Scenario: SyncMode configured via NewWAL
- **WHEN** `NewWAL(symbol, dir, syncMode, syncEvery, syncInterval)` is called
- **THEN** the WAL instance uses the specified sync configuration
- **AND** the SyncMode is stored in the WAL struct

#### Scenario: SyncMode configured via engine.Config
- **WHEN** `engine.Config{SyncMode: SyncByDuration, SyncInterval: 1*time.Millisecond}` is provided to NewMatcher
- **THEN** the WALManager passes this configuration to newly created WAL instances
