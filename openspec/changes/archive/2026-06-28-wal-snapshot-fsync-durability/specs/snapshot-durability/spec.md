# snapshot-durability

Snapshot durability through atomic file and directory synchronization ensures crash-safe persistence of order book snapshots.

## ADDED Requirements

### Requirement: Snapshot File Sync Before Close
The system SHALL call `file.Sync()` on the snapshot file before closing it to ensure content durability.

#### Scenario: File synced before close
- **WHEN** `snapshot.Save()` writes a snapshot file
- **THEN** `file.Sync()` is called after all data is written and encoded
- **AND THEN** `file.Close()` is called

### Requirement: Parent Directory Sync After Rename
The system SHALL call `dirFile.Sync()` on the parent directory after `os.Rename()` to ensure the directory entry is committed.

#### Scenario: Directory synced after rename
- **WHEN** `snapshot.Save()` renames the temporary snapshot file to its final location
- **THEN** the parent directory is opened with `os.Open(parentDir)`
- **AND THEN** `dirFile.Sync()` is called on the parent directory
- **AND THEN** `dirFile.Close()` is called

#### Scenario: Directory sync failure handling
- **WHEN** `os.Open(parentDir)` returns an error during directory sync
- **THEN** the error is logged as a warning
- **AND THEN** snapshot saving continues (non-fatal)

### Requirement: Snapshot Atomicity
A snapshot save operation SHALL be atomic such that after a crash, either the old snapshot or the new snapshot is visible, never a partial state.

#### Scenario: Crash during snapshot save - before rename
- **WHEN** a crash occurs before `os.Rename()` completes
- **THEN** the `.latest` symlink still points to the previous valid snapshot
- **AND** the temporary `.tmp` file is cleaned up on next startup

#### Scenario: Crash during snapshot save - after rename
- **WHEN** a crash occurs after `os.Rename()` but before directory sync completes
- **THEN** on most filesystems, the rename is durable due to journal
- **AND** the snapshot file content is already synced before rename

### Requirement: Latest Symlink Update Atomicity
The `.latest` symlink update SHALL be atomic using `os.Remove` + `os.Symlink` sequence.

#### Scenario: Symlink update
- **WHEN** `snapshot.Save()` completes successfully
- **THEN** `os.Remove(linkPath)` removes the old symlink
- **AND THEN** `os.Symlink(absTarget, linkPath)` creates the new symlink
- **AND THEN** the `.latest` symlink points to the newly saved snapshot

### Requirement: Snapshot Save Failure Handling
The system SHALL handle snapshot save failures gracefully without blocking order processing.

#### Scenario: Snapshot save failure - file creation
- **WHEN** `os.Create(tmpPath)` returns an error
- **THEN** `snapshot.Save()` returns the error
- **AND THEN** the old snapshot remains intact

#### Scenario: Snapshot save failure - encode
- **WHEN** `gob.NewEncoder(file).Encode(snap)` returns an error
- **THEN** the temporary file is closed and removed
- **AND THEN** `snapshot.Save()` returns the error
- **AND THEN** the old snapshot remains intact

#### Scenario: Snapshot save failure - file sync
- **WHEN** `file.Sync()` returns an error
- **THEN** the temporary file is closed and removed
- **AND THEN** `snapshot.Save()` returns the error
- **AND THEN** the old snapshot remains intact

#### Scenario: Snapshot save failure - rename
- **WHEN** `os.Rename(tmpPath, finalPath)` returns an error
- **THEN** the temporary file is removed
- **AND THEN** `snapshot.Save()` returns the error
- **AND THEN** the old snapshot remains intact
