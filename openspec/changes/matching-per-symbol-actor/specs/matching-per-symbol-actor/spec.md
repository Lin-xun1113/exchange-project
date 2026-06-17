# Matching Per-Symbol Actor

This spec defines the per-symbol actor concurrency model for the matching engine.

## ADDED Requirements

### Requirement: MATCHING-ACTOR-001 — Per-symbol actor goroutine

The matching engine SHALL assign exactly one dedicated actor goroutine to each trading symbol. Each actor owns the `OrderBook` for its symbol and processes all mutations (add, cancel, match) sequentially through its command channel. No other goroutine SHALL directly access the actor's `OrderBook` or command channel.

### Requirement: MATCHING-ACTOR-002 — Actor result equivalence

A `SubmitOrder` call dispatched through the actor channel SHALL produce results equivalent to the current synchronous `ob.AddOrder` call. The actor goroutine hop latency under idle conditions (no contention, empty orderbook) SHALL be less than 10 microseconds.

### Requirement: MATCHING-ACTOR-003 — gRPC handler blocks on actor reply with timeout

The gRPC `SubmitOrder` handler in `matching_server.go` SHALL dispatch the command to the per-symbol actor's command channel and block on a per-call reply channel. The handler SHALL enforce a configurable timeout (default 5 seconds, via `MatchingConfig.ActorTimeout`). If the actor does not reply within the timeout, the handler SHALL return a gRPC error to the client.

#### Scenario: Normal submission succeeds within timeout

- **WHEN** a client calls `SubmitOrder` for a valid order
- **THEN** the handler dispatches the command and returns the `MatchResult` from the actor within the default 5-second timeout

#### Scenario: Actor timeout returns error

- **WHEN** the actor goroutine for the symbol does not reply within 5 seconds
- **THEN** the handler returns a gRPC status `DEADLINE_EXCEEDED` error to the client

### Requirement: MATCHING-ACTOR-004 — Graceful shutdown drains in-flight commands

When `Matcher.Shutdown()` is called, it SHALL cancel all actor contexts, close all command channels, and wait for all actor goroutines to drain before returning. In-flight commands SHALL be processed to completion before the actor exits. New commands submitted after shutdown begins SHALL receive an error immediately.

#### Scenario: Shutdown drains pending commands

- **WHEN** `Shutdown()` is called while actors are processing commands
- **THEN** each actor processes its remaining in-flight commands and exits gracefully

#### Scenario: New command after shutdown returns error

- **WHEN** a `SubmitOrder` call is made after `Shutdown()` has been called
- **THEN** the handler returns a gRPC error indicating the service is shutting down
