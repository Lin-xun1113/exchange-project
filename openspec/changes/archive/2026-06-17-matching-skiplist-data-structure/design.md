## Context

The current `OrderBook` in `internal/matching/book/orderbook.go` uses `sort.Float64Slice` to back bid and ask price levels. Each price level maps to a `*PriceLevel` via `priceLevels map[float64]*PriceLevel`. The two data structures play distinct roles:

- `bids` / `asks` (`sort.Float64Slice`): sorted index for traversal and best-price lookup
- `priceLevels`: O(1) access to the `PriceLevel` struct by price

Operations on `sort.Float64Slice` (`sort.Insert`, `sort.Remove`) require O(n) slice copying on every price level change. The `sort.Search` for best bid/ask is O(log n) but the underlying sorted array creates per-operation allocation overhead as levels are added/removed.

The per-symbol actor goroutine (MATCHING-ACTOR-001) guarantees a single writer for each `OrderBook`, so any non-concurrent data structure is safe to use — no external locking is needed beyond what the skip list itself carries internally.

The ROADMAP identifies this as **P1 item ③** ("Skip list or red-black tree for price levels").

## Goals / Non-Goals

**Goals:**

- Replace `sort.Float64Slice` with a skip list providing O(log n) insert, delete, search, and seek.
- Best bid/ask lookup in O(log n) via `Seek`.
- Sorted traversal of price levels for `matchOrders` via the skip list iterator.
- Keep `priceLevels map[float64]*PriceLevel` unchanged — it remains the authoritative data structure.
- No API or proto changes; existing tests continue to pass.
- Add benchmark verifying O(log n) timing for 10K price levels.

**Non-Goals:**

- Replacing the `priceLevels` map itself — it stays as-is for O(1) price-level access.
- Thread-safety beyond what the actor model already guarantees — skip list mutex is single-writer safe.
- Persistence — skip list is in-memory only.
- Balancing/rebalancing of tree structures — skip list is self-balancing by design.
- Supporting duplicate price keys — skip list seek-then-compare handles price level deduplication.

## Decisions

### 1. Skip list over red-black tree or B-tree

**Decision:** Use a skip list with `MAX_LEVEL = 32` and `P = 1/e` as the primary sorted index.

**Rationale:** Skip lists provide O(log n) expected performance for all core operations with simpler implementation than a balanced tree. The probabilistic leveling eliminates the need for explicit rebalancing rotations. At 10K price levels, max depth is 32 (log₂(2^32) ≈ 32), matching the worst-case skip list depth. The "expected" O(log n) bound holds with high probability; Go's `math/rand` is sufficient for non-adversarial inputs.

**Alternatives considered:**
- Red-black tree: guaranteed O(log n) worst case but more complex implementation; rejected for simplicity.
- B-tree: better cache locality but more complex delete logic; rejected for simplicity.
- `sort.Float64Slice` with `sort.Search`: already the current approach; O(log n) search but O(n) insert/remove; rejected.

### 2. Generic `SkipList[K any]` with `Less func(k1, k2 int) bool` comparator

**Decision:** The skip list is generic over the key type `K` with a `Less` function for ordering.

```go
type SkipList[K any] struct {
    less   func(k1, k2 K) bool
    header *SkipListNode[K]
    len    int
    mu     sync.Mutex
}

type SkipListNode[K any] struct {
    Key    K
    Forward []*SkipListNode[K] // slice of size = node's level
}
```

For `SkipList[float64]`, `less = func(a, b float64) bool { return a < b }`.

**Rationale:** A generic implementation avoids code duplication for future sorted collections (e.g., time-ordered queues). The comparator pattern is idiomatic Go (mirrors `sort.Interface`). The `Less` function is captured at construction time — no interface allocation per comparison.

### 3. Single `sync.Mutex` for write operations

**Decision:** The skip list has a single `sync.Mutex` protecting all mutations. Read-only operations (`Search`, `Seek`, iterator) are lock-free under the actor model since the actor goroutine is the sole accessor.

**Rationale:** Under the per-symbol actor model, there is only one goroutine writing to the skip list. The mutex exists for correctness if the skip list is ever accessed from outside the actor, but it never sees contention in the current architecture. Lock-free reads (no mutex) are safe because the actor processes commands serially — each `AddOrder` call sees a consistent snapshot of the skip list.

**Alternatives considered:**
- `sync.RWMutex`: unnecessary — no concurrent reads in the current model.
- Lock-free skip list: significant complexity for negligible gain; rejected.

### 4. `Seek(key K) *SkipListNode[K]` returns first node with key >= given key

**Decision:** `Seek` returns the first node in the skip list whose key is greater than or equal to the provided key. This is the lower-bound semantics used in binary search.

**Rationale:** `Seek` is the core operation for both best-price lookup and iteration:
- Best bid (highest price ≤ ∞): `sl.Seek(math.Inf(+1))` returns the last node (highest key).
- Best ask (lowest price ≥ -∞): `sl.Seek(math.Inf(-1))` returns the first node (lowest key).
- Matching traversal: `sl.Seek(math.Inf(-1))` for asks (ascending), `sl.Seek(math.Inf(+1))` for bids (descending).

**Alternatives considered:**
- Separate `SeekFirst` / `SeekLast` methods: redundant with `Seek(±Inf)`; rejected.
- `SeekGTE` / `SeekGT`: `Seek` with lower-bound semantics is sufficient; rejected.

### 5. `Insert(key K) *SkipListNode[K]` returns the node (existing or new)

**Decision:** `Insert` first calls `Seek(key)`. If the found node's key equals `key`, return the existing node. Otherwise, insert a new node at the appropriate levels.

