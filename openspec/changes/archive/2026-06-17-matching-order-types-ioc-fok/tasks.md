## 1. Model Extension

- [x] 1.1 Extend OrderType in `internal/model/model.go` with IOC and FOK constants
- [x] 1.2 Add String() method for OrderType covering all four values (LIMIT, MARKET, IOC, FOK)

## 2. Order Book Matching Logic

- [x] 2.1 Add order type field to `OrderInBook` struct in `internal/matching/book/orderbook.go`
- [x] 2.2 Add `CanMatchForMarket()` method to skip price restriction for market orders
- [x] 2.3 Modify `AddOrder()` to handle market order matching (no price check, continue until filled or exhausted)
- [x] 2.4 Modify `AddOrder()` to handle IOC order matching (cancel unfilled remainder)
- [x] 2.5 Modify `AddOrder()` to handle FOK order matching (track trades, rollback if not fully filled)
- [x] 2.6 Add `UnfilledQty` field to support partial fill reporting

## 3. Engine and Actor Updates

- [x] 3.1 Add order type to command struct in `internal/matching/engine/actor.go`
- [x] 3.2 Pass order type through `handleCommand()` to `book.AddOrder()`
- [x] 3.3 Update `MatchResult` struct in `internal/matching/engine/engine.go` to include `UnfilledQty` field
- [x] 3.4 Update `SubmitOrder()` signature to accept order type parameter

## 4. gRPC Server Handler

- [x] 4.1 Add `order_type` field to `SubmitOrderRequest` in `api/proto/matching/v1/matching.proto`
- [x] 4.2 Map proto OrderType enum to model.OrderType in `matching_server.go`
- [x] 4.3 Handle market orders: return trades and `remaining_quantity`
- [x] 4.4 Handle IOC orders: return trades with 0 `remaining_quantity` (unfilled cancelled)
- [x] 4.5 Handle FOK orders: return `codes.InvalidArgument` error if partial fill
- [x] 4.6 Handle market orders with insufficient liquidity: return `codes.Unavailable` error
- [x] 4.7 Regenerate proto files

## 5. Tests

- [x] 5.1 Add test: market order fully fills against existing limits
- [x] 5.2 Add test: market order partially fills, `UnfilledQty` is set
- [x] 5.3 Add test: market sell order matches at best bid prices without price restriction
- [x] 5.4 Add test: IOC order partial fill leaves no remainder on book
- [x] 5.5 Add test: IOC order fully fills
- [x] 5.6 Add test: IOC order matches at better prices
- [x] 5.7 Add test: FOK order partial fill rolls back and returns error
- [x] 5.8 Add test: FOK order full fill succeeds
- [x] 5.9 Add test: FOK order matches across multiple price levels when fully filled
