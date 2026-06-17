## Why

The matching engine currently only supports Limit orders. Trading platforms require Market, IOC (Immediate-Or-Cancel), and FOK (Fill-Or-Kill) order types to support various trading strategies. Market orders allow traders to execute immediately at the best available prices without specifying a limit. IOC orders ensure partial fills are accepted while remaining quantity is cancelled. FOK orders require complete execution or no execution at all, preventing partial fills.

## What Changes

- Add Market order type: executes at any available price until fully filled or book exhausted
- Add IOC (Immediate-Or-Cancel) order type: matches as much as possible, cancels unfilled remainder
- Add FOK (Fill-Or-Kill) order type: requires full fill or entire order is rolled back
- Extend `OrderType` model with Market, IOC, FOK values
- Update gRPC `SubmitOrder` handler to dispatch based on order type
- Add comprehensive tests for all new order types

## Capabilities

### New Capabilities

- `matching-order-types`: Core matching engine order type support covering Market, IOC, and FOK order behaviors

## Impact

- **Code affected**: `internal/matching/book/orderbook.go`, `internal/matching/engine/engine.go`, `internal/matching/engine/actor.go`, `internal/matching/server/matching_server.go`
- **Proto affected**: `api/proto/matching/v1/matching.proto`
- **Model affected**: `internal/model/model.go`
- **New tests**: `internal/matching/book/orderbook_test.go`
