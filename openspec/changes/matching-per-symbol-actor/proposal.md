## Why

The current matching engine uses a workerpool with a global `sync.RWMutex` protecting a shared `map[string]*OrderBook`. Every `SubmitOrder` call acquires the mutex, then the orderbook's own mutex — double locking that serializes contention across all symbols. This model cannot deliver sub-microsecond per-symbol throughput, and the lock-free ordering guarantees required for price-time priority are obscured by mutex semantics. Switching to a per-symbol actor model eliminates both mutexes entirely, reduces latency, and makes the concurrency model explicit.

## What Changes

- Replace `sync.RWMutex` + workerpool in `Matcher` with a `map[symbol]*actor`, one goroutine per symbol.
- Each actor owns its `OrderBook` and processes commands from a dedicated channel (`chan command`), serializing all mutations.
- gRPC handler dispatches a `command` struct into the actor's channel and waits synchronously on a per-call reply channel with a 5-second timeout.
- Remove `sync.RWMutex` from `Matcher` (line 28 of `engine.go`) and `sync.Mutex` from `OrderInBook` (line 41 of `orderbook.go`) — actor goroutine is the sole writer.
- Add graceful shutdown: `Matcher.Shutdown()` cancels context, closes channels, waits for goroutines to drain.
- Update engine tests to use actor-channel-based concurrency instead of worker pool.
- Keep `api/proto/matching/v1/matching.proto` unchanged (remains unary).

## Capabilities

### New Capabilities

- `matching-per-symbol-actor`: Per-symbol actor goroutine owns the OrderBook; all mutations (add, cancel, match) are serialized through the actor's command channel. gRPC handlers dispatch commands and block on a result channel with configurable timeout.

### Modified Capabilities

- (none — this is an internal implementation change; no external API or spec-level behavior changes)

## Impact

- **Code**: `internal/matching/engine/engine.go`, `internal/matching/engine/actor.go` (new), `internal/matching/book/orderbook.go`, `internal/matching/server/matching_server.go`, `internal/matching/engine/engine_test.go`
- **No proto changes**: gRPC remains unary; the internal hop is an implementation detail
- **Concurrency model**: drops two `sync.Mutex`/`sync.RWMutex` instances; introduces channel-based dispatch
- **Latency**: actor hop target < 10 µs; total end-to-end p99 target to be validated by benchmark
