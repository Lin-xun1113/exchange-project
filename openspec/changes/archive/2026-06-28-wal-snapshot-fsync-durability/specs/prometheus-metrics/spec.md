# prometheus-metrics (delta)

Modified to add WAL fsync and Group Commit metrics to the existing WAL Metrics requirement.

## ADDED Requirements

### Requirement: WAL Fsync Metrics
The system SHALL expose fsync-related metrics for monitoring WAL durability behavior.

#### Scenario: Fsync latency recorded
- **WHEN** a WAL fsync operation completes
- **THEN** `matching_wal_fsync_seconds{status="success|failure"}` observes the fsync duration
- **AND** Buckets: `[.00005, .0001, .0005, .001, .005, .01, .025, .05, .1]`

#### Scenario: Pending entries gauge updated
- **WHEN** a WAL entry is appended
- **THEN** `matching_wal_pending_entries` gauge is incremented by 1
- **AND WHEN** an fsync completes
- **THEN** `matching_wal_pending_entries` gauge is decremented by the number of entries synced

#### Scenario: Group size histogram observed
- **WHEN** a WAL fsync completes with N entries pending
- **THEN** `matching_wal_group_size` observes the value N
- **AND** Buckets: `[1, 5, 10, 25, 50, 100, 250, 500, 1000]`

### Requirement: WAL Metrics Label Enhancement
The WAL append metric SHALL include a `sync_mode` label to distinguish performance characteristics.

#### Scenario: Append latency by sync mode
- **WHEN** a WAL append operation completes
- **THEN** `matching_wal_append_seconds{sync_mode="none|always|bycount|byduration"}` observes the append duration
- **AND** The label reflects the current SyncMode of the WAL instance
