## 1. Database Schema

- [x] 1.1 Create migrations/002_outbox.sql with outbox table definition
- [x] 1.2 Add new order states (frozen, submitted, settled) to orders.status enum in 001_init_schema.sql

## 2. Order Model & State Machine

- [x] 2.1 Add new order status constants (Frozen, Submitted, Settled) to internal/model/order.go
- [x] 2.2 Add State() method to Order model for transition validation
- [x] 2.3 Add ValidTransitions map for state machine rules

## 3. Outbox Implementation

- [x] 3.1 Create internal/order/outbox/outbox.go with OutboxEntry model
- [x] 3.2 Implement OutboxRepository with Create, Update, GetPending, GetBySagaID methods
- [x] 3.3 Implement OutboxWorker with Poll, Deliver, Retry logic
- [x] 3.4 Add graceful shutdown support to OutboxWorker

## 4. Saga Orchestrator

- [x] 4.1 Create internal/order/saga/order_saga.go with OrderSaga struct
- [x] 4.2 Implement CreateOrderSaga orchestration steps
- [x] 4.3 Implement compensation methods (UnfreezeOnFailure)
- [x] 4.4 Implement idempotency check with saga_id
- [x] 4.5 Implement saga state recovery on startup

## 5. Gateway Integration

- [x] 5.1 Modify handler.go CreateOrder to use OrderSaga
- [x] 5.2 Extract idempotency key and pass to saga
- [x] 5.3 Add SagaOrchestrator field to OrderHandler
- [x] 5.4 Wire up saga dependencies in gateway main.go

## 6. Testing & Verification

- [x] 6.1 Add unit tests for order state machine
- [x] 6.2 Add unit tests for saga compensation logic
- [x] 6.3 Add unit tests for outbox idempotency
