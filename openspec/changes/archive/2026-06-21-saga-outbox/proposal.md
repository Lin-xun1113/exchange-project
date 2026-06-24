## Why

The current order workflow in `handler.go` (lines 209-264) performs balance freeze, matching submission, and status updates in a synchronous manner. If the matching service fails after balance freeze but before order submission, or if status update fails, the system enters an inconsistent state requiring manual unfreeze via Gateway. This "best-effort" rollback approach creates a window for fund inconsistencies.

## What Changes

- **New Order State Machine**: Introduce explicit states (Created → Frozen → Submitted → Matched/Cancelled → Settled) with transitions enforced by the Saga orchestrator.
- **Saga Orchestrator**: Gateway becomes an orchestrator that coordinates the multi-step order workflow using idempotency keys as correlation IDs across steps.
- **Transactional Outbox Pattern**: Each saga step writes its intended action to an `outbox` table first, then a background worker delivers messages asynchronously. This guarantees at-least-once delivery without distributed transactions.
- **Saga Compensation**: If any step fails, the orchestrator executes compensating transactions (e.g., unfreeze on matching failure) based on recorded saga state.
- **Modified Order Status Enum**: Add `frozen`, `submitted`, `settled` to order status enum.

## Capabilities

### New Capabilities

- `order-saga`: Orchestration-based order workflow where Gateway coordinates balance freeze → matching submit → status update via idempotency-keyed saga steps. Includes compensation logic for rollback on failure.
- `order-outbox`: Transactional outbox pattern implementation. Each saga step writes to `outbox` table within the same DB transaction, and a background worker polls and delivers pending messages to downstream services.

### Modified Capabilities

- `order-management`: Order lifecycle states and transitions now follow explicit state machine enforced by saga orchestrator.

## Impact

- **New files**: `internal/order/saga/order_saga.go`, `internal/order/outbox/outbox.go`, `migrations/002_outbox.sql`
- **Modified files**: `internal/gateway/handler/handler.go` (becomes saga orchestrator), `migrations/001_init_schema.sql` (add order states)
- **Database**: New `outbox` table for transactional outbox pattern
- **Order states**: `orders.status` enum extended with `frozen`, `submitted`, `settled`
- **No proto changes**: External API unchanged; saga orchestration is internal
