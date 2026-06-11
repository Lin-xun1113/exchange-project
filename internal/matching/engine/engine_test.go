// Package engine_test 提供撮合引擎测试
package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

// TestMatcher_Basic 测试撮合引擎基本功能
func TestMatcher_Basic(t *testing.T) {
	m := engine.NewMatcher(engine.Config{
		Workers:   2,
		QueueSize: 100,
	})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加卖单
	_, err := m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	assert.NoError(t, err)

	// 添加买单
	result, err := m.SubmitOrder(ctx, "ORD002", 2, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50100), decimal.NewFromFloat(0.5))
	assert.NoError(t, err)

	// 应该成交
	assert.NotNil(t, result)
	assert.Len(t, result.Trades, 1)
	assert.True(t, result.Remaining.IsZero())
}

// TestMatcher_OrderBook 测试订单簿
func TestMatcher_OrderBook(t *testing.T) {
	m := engine.NewMatcher(engine.Config{
		Workers:   2,
		QueueSize: 100,
	})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加订单
	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	// 获取订单簿
	bids, asks := m.GetOrderBook("BTC/USDT", 10)
	assert.Len(t, bids, 1)
	assert.Len(t, asks, 0)
}

// TestMatcher_CancelOrder 测试取消订单
func TestMatcher_CancelOrder(t *testing.T) {
	m := engine.NewMatcher(engine.Config{
		Workers:   2,
		QueueSize: 100,
	})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加订单
	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	// 取消订单
	success := m.CancelOrder("BTC/USDT", "ORD001")
	assert.True(t, success)

	// 订单应该不在订单簿中
	order := m.GetOrder("BTC/USDT", "ORD001")
	assert.Nil(t, order)
}

// TestMatcher_BestPrice 测试最佳价格
func TestMatcher_BestPrice(t *testing.T) {
	m := engine.NewMatcher(engine.Config{
		Workers:   2,
		QueueSize: 100,
	})
	defer m.Shutdown()

	ctx := context.Background()

	// 添加多个买单
	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	m.SubmitOrder(ctx, "ORD002", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))
	m.SubmitOrder(ctx, "ORD003", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(49900), decimal.NewFromFloat(1.0))

	// 添加卖单
	m.SubmitOrder(ctx, "ORD004", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50200), decimal.NewFromFloat(1.0))

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
	m := engine.NewMatcher(engine.Config{
		Workers:   2,
		QueueSize: 100,
	})

	assert.True(t, m.IsRunning())

	m.Shutdown()

	assert.False(t, m.IsRunning())
}

// TestMatcher_SubmitOrderAsync 测试异步提交
func TestMatcher_SubmitOrderAsync(t *testing.T) {
	m := engine.NewMatcher(engine.Config{
		Workers:   2,
		QueueSize: 100,
	})
	defer m.Shutdown()

	ctx := context.Background()

	err := m.SubmitOrderAsync(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
}

// TestMatcher_Stats 测试统计信息
func TestMatcher_Stats(t *testing.T) {
	m := engine.NewMatcher(engine.Config{
		Workers:   2,
		QueueSize: 100,
	})
	defer m.Shutdown()

	ctx := context.Background()

	m.SubmitOrder(ctx, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

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
