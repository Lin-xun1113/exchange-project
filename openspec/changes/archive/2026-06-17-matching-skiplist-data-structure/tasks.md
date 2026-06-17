## 1. Create skip list implementation

- [x] 1.1 Create `internal/matching/book/skiplist.go` with `SkipListNode[K]` struct: `Key K`, `Forward []*SkipListNode[K]` (forward pointers by level)
- [x] 1.2 Create `SkipList[K]` struct: `less func(k1, k2 K) bool`, `header *SkipListNode[K]`, `len int`, `mu sync.Mutex`, `maxLevel int`, `prob float64` (set `maxLevel = 32`, `prob = 1/math.E`)
- [x] 1.3 Implement `randomLevel() int`: returns a level between 1 and `maxLevel` using geometric distribution (level k with probability `prob^(k-1) * (1-prob)`)
- [x] 1.4 Implement `NewSkipList[K](less func(k1, k2 K) bool) *SkipList[K]`: initializes header node with `maxLevel` forward pointers
- [x] 1.5 Implement `Insert(key K) *SkipListNode[K]`: seek to find insertion point, generate random level, create node, splice into list. If key already exists, return existing node without inserting
- [x] 1.6 Implement `Search(key K) *SkipListNode[K]`: traverse from header at each level, return exact-match node or nil
- [x] 1.7 Implement `Seek(key K) *SkipListNode[K]`: traverse from header, return first node with key >= given key (lower bound)
- [x] 1.8 Implement `Delete(key K) bool`: seek and remove node if key matches; return whether a node was removed
- [x] 1.9 Implement `Len() int` and `IsEmpty() bool`
- [x] 1.10 Implement `SeekFirst() *SkipListNode[K]` (convenience: `sl.Seek(math.Inf(-1))` semantics) and `SeekLast() *SkipListNode[K]` (convenience: `sl.Seek(math.Inf(+1))`)
- [x] 1.11 Implement `Iterator[K]` with `Next() *SkipListNode[K]`

## 2. Test skip list

- [x] 2.1 Create `internal/matching/book/skiplist_test.go`: unit tests for `Insert`, `Search`, `Seek`, `Delete`, `SeekFirst`, `SeekLast`, `Len`, `IsEmpty`, iterator
- [x] 2.2 Test `Seek(math.Inf(+1))` returns last node; `Seek(math.Inf(-1))` returns first node
- [x] 2.3 Test that `Insert` of duplicate key returns existing node without creating duplicate
- [x] 2.4 Test that `Delete` removes and subsequent `Search` returns nil
- [x] 2.5 Run `go test ./internal/matching/book/... -run SkipList -v` — all tests pass

## 3. Update OrderBook to use skip list

- [x] 3.1 In `orderbook.go`, add `bids *SkipList[float64]` and `asks *SkipList[float64]` fields alongside (or replacing) `buyOrders []*OrderInBook` and `sellOrders []*OrderInBook`
- [x] 3.2 Update `NewOrderBook`: initialize `bids = NewSkipList(func(a, b float64) bool { return a < b })` and `asks` similarly
- [x] 3.3 Update `addToBookInternal`: use `sl.Seek(float64(price))` to find or create the price level node; if node key matches, use existing; if not, call `sl.Insert`. Then add order to the corresponding `priceLevels[price].Orders`
- [x] 3.4 Update `removePriceLevel`: call `sl.Delete(price)` before `delete(priceLevels, price)` to keep structures in sync
- [x] 3.5 Update `GetBestBid`: use `sl.SeekLast()` (or `sl.Seek(math.Inf(+1))`) and return `decimal.NewFromFloat(node.Key)`; return `decimal.Zero` if node is nil
- [x] 3.6 Update `GetBestAsk`: use `sl.SeekFirst()` (or `sl.Seek(math.Inf(-1))`) and return `decimal.NewFromFloat(node.Key)`; return `decimal.Zero` if node is nil
- [x] 3.7 Update `GetDepth`: iterate using `sl.SeekFirst()` and `node.Next()` for asks; `sl.SeekLast()` and reverse iteration for bids; stop after `depth` entries

## 4. Update matching traversal to use skip list iterator

- [x] 4.1 Update the matching loop in `AddOrder`: for ask side, start with `sl.SeekFirst()` and iterate via `node.Next()`; for bid side, start with `sl.SeekLast()` and iterate via reverse pointer
- [x] 4.2 Verify trade execution order is unchanged: add orders at multiple prices, verify trades match in the same order as before
- [x] 4.3 Update `CancelOrder` to handle skip list removal: find price level, remove order from `PriceLevel.Orders`, if level becomes empty, call `sl.Delete(price)` and `delete(priceLevels, price)`

## 5. Update and run tests

- [x] 5.1 Run existing orderbook tests: `go test ./internal/matching/book/... -v` — all existing tests must pass
- [x] 5.2 Add benchmark in `skiplist_test.go`: `BenchmarkSkipListInsert_10KLevels` — insert 10K unique prices, verify timing is consistent with O(log n)
- [x] 5.3 Add benchmark: `BenchmarkOrderBook_AddOrder_10KPriceLevels` — add orders at 10K distinct price levels, verify matching throughput
- [x] 5.4 Run full matching suite: `go test ./internal/matching/... -bench=. -benchmem`

## 6. Final verification

- [x] 6.1 Verify all OpenSpec specs are met: MATCHING-SKIPLIST-001 through MATCHING-SKIPLIST-004
- [x] 6.2 Run `go vet ./internal/matching/...` with no errors
- [x] 6.3 Run `go build ./...` with no errors
