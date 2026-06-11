// Package book_test 提供撮合引擎测试
package book_test

import (
	"testing"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

// TestOrderBook_AddOrder_Basic 测试订单簿基本添加
func TestOrderBook_AddOrder_Basic(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加一个买单
	buyOrder := book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	trades := ob.AddOrder(buyOrder)

	// 没有卖单，不应该成交
	assert.Empty(t, trades)

	// 买单应该加入订单簿
	bids, asks := ob.GetDepth(10)
	assert.Len(t, bids, 1)
	assert.Len(t, asks, 0)
}

// TestOrderBook_AddOrder_Match 测试订单撮合
func TestOrderBook_AddOrder_Match(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加一个卖单
	sellOrder := book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	ob.AddOrder(sellOrder)

	// 添加一个买单价高于卖单的买单
	buyOrder := book.NewOrderInBook(2, "ORD002", 2, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50100), decimal.NewFromFloat(0.5))
	trades := ob.AddOrder(buyOrder)

	// 应该成交
	assert.Len(t, trades, 1)
	assert.True(t, trades[0].Quantity.Equal(decimal.NewFromFloat(0.5)))
	assert.Equal(t, "ORD002", trades[0].BuyOrderID)
	assert.Equal(t, "ORD001", trades[0].SellOrderID)
}

// TestOrderBook_AddOrder_PartialFill 测试部分成交
func TestOrderBook_AddOrder_PartialFill(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加一个卖单
	sellOrder := book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	ob.AddOrder(sellOrder)

	// 添加一个大于卖单量的买单
	buyOrder := book.NewOrderInBook(2, "ORD002", 2, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50100), decimal.NewFromFloat(1.5))
	trades := ob.AddOrder(buyOrder)

	// 卖单应该完全成交
	assert.Len(t, trades, 1)
	assert.True(t, trades[0].Quantity.Equal(decimal.NewFromFloat(1.0)))

	// 买单应该还剩 0.5 (需要通过结果验证)
	assert.True(t, buyOrder.RemainingQty.Equal(decimal.NewFromFloat(0.5)))
}

// TestOrderBook_CancelOrder 测试取消订单
func TestOrderBook_CancelOrder(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加一个买单
	buyOrder := book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	ob.AddOrder(buyOrder)

	// 取消订单
	success := ob.CancelOrder("ORD001")
	assert.True(t, success)

	// 订单应该不在订单簿中
	order := ob.GetOrderByID("ORD001")
	assert.Nil(t, order)
}

// TestOrderBook_CancelOrder_NotFound 测试取消不存在的订单
func TestOrderBook_CancelOrder_NotFound(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	success := ob.CancelOrder("NONEXISTENT")
	assert.False(t, success)
}

// TestOrderBook_GetBestBidAsk 测试最佳买卖价
func TestOrderBook_GetBestBidAsk(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加多个买单
	ob.AddOrder(book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0)))
	ob.AddOrder(book.NewOrderInBook(2, "ORD002", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0)))
	ob.AddOrder(book.NewOrderInBook(3, "ORD003", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(49900), decimal.NewFromFloat(1.0)))

	// 添加卖单
	ob.AddOrder(book.NewOrderInBook(4, "ORD004", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50200), decimal.NewFromFloat(1.0)))

	// 最佳买价应该是 50100
	bestBid := ob.GetBestBid()
	assert.True(t, bestBid.Equal(decimal.NewFromFloat(50100)))

	// 最佳卖价应该是 50200
	bestAsk := ob.GetBestAsk()
	assert.True(t, bestAsk.Equal(decimal.NewFromFloat(50200)))

	// 价差应该是 100
	spread := ob.GetSpread()
	assert.True(t, spread.Equal(decimal.NewFromFloat(100)))
}

// TestOrderInBook_Fill 测试订单成交
func TestOrderInBook_Fill(t *testing.T) {
	order := book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	assert.True(t, order.RemainingQty.Equal(decimal.NewFromFloat(1.0)))
	assert.Equal(t, model.OrderStatusPending, order.Status)

	// 成交 0.3
	order.Fill(decimal.NewFromFloat(0.3))
	assert.True(t, order.RemainingQty.Equal(decimal.NewFromFloat(0.7)))
	assert.Equal(t, model.OrderStatusPartialFilled, order.Status)

	// 成交剩余 0.7
	order.Fill(decimal.NewFromFloat(0.7))
	assert.True(t, order.RemainingQty.IsZero())
	assert.Equal(t, model.OrderStatusFilled, order.Status)
}

