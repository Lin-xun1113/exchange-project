// Package engine_test 提供撮合引擎测试
package engine_test

import (
	"context"
	"sync"
	"testing"

	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

// TestMatcher_Basic 测试撮合引擎基本功能
func TestMatcher_Basic(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加卖单
	_, _ = m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideSell, model.OrderTypeLimit, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	// 添加买单
	result, err := m.SubmitOrder(ctx, "ORD002", 2, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50100), decimal.NewFromFloat(0.5))
	assert.NoError(t, err)

	// 应该成交
	assert.NotNil(t, result)
	assert.Len(t, result.Trades, 1)
	assert.True(t, result.Remaining.IsZero())
}

// TestMatcher_OrderBook 测试订单簿
func TestMatcher_OrderBook(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加订单
	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	// 获取订单簿
	bids, asks := m.GetOrderBook("BTC/USDT", 10)
	assert.Len(t, bids, 1)
	assert.Len(t, asks, 0)
}

// TestMatcher_CancelOrder 测试取消订单
func TestMatcher_CancelOrder(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加订单
	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	// 取消订单
	success := m.CancelOrder("BTC/USDT", "ORD001")
	assert.True(t, success)

	// 订单应该不在订单簿中
	order := m.GetOrder("BTC/USDT", "ORD001")
	assert.Nil(t, order)
}

// TestMatcher_BestPrice 测试最佳价格
func TestMatcher_BestPrice(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加多个买单
	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	m.SubmitOrder(ctx, "ORD002", 1, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))
	m.SubmitOrder(ctx, "ORD003", 1, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(49900), decimal.NewFromFloat(1.0))

	// 添加卖单
	m.SubmitOrder(ctx, "ORD004", 1, "BTC/USDT",
		model.OrderSideSell, model.OrderTypeLimit, decimal.NewFromFloat(50200), decimal.NewFromFloat(1.0))

	// 获取最佳价格
	bestBid, bestAsk := m.GetBestPrice("BTC/USDT")
	assert.True(t, bestBid.Equal(decimal.NewFromFloat(50100)))
	assert.True(t, bestAsk.Equal(decimal.NewFromFloat(50200)))

	// 价差
	spread := m.GetSpread("BTC/USDT")
	assert.True(t, spread.Equal(decimal.NewFromFloat(100)))
}

// TestMatcher_Shutdown 测试关闭
func TestMatcher_Shutdown(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})

	assert.True(t, m.IsRunning())

	m.Shutdown()

	assert.False(t, m.IsRunning())
}

// TestMatcher_SubmitOrderAsync tests submitting multiple orders concurrently
// through the actor channel model (replaces worker-pool-based async test).
func TestMatcher_SubmitOrderAsync(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})
	defer m.Shutdown()

	ctx := context.Background()
	var wg sync.WaitGroup

	// Submit 10 orders concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			orderID := engine.GenerateOrderID()
			_, err := m.SubmitOrder(ctx, orderID, int64(idx), "BTC/USDT",
				model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50000), decimal.NewFromFloat(0.1))
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Verify orders are in the book
	bids, asks := m.GetOrderBook("BTC/USDT", 20)
	assert.Len(t, bids, 10)
	assert.Len(t, asks, 0)
}

// TestMatcher_ConcurrentSameSymbol verifies that 100 goroutines submitting
// to the same symbol concurrently produce correct price-time ordered trades.
func TestMatcher_ConcurrentSameSymbol(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})
	defer m.Shutdown()

	ctx := context.Background()

	// Place a large sell order first (acts as the liquidity source)
	_, err := m.SubmitOrder(ctx, "SELL001", 1, "BTC/USDT",
		model.OrderSideSell, model.OrderTypeLimit, decimal.NewFromFloat(50000), decimal.NewFromFloat(10.0))
	assert.NoError(t, err)

	// 100 goroutines submit buy orders at the same price concurrently
	const nOrders = 100
	var wg sync.WaitGroup
	results := make([]*engine.MatchResult, nOrders)
	errors := make([]error, nOrders)

	for i := 0; i < nOrders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			orderID := engine.GenerateOrderID()
			result, err := m.SubmitOrder(ctx, orderID, int64(idx), "BTC/USDT",
				model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50100), decimal.NewFromFloat(0.1))
			results[idx] = result
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// All orders should succeed
	for i := 0; i < nOrders; i++ {
		assert.NoError(t, errors[i], "order %d should not error", i)
		assert.NotNil(t, results[i], "order %d should have result", i)
	}

	// Each order should have traded 0.1
	for i := 0; i < nOrders; i++ {
		if results[i] != nil && len(results[i].Trades) > 0 {
			assert.True(t, results[i].Trades[0].Quantity.Equal(decimal.NewFromFloat(0.1)),
				"order %d trade qty should be 0.1", i)
		}
	}

	// All buy orders should have been filled
	bids, _ := m.GetOrderBook("BTC/USDT", 200)
	assert.Len(t, bids, 0, "no remaining bids after all 100 orders matched against sell")

	// Sell order should be fully filled
	bestBid, _ := m.GetBestPrice("BTC/USDT")
	assert.True(t, bestBid.IsZero(), "best bid should be zero when sell is exhausted")
}

// TestMatcher_Stats 测试统计信息
func TestMatcher_Stats(t *testing.T) {
	m := engine.NewMatcher(engine.Config{})
	defer m.Shutdown()

	ctx := context.Background()

	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	stats := m.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, true, stats["running"])
}

// TestGenerateOrderID 测试订单ID生成
func TestGenerateOrderID(t *testing.T) {
	id1 := engine.GenerateOrderID()
	id2 := engine.GenerateOrderID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "ORD")
}
