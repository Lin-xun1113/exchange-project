package main

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestHistoryIsDeterministicAndChronologicalAcrossMarkets(t *testing.T) {
	now := time.Date(2026, time.November, 24, 12, 30, 0, 0, time.FixedZone("local", 8*60*60))
	events := generateHistory(now, rand.New(rand.NewPCG(20261124, 7)))
	replay := generateHistory(now, rand.New(rand.NewPCG(20261124, 7)))
	require.NotEmpty(t, events)
	require.Equal(t, events, replay)

	var seen, recent [marketCount]int
	for i, event := range events {
		if i > 0 && event.at.Before(events[i-1].at) {
			t.Fatalf("trade %d precedes its predecessor: %v before %v", i, event.at, events[i-1].at)
		}
		m := &seedMarkets[event.market]
		seen[event.market]++
		require.Equal(t, seen[event.market], event.number)
		require.Equal(t, time.UTC, event.at.Location())
		require.False(t, event.at.Before(now.Add(-7*24*time.Hour)))
		require.False(t, event.at.After(now.Add(-2*time.Minute)))
		require.True(t, event.price.IsPositive())
		require.True(t, event.price.Equal(event.price.Round(2)))
		require.True(t, event.quantity.Equal(event.quantity.Round(m.qtyDP)))
		require.True(t, event.quantity.GreaterThanOrEqual(decimal.NewFromFloat(m.qtyMin)))
		require.True(t, event.quantity.LessThanOrEqual(decimal.NewFromFloat(m.qtyMax)))
		if !event.at.Before(now.Add(-24 * time.Hour)) {
			recent[event.market]++
		}
	}
	for i, n := range recent {
		require.Greater(t, n, 1000, seedMarkets[i].symbol)
		require.Greater(t, seen[i], n, seedMarkets[i].symbol)
	}
}

func TestHistoricalAccountingPreservesAssetTotalsAndDemoReserves(t *testing.T) {
	accounts := newAccounts()
	rng := rand.New(rand.NewPCG(20261124, 7))
	events := generateHistory(time.Date(2026, time.November, 24, 0, 0, 0, 0, time.UTC), rng)
	total := func(asset string) decimal.Decimal {
		var sum decimal.Decimal
		for i := range accounts {
			sum = sum.Add(accounts[i].available[asset])
		}
		return sum
	}
	assets := []string{quoteAsset, "BTC", "ETH"}
	want := make(map[string]decimal.Decimal, len(assets))
	for _, asset := range assets {
		want[asset] = total(asset)
	}
	for _, event := range events {
		base := seedMarkets[event.market].base
		notional := event.price.Mul(event.quantity)
		buyer, seller, err := accounts.counterparties(rng, base, notional, event.quantity)
		require.NoError(t, err)
		require.NotSame(t, buyer, seller)
		applyHistoryBalances(buyer, seller, base, notional, event.quantity)
		require.True(t, buyer.available[quoteAsset].GreaterThanOrEqual(buyer.reserve[quoteAsset]))
		require.True(t, seller.available[base].GreaterThanOrEqual(seller.reserve[base]))
		// Consume the same taker-side and timestamp draws as the seed loop.
		event.rows(1, 2, notional, rng)
	}
	for _, asset := range assets {
		require.True(t, want[asset].Equal(total(asset)), asset)
	}
}

func TestUnfundedHistoricalCounterpartiesUseDistinctMakers(t *testing.T) {
	accounts := newAccounts()
	for i := firstTrader; i <= demoAccount; i++ {
		accounts[i].available[quoteAsset] = decimal.Zero
		accounts[i].available["BTC"] = decimal.Zero
	}
	rng := rand.New(rand.NewPCG(20261124, 7))
	for range 30 {
		buyer, seller, err := accounts.counterparties(rng, "BTC", decimal.NewFromInt(100), decimal.NewFromInt(1))
		require.NoError(t, err)
		require.NotSame(t, buyer, seller)
		require.True(t, buyer == &accounts[0] || buyer == &accounts[1])
		require.True(t, seller == &accounts[0] || seller == &accounts[1])
	}
}

func TestHistoricalRowsShareTheSettlementAmountsAndReferences(t *testing.T) {
	event := historicalTrade{
		market: btcMarket, number: 42,
		at:    time.Date(2026, time.November, 24, 0, 0, 0, 0, time.UTC),
		price: decimal.RequireFromString("61840.25"), quantity: decimal.RequireFromString("0.0123"),
	}
	notional := event.price.Mul(event.quantity)
	buy, sell, trade := event.rows(10, 20, notional, rand.New(rand.NewPCG(20261124, 7)))
	require.Equal(t, "seed-btc-000042", trade.TradeID)
	require.Equal(t, "seed-btc-000042-b", trade.BuyOrderID)
	require.Equal(t, "seed-btc-000042-s", trade.SellOrderID)
	require.Equal(t, trade.BuyOrderID, buy.OrderID)
	require.Equal(t, trade.SellOrderID, sell.OrderID)
	require.Equal(t, trade.BuyUserID, buy.UserID)
	require.Equal(t, trade.SellUserID, sell.UserID)
	require.Equal(t, uint64(0), trade.LSN)
	require.Equal(t, event.at, trade.CreatedAt)
	require.Equal(t, quoteAsset, buy.FrozenAsset)
	require.Equal(t, "BTC", sell.FrozenAsset)
	require.True(t, notional.Equal(buy.FrozenAmount))
	require.True(t, event.quantity.Equal(sell.FrozenAmount))
	for _, order := range []model.Order{buy, sell} {
		require.Equal(t, order.OrderID, order.IdempotencyKey)
		require.Equal(t, model.OrderTypeLimit, order.Type)
		require.Equal(t, model.OrderStatusFilled, order.Status)
		require.True(t, order.Quantity.Equal(trade.Quantity))
		require.True(t, order.FilledQuantity.Equal(trade.Quantity))
		require.True(t, order.ClosedQuantity.IsZero())
		require.Equal(t, order.CreatedAt, order.UpdatedAt)
		if order.Side == trade.TakerSide {
			require.Equal(t, trade.CreatedAt, order.CreatedAt)
		} else {
			lag := trade.CreatedAt.Sub(order.CreatedAt)
			require.GreaterOrEqual(t, lag, time.Second)
			require.LessOrEqual(t, lag, 600*time.Second)
		}
	}
}