// TestOrderInBook_CanMatch 测试订单匹配条件
func TestOrderInBook_CanMatch(t *testing.T) {
	// 测试买单匹配
	buyOrder := book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	assert.True(t, buyOrder.CanMatch(decimal.NewFromFloat(50000)))   // 等于
	assert.True(t, buyOrder.CanMatch(decimal.NewFromFloat(49900))) // 低于
	assert.False(t, buyOrder.CanMatch(decimal.NewFromFloat(50100))) // 高于

	// 测试卖单匹配
	sellOrder := book.NewOrderInBook(2, "ORD002", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	assert.True(t, sellOrder.CanMatch(decimal.NewFromFloat(50000)))   // 等于
	assert.True(t, sellOrder.CanMatch(decimal.NewFromFloat(50100)))  // 高于
	assert.False(t, sellOrder.CanMatch(decimal.NewFromFloat(49900)))  // 低于
}

// TestOrderBook_MultipleMatching 测试多笔订单撮合
func TestOrderBook_MultipleMatching(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加多个卖单
	ob.AddOrder(book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50000), decimal.NewFromFloat(0.5)))
	ob.AddOrder(book.NewOrderInBook(2, "ORD002", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50050), decimal.NewFromFloat(0.5)))

	// 添加一个买单，应该与两个卖单都成交
	buyOrder := book.NewOrderInBook(3, "ORD003", 2, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50100), decimal.NewFromFloat(0.8))
	trades := ob.AddOrder(buyOrder)

	// 应该成交 2 笔
	assert.Len(t, trades, 2)

	// 买单应该还剩 0.2 未成交（在订单簿中）
	buyOrderAfter := ob.GetOrderByID("ORD003")
	if buyOrderAfter != nil {
		assert.True(t, buyOrderAfter.RemainingQty.Equal(decimal.NewFromFloat(0.2)))
	}
}

// TestGenerateTradeID 测试交易ID生成
func TestGenerateTradeID(t *testing.T) {
	id := book.GenerateTradeID()
	assert.NotEmpty(t, id)
	// ID 应该以 TRD 开头
	assert.Contains(t, id, "TRD")
}

// TestOrderBook_SellSideMatching 测试卖单匹配
func TestOrderBook_SellSideMatching(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加多个买单
	ob.AddOrder(book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50000), decimal.NewFromFloat(0.5)))
	ob.AddOrder(book.NewOrderInBook(2, "ORD002", 1, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(49900), decimal.NewFromFloat(0.5)))

	// 添加一个卖单，应该与买单价最高的买单成交
	sellOrder := book.NewOrderInBook(3, "ORD003", 2, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(49800), decimal.NewFromFloat(0.3))
	trades := ob.AddOrder(sellOrder)

	// 应该成交 1 笔
	assert.Len(t, trades, 1)
	assert.Equal(t, "ORD003", trades[0].SellOrderID)
	assert.True(t, trades[0].Price.Equal(decimal.NewFromFloat(50000))) // 吃的是最佳买价
}

// TestOrderBook_PricePriority 测试价格优先级
func TestOrderBook_PricePriority(t *testing.T) {
	ob := book.NewOrderBook("BTC/USDT")

	// 添加价格相同的卖单
	ob.AddOrder(book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0)))
	ob.AddOrder(book.NewOrderInBook(2, "ORD002", 1, "BTC/USDT",
		model.OrderSideSell, decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0)))

	// 买单应该成交两笔
	buyOrder := book.NewOrderInBook(3, "ORD003", 2, "BTC/USDT",
		model.OrderSideBuy, decimal.NewFromFloat(50100), decimal.NewFromFloat(1.5))
	trades := ob.AddOrder(buyOrder)

	assert.Len(t, trades, 2)
	// 买单成交量为 1.0 + 0.5 = 1.5，应该完全成交
	assert.True(t, buyOrder.RemainingQty.IsZero())
}
