## Context

The matching engine currently supports only Limit orders. The `matchOrders` logic in `orderbook.go` matches incoming orders against the book using price-level restrictions (buy orders match at prices >= book price, sell orders match at prices <= book price). After matching, any unfilled quantity is placed on the book as a resting limit order.

Order submission flows through the actor goroutine in `engine/actor.go`, which receives commands via a channel and calls `book.AddOrder`. All mutations are serialized through this actor.

## Goals / Non-Goals

**Goals:**
- Add Market order type: matches at any price level without limit constraints
- Add IOC (Immediate-Or-Cancel) order type: matches at limit price or better, unfilled remainder is cancelled
- Add FOK (Fill-Or-Kill) order type: requires complete fill or entire order is rolled back
- Extend `OrderType` model with new enum values
- Wire up gRPC handler to dispatch based on order type
- Maintain actor goroutine serialization of all order book mutations

**Non-Goals:**
- Changing existing Limit order behavior
- Implementing other order types (e.g., Stop, Stop-Limit)
- Modifying the actor goroutine architecture (only the matching logic changes)
- Adding order expiration/time-in-force to resting orders

## Decisions

### Decision 1: Order Type Model Extension

**Chosen approach**: Extend `OrderType` in `internal/model/model.go` with new string constants.

```go
const (
    OrderTypeLimit  OrderType = "limit"
    OrderTypeMarket OrderType = "market"
    OrderTypeIOC    OrderType = "ioc"
    OrderTypeFOK    OrderType = "fok"
)
```

**Rationale**: The model already has `OrderType` as a string enum. Extending it is simpler than introducing a new typed structure. The matching engine can dispatch on string comparison.

**Alternatives considered**:
- Typed int constant: Would require more widespread changes across the codebase
- Separate `OrderTypeFlags` bitmask: Over-engineered for this use case

### Decision 2: Order Type Dispatch Location

**Chosen approach**: Handle order type dispatch in `matchOrders` (in `orderbook.go`), which already contains all matching logic.

**Rationale**: All matching decisions (price checks, fill quantities, order resting) are already centralized in `matchOrders`. Extending it to handle order type differences keeps changes localized.

**Alternatives considered**:
- Dispatch in `SubmitOrder` (matching_server.go): Would require passing order type through the entire call chain
- Dispatch in actor: Would add complexity to actor command handling

### Decision 3: Market Order Matching

**Chosen approach**: Market orders skip the price-level check in `CanMatch`, matching at any price until fully filled or book exhausted.

```go
func (o *OrderInBook) CanMatchForMarket(price decimal.Decimal) bool {
    return true  // No price restriction
}
```

**Rationale**: Market orders should execute at any available price. Skipping the price check is the cleanest implementation.

### Decision 4: IOC Order Post-Match Handling

**Chosen approach**: After matching, if `RemainingQty > 0`, the order is cancelled (not placed on book).

**Rationale**: IOC behavior is "match what you can, cancel the rest." The matching logic itself is identical to Limit; only the post-match action differs.

### Decision 5: FOK Order Rollback Strategy

**Chosen approach**: Track partial trades during matching; if order not fully filled, discard all accumulated trades and return error.

**Rationale**: FOK requires atomicity — either the entire order fills or nothing happens. The simplest implementation is to track trades during matching and discard them if the order can't be fully filled.

### Decision 6: gRPC Error Codes

| Scenario | gRPC Code | Message |
|----------|-----------|---------|
| Market order, insufficient liquidity | `codes.Unavailable` | "insufficient liquidity" |
| FOK order, partial fill | `codes.InvalidArgument` | "FOK requires full fill" |

**Rationale**: `Unavailable` indicates the resource (liquidity) isn't available for Market orders. `InvalidArgument` correctly describes a FOK order that violates its own precondition (full fill requirement).

## Risks / Trade-offs

[Risk] Market orders executing at unfavorable prices in thin books
→ **Mitigation**: Return `UnfilledQty` in `MatchResult`. The gRPC handler surfaces `remaining_quantity` to clients, allowing them to reject partial fills.

[Risk] FOK rollback consuming computation without producing result
→ **Mitigation**: FOK matching is O(n) in book depth regardless of rollback. The rollback simply discards accumulated trades rather than reversing them.

[Risk] IOC orders silently cancelling remaining quantity
→ **Mitigation**: `RemainingQuantity` in response tells clients exactly how much was cancelled. No silent behavior from the client's perspective.

## Open Questions

1. Should Market orders with partial fills return an error to the client, or just the `remaining_quantity`? The design currently returns partial fills with no error, letting the client decide.
