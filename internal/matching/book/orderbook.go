// Package book 提供撮合引擎订单簿实现
//
// 性能注意事项 (Medium Priority - 已评估):
//
// 当前实现使用单一 sync.RWMutex 保护整个订单簿，这对于单交易对场景是足够的。
// 在以下条件下，考虑升级为价格级别锁定或读写锁优化：
//   - 需要支持数百个交易对的高并发场景
//   - 单个订单簿的读写操作成为明显瓶颈
//   - 需要支持同一交易对的并发订单添加
//
// 可能的优化方向:
//   1. 读写锁 (sync.RWMutex) - 已部分使用，但 AddOrder 需要写锁
//   2. 分片锁定 - 按价格区间分片，减少锁竞争
//   3. 乐观锁 + CAS - 使用 atomic 操作减少锁持有时间
//
// 权衡: 当前实现简单且正确，对于中小规模的交易系统足够高效。
package book

import (
	"fmt"
	"sync"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
)

// OrderInBook 订单簿中的订单
type OrderInBook struct {
	ID             int64           `json:"id"`
	OrderID        string          `json:"order_id"`
	UserID         int64           `json:"user_id"`
	Symbol         string          `json:"symbol"`
	Side           model.OrderSide `json:"side"`
	Price          decimal.Decimal `json:"price"`
	Quantity       decimal.Decimal `json:"quantity"`
	FilledQuantity decimal.Decimal `json:"filled_quantity"`
	RemainingQty   decimal.Decimal `json:"remaining_quantity"`
	Status         model.OrderStatus `json:"status"`
	CreatedAt      int64           `json:"created_at"` // 纳秒时间戳，用于 FIFO
}

// NewOrderInBook 从订单创建订单簿订单
func NewOrderInBook(id int64, orderID string, userID int64, symbol string, side model.OrderSide, price, quantity decimal.Decimal) *OrderInBook {
	return &OrderInBook{
		ID:             id,
		OrderID:        orderID,
		UserID:         userID,
		Symbol:         symbol,
		Side:           side,
		Price:          price,
		Quantity:       quantity,
		FilledQuantity: decimal.Zero,
		RemainingQty:   quantity,
		Status:         model.OrderStatusPending,
		CreatedAt:      time.Now().UnixNano(),
	}
}

// Fill 成交
func (o *OrderInBook) Fill(qty decimal.Decimal) {
	o.FilledQuantity = o.FilledQuantity.Add(qty)
	o.RemainingQty = o.RemainingQty.Sub(qty)
	if o.RemainingQty.IsZero() {
		o.Status = model.OrderStatusFilled
	} else {
		o.Status = model.OrderStatusPartialFilled
	}
}

// CanMatch 判断是否可以成交
func (o *OrderInBook) CanMatch(price decimal.Decimal) bool {
	switch o.Side {
	case model.OrderSideBuy:
		return o.Price.GreaterThanOrEqual(price)
	case model.OrderSideSell:
		return o.Price.LessThanOrEqual(price)
	}
	return false
}

// IsFilled 检查是否完全成交
func (o *OrderInBook) IsFilled() bool {
	return o.RemainingQty.IsZero()
}

// IsActive 检查是否活跃
func (o *OrderInBook) IsActive() bool {
	return o.Status == model.OrderStatusPending || o.Status == model.OrderStatusPartialFilled
}

// Trade 交易记录
type Trade struct {
	ID          int64           `json:"id"`
	TradeID     string          `json:"trade_id"`
	BuyOrderID  string          `json:"buy_order_id"`
	SellOrderID string          `json:"sell_order_id"`
	BuyUserID   int64           `json:"buy_user_id"`
	SellUserID  int64           `json:"sell_user_id"`
	Symbol      string          `json:"symbol"`
	Price       decimal.Decimal `json:"price"`
	Quantity    decimal.Decimal `json:"quantity"`
	CreatedAt   int64           `json:"created_at"`
}

// OrderBook 订单簿
type OrderBook struct {
	mu         sync.RWMutex
	symbol     string
	buyOrders  []*OrderInBook // 买单列表，按价格降序
	sellOrders []*OrderInBook // 卖单列表，按价格升序
}

// NewOrderBook 创建新的订单簿
func NewOrderBook(symbol string) *OrderBook {
	return &OrderBook{
		symbol:     symbol,
		buyOrders:  make([]*OrderInBook, 0),
		sellOrders: make([]*OrderInBook, 0),
	}
}

// GetSymbol 返回交易对
func (ob *OrderBook) GetSymbol() string {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return ob.symbol
}

