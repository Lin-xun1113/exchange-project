## Context

The current matching engine in `internal/matching/engine/engine.go` uses a workerpool to dispatch work asynchronously, with a global `sync.RWMutex` (line 28) protecting a shared `map[string]*book.OrderBook`. On every `SubmitOrder` call:

1. `GetOrCreateOrderBook` acquires `mu.Lock` to get or create the orderbook for the symbol.
2. `ob.AddOrder` internally acquires `ob.mu.Lock` (the `OrderInBook` mutex at `orderbook.go` line 41).

This double-locking is necessary under a shared-map model but creates two problems: contention across symbols (every goroutine blocks on the global mutex even for unrelated symbols) and obscured ordering guarantees (the mutex semantics don't make price-time priority explicit in the code).

The ROADMAP identifies this as **P1 item ④** ("Per-symbol single goroutine actor, cancel global RWMutex") and lists the per-symbol actor as the recommended model (referencing AsthaMishra/matching-engine and arxiv:2606.01183v3).

## Goals / Non-Goals

**Goals:**

- Each symbol has exactly one dedicated goroutine that owns its `OrderBook` and serializes all mutations.
- Zero `sync.RWMutex` / `sync.Mutex` in the matching engine hot path.
- gRPC handlers remain unary (no proto change); internal hop is channel-based async.
- Actor hop latency < 10 µs under idle conditions.
- Graceful shutdown drains in-flight commands before the process exits.
- Existing engine tests pass (with updated concurrency model).

**Non-Goals:**

- Proto file changes — keep `SubmitOrder` as unary RPC.
- Horizontal scaling of matching across multiple instances (out of scope for this change).
- WAL / Snapshot / crash recovery (separate P2 item ⑦).
- SkipList data structure upgrade (separate P1 item ③, but actor model is compatible with both).
- SPSC ring buffer or lock-free queues — plain `chan` is sufficient at current throughput.

## Decisions

### 1. Keep unary gRPC, internal async hop

**Decision:** The proto stays unchanged as unary. The gRPC handler dispatches a command into the actor's channel and synchronously waits on a per-call result channel.

**Rationale:** Changing to server-streaming or futures-style proto would require updating all clients. The internal async hop (channel dispatch + reply wait) achieves the same actor-model benefits without API churn. The ~1–2 µs channel round-trip is negligible compared to the gRPC wire time.

**Alternatives considered:**
- Server-streaming proto: requires client-side flow control changes; rejected.
- `sync.WaitGroup` in handler: doesn't generalize to per-symbol routing; rejected.

### 2. Remove `sync.RWMutex` from `Matcher` and `sync.Mutex` from `OrderInBook`

**Decision:** Drop both mutexes. The actor goroutine is the sole writer for its `OrderBook`; no concurrent access is possible.

**Rationale:** With one goroutine per symbol, there is no concurrent read/write to the orderbook. The `OrderInBook.mu` at `orderbook.go:41` and `engine.go:28` both disappear. The `map[symbol]*actor` can be safely written lazily at init (or use `sync.Map` for a slightly cleaner read path).

**Alternatives considered:**
- Keep `sync.Map` for actors map with mutex for orderbook: unnecessary indirection; rejected.
- Lazy actor start vs eager: lazy (start actor on first order) avoids spinning up goroutines for inactive symbols; chosen.

### 3. New `actor.go` with `struct actor`

**Decision:** Create `internal/matching/engine/actor.go` containing:

```go
type command struct {
    orderID  string
    userID   int64
    side     model.OrderSide
    price, qty decimal.Decimal
    replyCh  chan<- *MatchResult
}

type actor struct {
    symbol  string
    cmdCh   chan command
    book    *book.OrderBook
    cancel  context.CancelFunc
}
```

The `run()` loop: `for cmd := range cmdCh { result := ob.AddOrder(cmd.order); cmd.replyCh <- result }`.

**Rationale:** Separating the actor struct from `engine.go` keeps the engine lean and makes the actor lifecycle (start, stop, drain) clearly delineated. The command struct carries everything needed for `AddOrder` plus the reply channel.

### 4. `Matcher.getOrCreateActor(symbol)` replaces `GetOrCreateOrderBook`

**Decision:** `getOrCreateActor` lazily starts the actor goroutine on first order for a symbol.

**Rationale:** Inactive symbols (no trading pair opened yet) should not consume goroutines. Lazy start defers goroutine creation until the first order arrives.

**Alternatives considered:**
- Eager start on engine init: wastes goroutines for unused symbols; rejected.
- Actor registry separate from engine: adds indirection; keeping it in `Matcher` is simpler.

### 5. Command result channel with 5s timeout

**Decision:** The gRPC handler waits on the reply channel with a 5-second context deadline. If the actor doesn't reply within 5s, the handler returns an error to the client.

**Rationale:** A stuck actor (e.g., due to a bug in `AddOrder` causing infinite loop) should not hang the gRPC handler forever. The timeout is configurable via `MatchingConfig.ActorTimeout` (default 5s).

### 6. Graceful shutdown: drain before exit

**Decision:** `Matcher.Shutdown()` (called on service stop):
1. Calls `cancel()` on all actor contexts.
2. Closes all `cmdCh` channels.
3. Waits for all actor goroutines to exit (via `sync.WaitGroup`).

**Rationale:** Commands in-flight when shutdown begins should complete normally (the actor processes remaining commands before seeing the closed channel). New commands submitted after shutdown receive an error immediately.

**Rollback:** If shutdown fails to complete within a hard timeout (10s), the process exits anyway — the WAL (when implemented in item ⑦) will recover state.

## Risks / Trade-offs

[Risk] **Actor goroutine leak on symbol with zero orders** → Mitigation: lazy start ensures actors only exist for active symbols. `Shutdown` cleans up all actors.

[Risk] **Actor panic bubbles up as uncaught panic** → Mitigation: the actor `run()` loop wraps `defer func() { if r := recover(); r != nil { log.Error(...) } }()` to contain panics and log them before the goroutine exits. A supervisor could restart the actor (future work).

[Risk] **Channel send blocks if actor is slow** → Mitigation: the handler uses a context with deadline; if the channel send itself would block (actor channel buffer full), the context deadline fires first. The channel buffer size is 1 (unbuffered or capacity 1) — choose based on benchmarks.

[Risk] **Order of magnitude latency regression from channel overhead** → Mitigation: per-symbol actor eliminates the global mutex contention which is the dominant cost at high concurrency. Channel round-trip is ~1 µs; this should be a net improvement at 50+ concurrent orders.

[Risk] **Testing becomes harder without explicit locks** → Mitigation: the command channel makes concurrency testable — inject commands from test goroutines and verify result channel outputs. Add a concurrent stress test (100 goroutines, same symbol) to verify price-time order.

## Open Questions

- **Buffer size for actor channels**: Unbuffered (0) forces synchronous handoff; capacity 1 allows the handler to return immediately if the actor is busy. Benchmark both; start with unbuffered for simplicity.
- **Supervisor / actor restart**: If an actor panics, should it be restarted automatically? For now, log and leave the symbol inactive. Supervisor is future work.
- **Metrics**: Should actor queue depth be exposed as a Prometheus metric (`matching_actor_queue_depth{symbol}`)? Add in P1 item ⑨ (Prometheus expansion).
