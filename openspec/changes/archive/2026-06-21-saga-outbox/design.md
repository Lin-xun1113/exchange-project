## Context

The current order creation flow in `handler.go` (lines 179-290) executes balance freeze, matching submission, and status update synchronously. When matching fails after freeze (line 256-261), the handler attempts best-effort unfreeze, but if the HTTP response fails mid-way or the service crashes, the balance remains frozen with no order record.

Order state is tracked via a simple enum (`pending`, `partial_filled`, `filled`, `cancelled`, `rejected`) with no explicit state machine enforcing valid transitions.

## Goals / Non-Goals

**Goals:**
- Eliminate inconsistent state where balance is frozen but order is not created or not submitted.
- Provide explicit, auditable saga steps with compensation logic for rollback.
- Guarantee at-least-once delivery of saga actions using transactional outbox.
- Track full order lifecycle: Created → Frozen → Submitted → Matched/Cancelled → Settled.

**Non-Goals:**
- Replace gRPC communication between services (remains unary).
- Implement distributed saga with multiple orchestrator instances (single instance first).
- Full 2PC distributed transaction (outbox is at-least-once, not exactly-once).

## Decisions

### 1. Order State Machine

Add explicit states to `orders.status` enum:
```
created   --freeze--> frozen --submit--> submitted --match/cancel--> matched/cancelled --settle--> settled
```

State transitions are enforced by the Saga orchestrator. Direct DB updates bypassing the saga are rejected at application level (by convention, not constraint).

**Alternatives considered:**
- *Stateless design*: Keep status as free-form string, validate transitions in service layer. Rejected — error-prone, hard to audit.
- *Event sourcing*: Emit events on every transition, rebuild state from event log. Overkill for this system.

### 2. Outbox Table Schema

```sql
CREATE TABLE outbox (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    saga_id VARCHAR(64) NOT NULL,
    step_name VARCHAR(64) NOT NULL,
    action_type VARCHAR(32) NOT NULL,   -- 'freeze_balance', 'submit_matching', 'update_status'
    payload JSON NOT NULL,
    status ENUM('pending', 'processing', 'done', 'failed') DEFAULT 'pending',
    retry_count INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMP NULL,
    INDEX idx_saga_id (saga_id),
    INDEX idx_status (status),
    INDEX idx_created_at (created_at)
);
```

**Design rationale:** `saga_id` correlates all steps in one saga. `step_name` provides ordering and idempotency. `payload` stores the gRPC request to be delivered. `status` tracks delivery progress.

**Alternatives considered:**
- *Redis Streams as outbox*: Lower latency, but loses durability if Redis restarts before delivery confirmation. MySQL outbox is simpler and co-located with order data.
- *Event sourcing store*: Too generic, adds complexity.

### 3. Saga Orchestrator Design

The Saga orchestrator (`internal/order/saga/order_saga.go`) implements the following workflow:

```
CreateOrderSaga(user_id, idempotency_key, order_details)
  1. BEGIN TX
     - Insert order record with status='created'
     - Insert outbox entry: {action: 'freeze_balance', payload}
  2. COMMIT
  3. Worker picks up 'freeze_balance', calls UserService.FreezeAmount
  4. On success: Update order status='frozen', insert outbox: {action: 'submit_matching'}
  5. Worker picks up 'submit_matching', calls MatchingService.SubmitOrder
  6. On success: Update order status='submitted'
  7. Matching async callback or polling: Update order status='matched'/'cancelled'
  8. On failure at any step: Execute compensating transaction (e.g., unfreeze)
```

Each saga step is idempotent — if the outbox worker crashes after delivery but before marking `done`, it retries with the same payload. The saga state is recoverable from `outbox` + `orders` tables.

**Alternatives considered:**
- *Choreography-based saga*: Each service emits events that trigger the next step. Simpler initially but leads to distributed implicit coupling. Orchestrator is more explicit and easier to debug.
- *Eventuate Tram or similar framework*: Adds external dependency. Custom implementation is lightweight and purpose-built.

### 4. Idempotency Strategy

`idempotency_key` from the client request becomes the `saga_id`. If a second request arrives with the same key:
1. Check `outbox` table for existing `saga_id`.
2. If found and completed, return cached result.
3. If found and in-progress, wait or return `409 Conflict`.

The idempotency key is stored in both `orders.idempotency_key` and `outbox.saga_id`.

### 5. Gateway as Orchestrator

`handler.go` `CreateOrder` becomes:
1. Receive request, extract `idempotency_key`.
2. Call `saga.CreateOrderSaga(ctx, req)` which manages the full lifecycle.
3. Return immediately with saga state (or pending status).
4. Long-running saga steps are handled asynchronously by outbox workers.

**Alternatives considered:**
- *Dedicated saga service*: More separation but adds a new service to manage. Gateway-as-orchestrator keeps logic co-located.

## Risks / Trade-offs

[Risk] **Outbox worker crash during processing**: Worker picks up a message, calls downstream service, crashes before marking `done`. On restart, it re-processes the same message.
→ **Mitigation**: Idempotent downstream handlers + `processed_at` column to detect duplicates. Retry with exponential backoff.

[Risk] **Saga in inconsistent state if DB commit succeeds but outbox insert fails**: Order is created but no outbox entry means the freeze will never happen.
→ **Mitigation**: Both inserts happen in the same DB transaction. If either fails, both roll back.

[Risk] **Gateway crashes during saga execution**: Saga state is in DB, so it survives Gateway restart. On restart, scan `outbox` for `saga_id` with `status='processing'` older than threshold and retry.
→ **Mitigation**: Periodic health check scans for stale in-progress sagas.

[Risk] **Compensation failure**: If compensation (e.g., unfreeze) fails, balance remains frozen.
→ **Mitigation**: Retry compensation with backoff. Alert on repeated failure. Manual intervention threshold.

## Migration Plan

1. **Phase 1 — Schema**: Run `migrations/002_outbox.sql` to add `outbox` table and extend `orders.status` enum. No behavior change yet.
2. **Phase 2 — Saga package**: Add `internal/order/saga/` and `internal/order/outbox/` packages. Implement saga logic alongside existing handler. Feature flag `use_saga=true` gates new path.
3. **Phase 3 — Gateway migration**: Flip feature flag. Monitor for issues. Roll back by reverting flag.
4. **Phase 4 — Cleanup**: Remove old handler logic paths once saga path is stable.

## Open Questions

1. Should saga state be stored in a separate `saga_instances` table for observability, or is `outbox` + `orders` sufficient?
2. How to handle timeout on the client side if saga takes longer than expected (e.g., matching takes 30s)?
3. Should matching service push completion events, or should the saga poll for order status?
