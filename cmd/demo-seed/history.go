package main

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	btcMarket = iota
	ethMarket
	marketCount

	quoteAsset = "USDT"
	batchSize  = 500
)

var seedMarkets = [marketCount]struct {
	code, symbol, base     string
	startPrice, priceSD    float64
	median, qtyMin, qtyMax float64
	qtyDP                  int32
}{
	btcMarket: {code: "btc", symbol: "BTC/USDT", base: "BTC", startPrice: 61840, priceSD: 0.0009, median: 0.03, qtyMin: 0.0005, qtyMax: 2.5, qtyDP: 4},
	ethMarket: {code: "eth", symbol: "ETH/USDT", base: "ETH", startPrice: 3212.40, priceSD: 0.0011, median: 0.6, qtyMin: 0.01, qtyMax: 60, qtyDP: 3},
}

type historicalTrade struct {
	market, number  int
	at              time.Time
	price, quantity decimal.Decimal
}

func generateHistory(now time.Time, rng *rand.Rand) []historicalTrade {
	now = now.UTC().Truncate(time.Microsecond)
	start, recent, end := now.Add(-7*24*time.Hour), now.Add(-24*time.Hour), now.Add(-2*time.Minute)
	events := make([]historicalTrade, 0, marketCount*(6*24*60/4+24*60))
	for i, market := range seedMarkets {
		at, price := start, market.startPrice
		for n := 1; ; n++ {
			mean := 4 * time.Minute
			if !at.Before(recent) {
				mean = time.Minute
			}
			at = at.Add(time.Duration(rng.ExpFloat64() * float64(mean)))
			if at.After(end) {
				break
			}
			price *= math.Exp(0.00001 + market.priceSD*rng.NormFloat64())
			qty := min(market.qtyMax, max(market.qtyMin, market.median*math.Exp(rng.NormFloat64())))
			events = append(events, historicalTrade{
				market: i, number: n, at: at.Truncate(time.Microsecond),
				price: decimal.NewFromFloat(price).Round(2), quantity: decimal.NewFromFloat(qty).Round(market.qtyDP),
			})
		}
	}
	// Both markets spend USDT from the same accounts; apply one merged timeline.
	slices.SortStableFunc(events, func(a, b historicalTrade) int { return a.at.Compare(b.at) })
	return events
}

func (e historicalTrade) rows(buyer, seller int64, notional decimal.Decimal, rng *rand.Rand) (model.Order, model.Order, model.Trade) {
	m := seedMarkets[e.market]
	tradeID := fmt.Sprintf("seed-%s-%06d", m.code, e.number)
	buyID, sellID := tradeID+"-b", tradeID+"-s"
	taker := model.OrderSideBuy
	if rng.IntN(2) == 0 {
		taker = model.OrderSideSell
	}
	makerAt := e.at.Add(-time.Duration(1+rng.IntN(600)) * time.Second)
	buyAt, sellAt := e.at, e.at
	if taker == model.OrderSideBuy {
		sellAt = makerAt
	} else {
		buyAt = makerAt
	}
	buy := model.Order{
		OrderID: buyID, IdempotencyKey: buyID, UserID: buyer, Symbol: m.symbol, Side: model.OrderSideBuy,
		Type: model.OrderTypeLimit, Status: model.OrderStatusFilled, Price: e.price, Quantity: e.quantity,
		FilledQuantity: e.quantity, FrozenAsset: quoteAsset, FrozenAmount: notional,
		CreatedAt: buyAt, UpdatedAt: buyAt,
	}
	sell := model.Order{
		OrderID: sellID, IdempotencyKey: sellID, UserID: seller, Symbol: m.symbol, Side: model.OrderSideSell,
		Type: model.OrderTypeLimit, Status: model.OrderStatusFilled, Price: e.price, Quantity: e.quantity,
		FilledQuantity: e.quantity, FrozenAsset: m.base, FrozenAmount: e.quantity,
		CreatedAt: sellAt, UpdatedAt: sellAt,
	}
	return buy, sell, model.Trade{
		TradeID: tradeID, Symbol: m.symbol, BuyOrderID: buyID, SellOrderID: sellID,
		BuyUserID: buyer, SellUserID: seller, Price: e.price, Quantity: e.quantity,
		TakerSide: taker, LSN: 0, CreatedAt: e.at,
	}
}

func seedHistory(ctx context.Context, db *gorm.DB, user userpb.UserServiceClient, accounts *accounts, rng *rand.Rand, now time.Time) ([marketCount]decimal.Decimal, error) {
	var last [marketCount]decimal.Decimal
	events := generateHistory(now, rng)
	orders := make([]model.Order, 0, 2*len(events))
	trades := make([]model.Trade, 0, len(events))
	for _, event := range events {
		market := seedMarkets[event.market]
		notional := event.price.Mul(event.quantity)
		buyer, seller, err := accounts.counterparties(rng, market.base, notional, event.quantity)
		if err != nil {
			return last, fmt.Errorf("%s historical trade %d: %w", market.symbol, event.number, err)
		}
		buy, sell, trade := event.rows(buyer.id, seller.id, notional, rng)
		if err := settleHistoryTrade(ctx, user, buy, sell, trade); err != nil {
			return last, err
		}
		applyHistoryBalances(buyer, seller, market.base, notional, event.quantity)
		orders = append(orders, buy, sell)
		trades = append(trades, trade)
		last[event.market] = event.price
	}

	// Makers predate their prints, so order rows need their own chronological sort.
	slices.SortStableFunc(orders, func(a, b model.Order) int { return a.CreatedAt.Compare(b.CreatedAt) })
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(orders, batchSize).Error; err != nil {
			return fmt.Errorf("insert historical orders: %w", err)
		}
		if err := tx.CreateInBatches(trades, batchSize).Error; err != nil {
			return fmt.Errorf("insert historical trades: %w", err)
		}
		return nil
	})
	return last, err
}

func settleHistoryTrade(ctx context.Context, user userpb.UserServiceClient, buy, sell model.Order, trade model.Trade) error {
	for _, order := range [2]model.Order{buy, sell} {
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		resp, err := user.Freeze(rpcCtx, &userpb.FreezeRequest{
			UserId: order.UserID, Asset: order.FrozenAsset, Amount: order.FrozenAmount.String(), OrderId: order.OrderID,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("freeze %s: %w", order.OrderID, err)
		}
		if !resp.GetOk() {
			return fmt.Errorf("freeze %s denied: %q", order.OrderID, resp.GetReason())
		}
	}
	price := trade.Price.String()
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	_, err := user.SettleTrade(rpcCtx, &userpb.SettleTradeRequest{
		TradeId: trade.TradeID, BaseAsset: sell.FrozenAsset, QuoteAsset: buy.FrozenAsset,
		BuyUserId: trade.BuyUserID, SellUserId: trade.SellUserID,
		Price: price, Quantity: trade.Quantity.String(), BuyOrderPrice: price,
	})
	if err != nil {
		return fmt.Errorf("settle %s: %w", trade.TradeID, err)
	}
	return nil
}

func applyHistoryBalances(buyer, seller *seedAccount, base string, notional, quantity decimal.Decimal) {
	buyer.available[quoteAsset] = buyer.available[quoteAsset].Sub(notional)
	buyer.available[base] = buyer.available[base].Add(quantity)
	seller.available[base] = seller.available[base].Sub(quantity)
	seller.available[quoteAsset] = seller.available[quoteAsset].Add(notional)
}
