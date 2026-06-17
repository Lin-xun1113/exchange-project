## 1. New `actor.go` — actor struct and command types

- [ ] 1.1 Define `command` struct in `internal/matching/engine/engine.go` (or `actor.go`): fields `orderID`, `userID`, `side model.OrderSide`, `price, qty decimal.Decimal`, `replyCh chan<- *MatchResult`
- [ ] 1.2 Define `actor` struct: fields `symbol string`, `cmdCh chan command`, `book *book.OrderBook`, `cancel context.CancelFunc`, `wg *sync.WaitGroup`
- [ ] 1.3 Implement `actor.run()` loop: `for cmd := range cmdCh { result := ob.AddOrder(cmd.order); cmd.replyCh <- result }` with `defer wg.Done()` and `defer close(cmdCh)` on exit
- [ ] 1.4 Wrap `run()` body in `defer func() { if r := recover(); r != nil { log.Error(...) } }()` to contain panics

## 2. Remove mutexes from existing engine and orderbook

- [ ] 2.1 Remove `mu sync.RWMutex` and `books map[string]*book.OrderBook` from `Matcher` in `engine.go`; replace with `actors map[string]*actor` (use `sync.Map` or plain map with mutex)
- [ ] 2.2 Remove `workers *workerpool.WorkerPool` from `Matcher` — worker pool no longer needed
- [ ] 2.3 Remove `GetOrCreateOrderBook` method entirely
- [ ] 2.4 Remove `OrderInBook.mu sync.Mutex` from `orderbook.go` line 41; remove all `mu.Lock()/mu.Unlock()` calls in `OrderInBook` and `Fill()`
- [ ] 2.5 Add `actorTimeout time.Duration` field to `MatchingConfig`

## 3. Implement per-symbol actor lifecycle in `Matcher`

- [ ] 3.1 Implement `Matcher.getOrCreateActor(symbol string) *actor`: lazily creates actor goroutine if not exists; uses `sync.Map` or a small mutex-protected map for `actors` map
- [ ] 3.2 In `getOrCreateActor`: create `cmdCh := make(chan command)` (unbuffered or capacity 1), create `*book.OrderBook`, start goroutine `go a.run()` with context
- [ ] 3.3 Implement `Matcher.dispatch(cmd command) *MatchResult`: calls `getOrCreateActor(symbol)`, sends `cmd` on `act.cmdCh`, selects on `cmd.replyCh` and context deadline; returns error on timeout
- [ ] 3.4 Update `Matcher.SubmitOrder` to call `dispatch` instead of the current lock-then-call path
- [ ] 3.5 Add `Matcher.Shutdown(ctx context.Context) error`: cancel all actor contexts, close all channels, wait for all goroutines via `wg.Wait()`

## 4. Update gRPC handler to use actor dispatch

- [ ] 4.1 Update `SubmitOrder` in `internal/matching/server/matching_server.go`: dispatch command to actor channel, wait on reply channel with configured timeout
- [ ] 4.2 Return gRPC `status.DEADLINE_EXCEEDED` if timeout fires; return appropriate error if matcher is shutting down

## 5. Update tests

- [ ] 5.1 Update `internal/matching/engine/engine_test.go`: remove `TestMatcher_SubmitOrderAsync` worker-pool-based test; replace with actor-channel-based async test
- [ ] 5.2 Add new test: `TestMatcher_ConcurrentSameSymbol` — 100 goroutines submitting to the same symbol concurrently, verify all trades are in price-time order
- [ ] 5.3 Run `go test ./internal/matching/...` — all tests must pass

## 6. Remove workerpool dependency (optional, verify no other use)

- [ ] 6.1 Grep for any remaining references to `workerpool` in `internal/matching/`; if only used by engine, leave workerpool package in place for future use
