## ADDED Requirements

### Requirement: Outbox table SHALL record all saga actions in the same transaction as state changes
Each saga step SHALL write its action to the `outbox` table within the same database transaction that updates order state. This ensures atomicity — if the transaction commits, the action is recorded; if it rolls back, no orphan action exists.

### Requirement: Outbox worker SHALL deliver pending messages asynchronously
A background worker SHALL poll the `outbox` table for entries with `status='pending'`, deliver the payload to the target service, and update `status='done'` on success or `status='failed'` on persistent failure.

#### Scenario: Successful message delivery
- **WHEN** outbox worker picks up pending freeze_balance entry
- **THEN** worker calls UserService.FreezeAmount, marks entry status='done', and sets processed_at

#### Scenario: Delivery failure triggers retry
- **WHEN** UserService is unavailable during freeze_balance delivery
- **THEN** worker increments retry_count, marks status='failed', and retries after backoff

### Requirement: Outbox entries SHALL be idempotent
If the same outbox entry is delivered more than once (e.g., worker crash mid-delivery), downstream handlers SHALL treat it as idempotent using the saga_id and step_name as deduplication key.

### Requirement: Outbox worker SHALL process entries in saga_id + created_at order
Entries for the same saga SHALL be processed sequentially to maintain order. Different sagas MAY be processed concurrently.

### Requirement: Stale pending entries SHALL be detected and retried
Entries with `status='pending'` and `created_at` older than configurable threshold (default 30s) SHALL be retried by the health-check goroutine.

### Requirement: Failed entries SHALL exhaust retries before alerting
Entries that fail more than `max_retries` (default 5) SHALL be marked `status='dead_letter'` and an alert SHALL be emitted for manual intervention.

#### Scenario: Dead letter after max retries
- **WHEN** outbox entry fails 5 consecutive delivery attempts
- **THEN** entry status becomes 'dead_letter', error is logged with full context, and alert is emitted
