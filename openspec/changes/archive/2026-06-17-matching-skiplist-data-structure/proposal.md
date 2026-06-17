## Why

The current `OrderBook` uses `sort.Float64Slice` backed by `[]float64` for bid/ask price levels, which provides sorted traversal but requires O(log n) binary search for best-price lookups and O(n) insertion/removal due to slice manipulation. Switching to a **skip list** preserves the sorted traversal property while achieving O(log n) for all core operations (insert, delete, search, seek), and eliminates the need for slice copying on every price level change. The existing per-symbol actor model (MATCHING-ACTOR-001) guarantees a single-writer goroutine, making the skip list's non-thread-safe implementation safe with minimal overhead.

## What Changes

- Replace `sort.Float64Slice` fields (`bids`, `asks`) in `OrderBook` with `*book.SkipList[float64]`.
- Create `internal/matching/book/skiplist.go`: generic skip list implementation with `Insert`, `Delete`, `Search`, `Seek`, and iterator support.
- Update `addToBookInternal` to use `sl.Seek` + `Insert` for O(log n) price level creation.
- Update `removePriceLevel` to use `sl.Delete` for O(log n) removal.
- Update `GetBestBid` / `GetBestAsk` to use `sl.Seek(math.Inf(-1))` / `sl.Seek(math.Inf(1))` — O(log n) instead of array indexing.
- Update `matchOrders` traversal (if present) to use skip list iterator — `for node := sl.Seek(inf); node != nil; node = node.Next()`.
- Keep `priceLevels map[float64]*PriceLevel` unchanged — it provides O(1) PriceLevel access; skip list provides sorted index.
- Add benchmark test: insert 10K price levels, verify O(log n) timing characteristics.
- No proto or API changes; trade execution order remains unchanged.

## Capabilities

### New Capabilities

- `matching-skiplist`: Skip list replaces `sort.Float64Slice` for price-level indexing in the order book, providing O(log n) insert, delete, search, and seek with O(log n) best bid/ask retrieval.

### Modified Capabilities

- (none — this is an internal data structure upgrade; external matching behavior is unchanged)

## Impact

- **New file**: `internal/matching/book/skiplist.go` — generic skip list
- **Modified files**: `internal/matching/book/orderbook.go` — replace `sort.Float64Slice` with skip list; `internal/matching/book/orderbook_test.go` — update tests and add benchmarks
- **No proto changes**: gRPC API unchanged
- **No persistence changes**: skip list is in-memory only; `priceLevels` map remains the persistence-ready data structure
- **Concurrency**: safe under the existing per-symbol actor model — the actor goroutine is the sole writer; skip list mutex (single `sync.Mutex`) is present but never contended