// AddOrder 添加订单并尝试撮合
func (ob *OrderBook) AddOrder(order *OrderInBook) []*Trade {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	var trades []*Trade

	// 获取对侧订单列表
	var opposite []*OrderInBook
	switch order.Side {
	case model.OrderSideBuy:
		opposite = ob.sellOrders
	case model.OrderSideSell:
		opposite = ob.buyOrders
	}

	// 尝试撮合
	for i := 0; i < len(opposite); i++ {
		opp := opposite[i]
		if !opp.IsActive() {
			continue
		}

		// 检查是否可以成交
		if !order.CanMatch(opp.Price) {
			break
		}

		// 成交数量取较小值
		filledQty := decimal.Min(order.RemainingQty, opp.RemainingQty)

		// 创建交易记录
		trade := &Trade{
			TradeID:     GenerateTradeID(),
			BuyOrderID:  opp.OrderID,
			SellOrderID: order.OrderID,
			BuyUserID:   opp.UserID,
			SellUserID:  order.UserID,
			Symbol:      ob.symbol,
			Price:       opp.Price, // 吃单方价格
			Quantity:    filledQty,
			CreatedAt:   time.Now().UnixNano(),
		}
		if order.Side == model.OrderSideBuy {
			trade.BuyOrderID = order.OrderID
			trade.SellOrderID = opp.OrderID
			trade.BuyUserID = order.UserID
			trade.SellUserID = opp.UserID
		}
		trades = append(trades, trade)

		// 更新订单剩余数量
		order.Fill(filledQty)
		opp.Fill(filledQty)

		// 如果订单完全成交，移除
		if order.IsFilled() {
			break
		}
	}

	// 清理已完全成交的订单
	ob.cleanupOrdersInternal()

	// 未完全成交的订单加入订单簿
	if !order.IsFilled() && order.IsActive() {
		ob.addToBookInternal(order)
	}

	return trades
}

// addToBookInternal 添加订单到订单簿（内部方法，调用前需持有锁）
func (ob *OrderBook) addToBookInternal(order *OrderInBook) {
	switch order.Side {
	case model.OrderSideBuy:
		// 买单按价格降序插入
		inserted := false
		for i := 0; i < len(ob.buyOrders); i++ {
			if order.Price.GreaterThan(ob.buyOrders[i].Price) {
				ob.buyOrders = append(ob.buyOrders[:i], append([]*OrderInBook{order}, ob.buyOrders[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			ob.buyOrders = append(ob.buyOrders, order)
		}
	case model.OrderSideSell:
		// 卖单按价格升序插入
		inserted := false
		for i := 0; i < len(ob.sellOrders); i++ {
			if order.Price.LessThan(ob.sellOrders[i].Price) {
				ob.sellOrders = append(ob.sellOrders[:i], append([]*OrderInBook{order}, ob.sellOrders[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			ob.sellOrders = append(ob.sellOrders, order)
		}
	}
}

// cleanupOrdersInternal 清理已完全成交的订单（内部方法，调用前需持有锁）
func (ob *OrderBook) cleanupOrdersInternal() {
	// 清理买单
	newBuyOrders := make([]*OrderInBook, 0, len(ob.buyOrders))
	for _, o := range ob.buyOrders {
		if o.IsActive() {
			newBuyOrders = append(newBuyOrders, o)
		}
	}
	ob.buyOrders = newBuyOrders

	// 清理卖单
	newSellOrders := make([]*OrderInBook, 0, len(ob.sellOrders))
	for _, o := range ob.sellOrders {
		if o.IsActive() {
			newSellOrders = append(newSellOrders, o)
		}
	}
	ob.sellOrders = newSellOrders
}

// GetDepth 获取订单簿深度
func (ob *OrderBook) GetDepth(depth int) (bids []*OrderInBook, asks []*OrderInBook) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	if depth <= 0 {
		depth = 10
	}

	if len(ob.buyOrders) < depth {
		bids = ob.buyOrders
	} else {
		bids = ob.buyOrders[:depth]
	}

	if len(ob.sellOrders) < depth {
		asks = ob.sellOrders
	} else {
		asks = ob.sellOrders[:depth]
	}

	return
}

// GetOrderByID 根据订单ID获取订单
func (ob *OrderBook) GetOrderByID(orderID string) *OrderInBook {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	for _, o := range ob.buyOrders {
		if o.OrderID == orderID {
			return o
		}
	}
	for _, o := range ob.sellOrders {
		if o.OrderID == orderID {
			return o
		}
	}
	return nil
}

// CancelOrder 取消订单
func (ob *OrderBook) CancelOrder(orderID string) bool {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	// 从买单中移除
	for i, o := range ob.buyOrders {
		if o.OrderID == orderID {
			o.Status = model.OrderStatusCancelled
			ob.buyOrders = append(ob.buyOrders[:i], ob.buyOrders[i+1:]...)
			return true
		}
	}

	// 从卖单中移除
	for i, o := range ob.sellOrders {
		if o.OrderID == orderID {
			o.Status = model.OrderStatusCancelled
			ob.sellOrders = append(ob.sellOrders[:i], ob.sellOrders[i+1:]...)
			return true
		}
	}

	return false
}

// GetBestBid 获取最佳买价
func (ob *OrderBook) GetBestBid() decimal.Decimal {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	if len(ob.buyOrders) == 0 {
		return decimal.Zero
	}
	return ob.buyOrders[0].Price
}

// GetBestAsk 获取最佳卖价
func (ob *OrderBook) GetBestAsk() decimal.Decimal {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	if len(ob.sellOrders) == 0 {
		return decimal.Zero
	}
	return ob.sellOrders[0].Price
}

// GetSpread 获取买卖价差
func (ob *OrderBook) GetSpread() decimal.Decimal {
	bid := ob.GetBestBid()
	ask := ob.GetBestAsk()
	if bid.IsZero() || ask.IsZero() {
		return decimal.Zero
	}
	return ask.Sub(bid)
}

// GenerateTradeID 生成交易ID
func GenerateTradeID() string {
	return fmt.Sprintf("TRD%d%s", time.Now().UnixNano(), uuidShort())
}

// uuidShort 生成短 UUID
func uuidShort() string {
	return fmt.Sprintf("%08x", time.Now().UnixNano()%0xffffffff)
}