**Rationale:** This pattern naturally handles duplicate price levels — the caller gets the existing node and adds orders to the same `PriceLevel` without creating a second entry. No separate "find or insert" API is needed.

### 6. `Delete(key K) bool` removes by exact key match

**Decision:** `Delete` removes the node with the exact given key and returns whether a node was found and removed.

**Rationale:** `Delete` and `removePriceLevel` in `orderbook.go` are called in coordinated pairs: `sl.Delete(price)` removes from the skip list, then `delete(priceLevels, price)` removes the map entry. This keeps the two data structures in sync.

### 7. Iterator via closure over `Seek`

**Decision:** The iterator is a simple `Iter()` method returning a `*SkipListIterator[K]` that supports `Next()` traversal from any starting node.

```go
func (sl *SkipList[K]) Seek(key K) *SkipListNode[K]
func (sl *SkipList[K]) SeekFirst() *SkipListNode[K] // = Seek(-∞)
func (sl *SkipList[K]) SeekLast() *SkipListNode[K]  // = Seek(+∞)
func (n *SkipListNode[K]) Next() *SkipListNode[K]
```

**Rationale:** A closure-based iterator avoids interface allocation. The caller uses `Seek(±Inf)` to get the traversal start and `node.Next()` to iterate. The `SeekFirst` / `SeekLast` helpers are provided for clarity in the matching loop.

### 8. `MAX_LEVEL = 32`, `P = 1/e`

**Decision:** Max level is 32 (supports up to ~4 billion nodes), probability of level promotion is `1/e ≈ 0.3679`.

**Rationale:** 32 levels is the standard skiplist max (used by Redis, LevelDB). With 10K price levels, expected max level is `ln(10000)/ln(1/P) ≈ 9`, well within bounds. The `P = 1/e` value minimizes the expected number of forward pointers per node.

### 9. `OrderBook.bids` and `OrderBook.asks` become `*SkipList[float64]`

**Decision:** Replace `bids sort.Float64Slice` and `asks sort.Float64Slice` with `bids *SkipList[float64]` and `asks *SkipList[float64]`.

**Rationale:** The skip list replaces the sorted index role of `sort.Float64Slice`. The `priceLevels map[float64]*PriceLevel` remains unchanged — it continues to provide O(1) access to the `PriceLevel` struct for any price. The skip list tracks only the set of prices that have non-empty price levels.

### 10. `removePriceLevel` uses `sl.Delete` before `delete(priceLevels, price)`

**Decision:** `removePriceLevel` calls `sl.Delete(price)` to remove from the skip list, then removes from `priceLevels` separately.

**Rationale:** This keeps the two data structures in sync. The order of operations (delete from skip list first, then from map) is safe — if the process crashes between the two, the price level is orphaned in the map but never accessible since it's no longer in the skip list's traversal path.

## Risks / Trade-offs

[Risk] **Expected O(log n) vs guaranteed O(log n) of a balanced tree** → The skip list's performance bound is probabilistic. For adversarial inputs (e.g., attacker injecting specially crafted price sequences), the tree depth could degrade. Mitigation: use `math/rand` with a global lock-safe seed; for non-adversarial trading inputs, the expected bound holds reliably. If worst-case guarantees are required in the future, switch to a red-black tree.

[Risk] **Actor mutex now protects skip list writes** → The skip list's internal mutex could theoretically contend if code outside the actor accesses it. Mitigation: all skip list mutations go through the actor command channel; no code outside the actor calls `Insert`/`Delete`. The mutex is present but never contended.

[Risk] **`math.Inf` values in `Seek` calls** → `sl.Seek(math.Inf(-1))` for best ask and `sl.Seek(math.Inf(+1))` for best bid rely on the skip list's comparison function. For bids (highest first), `math.Inf(+1)` is the maximum possible key — `Seek` will return the last node. Mitigation: verify this works correctly in unit tests.

[Risk] **Breakage of existing tests that assume `sort.Float64Slice` ordering** → Tests like `TestOrderBook_GetBestBidAsk` rely on `buyOrders[0]` being the best bid. With the skip list, the best bid is `sl.SeekLast()`. Mitigation: update `GetBestBid` and `GetBestAsk` to use skip list seek; tests calling `GetDepth` should still work since the skip list iteration returns prices in sorted order.

## Migration Plan

1. Create `internal/matching/book/skiplist.go` with the generic skip list implementation and comprehensive unit tests.
2. Verify skiplist tests pass: `go test ./internal/matching/book/... -run SkipList`.
3. Update `OrderBook` struct in `orderbook.go` to replace `sort.Float64Slice` with `*SkipList[float64]`.
4. Update `addToBookInternal`, `removePriceLevel`, `GetBestBid`, `GetBestAsk`, and `matchOrders` to use skip list methods.
5. Run existing orderbook tests: `go test ./internal/matching/book/...` — all should pass.
6. Add benchmark test: `BenchmarkSkipListInsert_10KLevels` verifying O(log n) timing.
7. Run full matching test suite: `go test ./internal/matching/...`.
8. **Rollback**: If issues arise, revert to `sort.Float64Slice` in `orderbook.go` — the skip list file can be left in place as a standalone utility.

## Open Questions

- **Should the skip list expose a `Len()` method?** Yes — useful for `OrderBook.Size()` if added later.
- **Should `SkipList` support a `Range(lo, hi)` iterator for depth-limited queries?** Not needed now; `GetDepth` uses `Seek` + iteration and stops at `depth` nodes.
- **Should the skip list be moved to a shared package (`pkg/skiplist`)?** For now, keep it in `internal/matching/book/` since it's the only consumer. If other packages need it, move to `pkg/ds/skiplist` later.
