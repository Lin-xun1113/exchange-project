## ADDED Requirements

### Requirement: Order Saga Orchestrator SHALL coordinate the full order lifecycle
The Gateway SHALL act as a saga orchestrator, managing the multi-step order workflow through explicit state transitions: Created → Frozen → Submitted → Matched/Cancelled → Settled.

### Requirement: Idempotency key SHALL correlate saga steps
The client-provided `idempotency_key` SHALL be used as the `saga_id` to correlate all steps of an order saga. Duplicate requests with the same idempotency key SHALL return the existing saga result without re-executing steps.

### Requirement: Saga compensation SHALL rollback on failure
If any saga step fails, the orchestrator SHALL execute compensating transactions in reverse order (e.g., unfreeze balance if matching submission fails).

#### Scenario: Successful order creation
- **WHEN** client submits order with idempotency_key="abc123"
- **THEN** saga creates order in "created" state, schedules freeze_balance outbox entry, and returns success

#### Scenario: Duplicate request with same idempotency key
- **WHEN** client submits order with idempotency_key="abc123" while saga for "abc123" is in-progress
- **THEN** saga returns existing saga state with HTTP 409 Conflict

#### Scenario: Matching failure triggers compensation
- **WHEN** matching submission fails after balance freeze
- **THEN** saga executes unfreeze compensation, order status becomes "cancelled", and error is returned to client

### Requirement: Order state machine SHALL enforce valid transitions
Order status SHALL only transition through: created → frozen → submitted → matched/cancelled → settled. Invalid transitions SHALL be rejected at the saga layer.

#### Scenario: Invalid transition rejected
- **WHEN** saga receives matching success callback for order in "created" state (not "submitted")
- **THEN** transition is rejected and error is logged

### Requirement: Saga state SHALL be recoverable after process crash
Saga progress SHALL be persisted to database. After Gateway restart, in-progress sagas with `processed_at` older than threshold SHALL be retried.

#### Scenario: Gateway restart resumes pending saga
- **WHEN** Gateway restarts with pending outbox entries older than 30 seconds
- **THEN** Gateway resumes processing those entries
