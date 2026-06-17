# Matching Skip List

This spec defines the skip list data structure replacing `sort.Float64Slice` for price-level indexing in the order book.

## ADDED Requirements

### Requirement: MATCHING-SKIPLIST-001 — Skip list replaces sorted slice for price-level indexing

The `OrderBook.bids` and `OrderBook.asks` fields SHALL be `*SkipList[float64]`, providing sorted traversal in price order. The skip list SHALL store only prices that have non-empty price levels. The `priceLevels map[float64]*PriceLevel` map SHALL remain unchanged and SHALL provide O(1) access to `PriceLevel` structs by price.

#### Scenario: Best bid retrieval

- **WHEN** `GetBestBid()` is called on an order book with at least one bid price level
- **THEN** the skip list seek operation returns the highest key (last node), and the corresponding `priceLevels` entry is returned

#### Scenario: Best ask retrieval

- **WHEN** `GetBestAsk()` is called on an order book with at least one ask price level
- **THEN** the skip list seek operation returns the lowest key (first node), and the corresponding `priceLevels` entry is returned

#### Scenario: Empty order book

- **WHEN** `GetBestBid()` or `GetBestAsk()` is called on an empty order book
- **THEN** `decimal.Zero` is returned

### Requirement: MATCHING-SKIPLIST-002 — Skip list operations are O(log n)

`SkipList.Insert`, `SkipList.Delete`, `SkipList.Search`, and `SkipList.Seek` operations SHALL be O(log n) expected time with `MAX_LEVEL = 32` and `P = 1/e`. The skip list SHALL use a single `sync.Mutex` to protect all mutation operations. Under the per-symbol actor model (MATCHING-ACTOR-001), the mutex SHALL never experience contention since the actor goroutine is the sole writer.

#### Scenario: Insert a new price level

- **WHEN** `addToBookInternal` inserts a new price that does not yet exist in the order book
- **THEN** the skip list `Insert` operation completes in O(log n) expected time and returns the newly created node

#### Scenario: Insert an existing price level

- **WHEN** `addToBookInternal` inserts a price that already exists in the order book
- **THEN** the skip list `Seek` finds the existing node and `Insert` returns it without creating a duplicate entry

#### Scenario: Delete a price level

- **WHEN** `removePriceLevel` removes a price from the order book
- **THEN** the skip list `Delete` operation removes the node in O(log n) expected time and the `priceLevels` map entry is removed separately

#### Scenario: Search for an exact price

- **WHEN** `Search(price)` is called on the skip list
- **THEN** the operation returns the node with the exact matching price, or `nil` if not found, in O(log n) expected time

### Requirement: MATCHING-SKIPLIST-003 — Best bid/ask via seek in O(log n)

`GetBestBid()` SHALL call `sl.Seek(math.Inf(+1))` to retrieve the highest-priced bid node in O(log n). `GetBestAsk()` SHALL call `sl.SeekFirst()` (equivalent to `sl.Seek(math.Inf(-1))`) to retrieve the lowest-priced ask node in O(log n). `GetSpread()` SHALL call both and compute the difference.

#### Scenario: GetSpread returns correct value

- **WHEN** `GetSpread()` is called on an order book with both best bid and best ask
- **THEN** the spread equals `bestAsk - bestBid`

#### Scenario: GetSpread with missing side

- **WHEN** `GetSpread()` is called on an order book with only bids or only asks
- **THEN** `decimal.Zero` is returned

### Requirement: MATCHING-SKIPLIST-004 — Match traversal uses skip list iterator

The `matchOrders` operation (in `AddOrder`) SHALL traverse price levels in sorted order using the skip list's seek-then-iterate pattern. For asks (ascending price), the traversal SHALL start at `sl.SeekFirst()` and continue via `node.Next()` until no more nodes remain. For bids (descending price), the traversal SHALL start at `sl.SeekLast()` and continue via a reverse iterator. The trade execution order SHALL remain unchanged from the current implementation.

#### Scenario: Sell order matches multiple bid levels

- **WHEN** a sell order is submitted against multiple bid price levels
- **THEN** trades are executed in ascending bid price order (highest bid matched first), matching the current behavior

#### Scenario: Buy order matches multiple ask levels

- **WHEN** a buy order is submitted against multiple ask price levels
- **THEN** trades are executed in ascending ask price order (lowest ask matched first), matching the current behavior
