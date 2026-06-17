# Matching Order Types

This spec defines the order types supported by the matching engine: LIMIT, MARKET, IOC, and FOK.

## Purpose

The matching engine supports four order types to accommodate different trading strategies and execution requirements. Each order type has distinct matching semantics regarding price constraints, fill requirements, and book placement behavior.

## Requirements

### Requirement: MATCHING-ORDER-TYPES-001: OrderType enum covers LIMIT, MARKET, IOC, FOK with correct String()

The matching engine SHALL support four order types: LIMIT, MARKET, IOC, and FOK. The OrderType enum in `internal/model/model.go` SHALL define these four values. The String() representation SHALL return "limit", "market", "ioc", and "fok" respectively.

#### Scenario: OrderType enum values are defined
- **WHEN** the matching engine processes an order
- **THEN** the OrderType enum SHALL have values for LIMIT, MARKET, IOC, and FOK

#### Scenario: OrderType string representation
- **WHEN** OrderType.String() is called
- **THEN** it SHALL return "limit" for LIMIT, "market" for MARKET, "ioc" for IOC, and "fok" for FOK

### Requirement: MATCHING-ORDER-TYPES-002: Market orders skip price-level restriction, match at any price until exhausted

Market orders SHALL NOT enforce price-level restrictions during matching. A market buy order SHALL match against all available sell orders starting from the best ask price and continue until the order's quantity is fully filled or the book is exhausted. A market sell order SHALL match against all available buy orders starting from the best bid price and continue until the order's quantity is fully filled or the book is exhausted. Market orders that are not fully filled SHALL NOT be placed on the book; they are considered killed.

#### Scenario: Market buy order fully fills against multiple price levels
- **WHEN** a market buy order for 100 units is submitted against a book with sell orders at 10.00 (qty 30), 10.01 (qty 40), 10.02 (qty 40)
- **THEN** trades SHALL be created at prices 10.00, 10.01, and 10.02 totaling 100 units filled
- **AND** no remaining quantity is placed on the book

#### Scenario: Market order partially fills with insufficient liquidity
- **WHEN** a market buy order for 100 units is submitted against a book with sell orders totaling 60 units
- **THEN** trades SHALL be created for 60 units
- **AND** the remaining 40 units SHALL NOT be placed on the book
- **AND** MatchResult.UnfilledQty SHALL be 40

#### Scenario: Market sell order matches against best bid prices
- **WHEN** a market sell order for 50 units is submitted
- **THEN** it SHALL match against the best bid price(s) without requiring the price to be >= a limit

### Requirement: MATCHING-ORDER-TYPES-003: IOC orders match at limit price or better; unfilled qty is cancelled (not placed on book)

IOC (Immediate-Or-Cancel) orders SHALL match at the limit price or better, similar to limit orders. After matching, any unfilled quantity SHALL be cancelled and NOT placed on the book. IOC orders never rest on the book regardless of how much was filled.

#### Scenario: IOC order fully fills
- **WHEN** an IOC buy order with price 10.00 and quantity 100 is submitted against sell orders at 10.00 totaling 100 units
- **THEN** trades SHALL be created for 100 units at price 10.00
- **AND** no quantity SHALL be placed on the book

#### Scenario: IOC order partially fills - remaining is cancelled
- **WHEN** an IOC buy order with price 10.00 and quantity 100 is submitted against sell orders at 10.00 totaling 60 units
- **THEN** trades SHALL be created for 60 units at price 10.00
- **AND** the remaining 40 units SHALL be cancelled
- **AND** no quantity SHALL be placed on the book

#### Scenario: IOC order matches at better prices
- **WHEN** an IOC buy order with price 10.00 is submitted against a sell order at 9.99
- **THEN** the trade SHALL execute at price 9.99 (better than limit)

### Requirement: MATCHING-ORDER-TYPES-004: FOK orders require full fill; partial matches are rolled back, error returned

FOK (Fill-Or-Kill) orders SHALL require the entire order quantity to be matched. If the full quantity cannot be matched, the order SHALL NOT execute at all — any partial matches that occurred during the matching process SHALL be rolled back. FOK orders return an error when full fill is not possible.

#### Scenario: FOK order fully fills
- **WHEN** a FOK buy order with price 10.00 and quantity 100 is submitted against sell orders totaling exactly 100 units at 10.00
- **THEN** trades SHALL be created for 100 units
- **AND** the order is considered complete

#### Scenario: FOK order partial fill - rollback and error
- **WHEN** a FOK buy order with price 10.00 and quantity 100 is submitted against sell orders totaling 60 units
- **THEN** no trades SHALL be created
- **AND** an error SHALL be returned with message "FOK requires full fill"
- **AND** the order book state SHALL remain unchanged

#### Scenario: FOK order matches across multiple levels
- **WHEN** a FOK buy order with price 10.00 and quantity 100 is submitted against sell orders at 10.00 (qty 30), 10.01 (qty 40), 10.02 (qty 40)
- **THEN** trades SHALL be created for all 100 units across the three levels
- **AND** the order is considered complete

### Requirement: MATCHING-ORDER-TYPES-005: Proto OrderType enum values map correctly to model.OrderType

The gRPC proto definition SHALL have an OrderType enum with values LIMIT=0, MARKET=1, IOC=2, FOK=3. The matching server SHALL correctly map proto OrderType values to the corresponding model.OrderType values when processing SubmitOrder requests.

#### Scenario: Proto LIMIT maps to model OrderTypeLimit
- **WHEN** SubmitOrderRequest with OrderType LIMIT is received
- **THEN** the order SHALL be processed as a limit order

#### Scenario: Proto MARKET maps to model OrderTypeMarket
- **WHEN** SubmitOrderRequest with OrderType MARKET is received
- **THEN** the order SHALL be processed as a market order

#### Scenario: Proto IOC maps to model OrderTypeIOC
- **WHEN** SubmitOrderRequest with OrderType IOC is received
- **THEN** the order SHALL be processed as an IOC order

#### Scenario: Proto FOK maps to model OrderTypeFOK
- **WHEN** SubmitOrderRequest with OrderType FOK is received
- **THEN** the order SHALL be processed as a FOK order
