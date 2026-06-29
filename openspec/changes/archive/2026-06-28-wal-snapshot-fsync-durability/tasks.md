# WAL and Snapshot Durability Implementation Tasks

## 1. WAL SyncMode Infrastructure

- [ ] 1.1 Add SyncMode type and constants (SyncNone, SyncAlways, SyncByCount, SyncByDuration) to `internal/matching/wal/wal.go`
- [ ] 1.2 Add sync fields to WAL struct: syncMode, syncEvery, syncInterval, pendingSyncs, lastSyncTime
- [ ] 1.3 Implement shouldSync() method that returns true based on SyncMode conditions
- [ ] 1.4 Update NewWAL signature to accept SyncMode and sync configuration parameters

## 2. WAL Append fsync Integration

- [ ] 2.1 Modify WAL.Append to call file.Sync() when shouldSync() returns true
- [ ] 2.2 Add fsync latency measurement using time.Since(start)
- [ ] 2.3 Reset pendingSyncs counter and update lastSyncTime after successful fsync
- [ ] 2.4 Handle fsync error gracefully (log + metric, do not block)

## 3. Snapshot Save fsync Integration

- [ ] 3.1 Add file.Sync() call before file.Close() in snapshot.Save()
- [ ] 3.2 Add parent directory open and dirFile.Sync() after os.Rename()
- [ ] 3.3 Handle directory sync failure with warning log (non-fatal)

## 4. Metrics Registration

- [ ] 4.1 Add matching_wal_fsync_seconds histogram to Metrics struct
- [ ] 4.2 Add matching_wal_pending_entries gauge to Metrics struct
- [ ] 4.3 Add matching_wal_group_size histogram to Metrics struct
- [ ] 4.4 Update matching_wal_append_seconds to include sync_mode label
- [ ] 4.5 Implement RecordWALFsyncLatency(duration, status) method
- [ ] 4.6 Implement SetWALPendingEntries(count) and IncWALPendingEntries() methods
- [ ] 4.7 Implement RecordWALGroupSize(size) method

## 5. Engine Config Propagation

- [ ] 5.1 Add SyncMode and sync config fields to engine.Config struct
- [ ] 5.2 Update WALManager to accept and store sync config
- [ ] 5.3 Modify WALManager.GetWAL to pass sync config to NewWAL
- [ ] 5.4 Update NewMatcher to set WALManager with sync config from Config

## 6. Application Configuration

- [ ] 6.1 Add WAL sync mode and interval environment variables to main.go
- [ ] 6.2 Add WAL_SYNC_MODE (none|always|bycount|byduration) env var parsing
- [ ] 6.3 Add WAL_SYNC_INTERVAL_MS env var for SyncByDuration
- [ ] 6.4 Add WAL_SYNC_EVERY env var for SyncByCount
- [ ] 6.5 Wire sync config to engine.Config when creating matcher

## 7. Testing

- [ ] 7.1 Add unit tests for SyncMode.SyncNone - verify no fsync called
- [ ] 7.2 Add unit tests for SyncMode.SyncAlways - verify fsync on every append
- [ ] 7.3 Add unit tests for SyncMode.SyncByCount - verify fsync after N appends
- [ ] 7.4 Add unit tests for SyncMode.SyncByDuration - verify fsync after D time
- [ ] 7.5 Add integration test for snapshot.Save - verify file.Sync and dir.Sync calls
- [ ] 7.6 Run `go test ./internal/matching/...` and ensure all tests pass
- [ ] 7.7 Run `go build ./...` and ensure no compilation errors

## 8. Documentation

- [ ] 8.1 Update docs/interview/03-wal-snapshot-recovery.md - remove "bufio.Flush() equals fsync" error
- [ ] 8.2 Add "Persistence Levels L1-L3" section explaining durability guarantees
- [ ] 8.3 Add "Group Commit" section explaining 1ms window mechanism
- [ ] 8.4 Update Snapshot Save flow diagram to show file.Sync + dir.Sync
- [ ] 8.5 Document the three new Prometheus metrics
