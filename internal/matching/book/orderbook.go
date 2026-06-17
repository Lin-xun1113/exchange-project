package book

import (
	"fmt"
	"sort"
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
	OrderType      model.OrderType `json:"order_type"`
	Price          decimal.Decimal `json:"price"`
	Quantity       decimal.Decimal `json:"quantity"`
	FilledQuantity decimal.Decimal `json:"filled_quantity"`
	RemainingQty   decimal.Decimal `json:"remaining_quantity"`
	UnfilledQty    decimal.Decimal `json:"unfilled_quantity"`
	Status         model.OrderStatus `json:"status"`
	CreatedAt      int64           `json:"created_at"` // 纳秒时间戳，用于 FIFO
}

// NewOrderInBook 从订单创建订单簿订单
func NewOrderInBook(id int64, orderID string, userID int64, symbol string, side model.OrderSide, orderType model.OrderType, price, quantity decimal.Decimal) *OrderInBook {
	return &OrderInBook{
		ID:             id,
		OrderID:        orderID,
		UserID:         userID,
		Symbol:         symbol,
		Side:           side,
		OrderType:      orderType,
		Price:          price,
		Quantity:       quantity,
		FilledQuantity: decimal.Zero,
		RemainingQty:   quantity,
		UnfilledQty:    decimal.Zero,
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

// CanMatchForMarket 市场订单跳过价格限制
func (o *OrderInBook) CanMatchForMarket(_ decimal.Decimal) bool {
	return true
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

// PriceLevel represents a price level in the order book.
type PriceLevel struct {
	Price   float64
	Orders  []*OrderInBook
}

// OrderBook 订单簿
type OrderBook struct {
	mu          sync.RWMutex
	symbol      string
	buyOrders   []*OrderInBook // 买单列表，按价格降序
	sellOrders  []*OrderInBook // 卖单列表，按价格升序
	bids        *SkipList[float64] // 价格索引：最高买价 = SeekLast()
	asks        *SkipList[float64] // 价格索引：最低卖价 = SeekFirst()
	priceLevels map[float64]*PriceLevel // 价格级别映射
}

// NewOrderBook 创建新的订单簿
func NewOrderBook(symbol string) *OrderBook {
	return &OrderBook{
		symbol:      symbol,
		buyOrders:  make([]*OrderInBook, 0),
		sellOrders: make([]*OrderInBook, 0),
		bids: NewSkipList(func(a, b float64) bool { return a < b }),
		asks: NewSkipList(func(a, b float64) bool { return a < b }),
		priceLevels: make(map[float64]*PriceLevel),
	}
}

// GetSymbol 返回交易对
func (ob *OrderBook) GetSymbol() string {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return ob.symbol
}

// AddOrder 添加订单并尝试撮合
func (ob *OrderBook) AddOrder(order *OrderInBook) (trades []*Trade, err error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	var tradesList []*Trade

	// Save opposite order state for FOK rollback
	type restoredOrder struct {
		opp           *OrderInBook
		filledOrig    decimal.Decimal
		remainingOrig decimal.Decimal
		statusOrig    model.OrderStatus
	}
	var restored []*restoredOrder

	// 尝试撮合
	if order.Side == model.OrderSideBuy {
		// 遍历所有卖单（从最低价开始）
		for node := ob.asks.SeekFirst(); node != nil; node = node.Next() {
			pl := ob.priceLevels[node.Key]
			if pl == nil {
				continue
			}
			// 按时间戳升序遍历该价格级别的订单
			sort.Slice(pl.Orders, func(i, j int) bool {
				return pl.Orders[i].CreatedAt < pl.Orders[j].CreatedAt
			})
			for _, opp := range pl.Orders {
				if !opp.IsActive() {
					continue
				}
				canMatch := false
				if order.OrderType == model.OrderTypeMarket {
					canMatch = true
				} else {
					canMatch = order.CanMatch(opp.Price)
				}
				if !canMatch {
					break
				}
				// Save state for FOK rollback
				restored = append(restored, &restoredOrder{
					opp:           opp,
					filledOrig:    opp.FilledQuantity,
					remainingOrig: opp.RemainingQty,
					statusOrig:    opp.Status,
				})
				filledQty := decimal.Min(order.RemainingQty, opp.RemainingQty)
				trade := &Trade{
					TradeID:     GenerateTradeID(),
					BuyOrderID:  order.OrderID,
					SellOrderID: opp.OrderID,
					BuyUserID:   order.UserID,
					SellUserID:  opp.UserID,
					Symbol:      ob.symbol,
					Price:       opp.Price,
					Quantity:    filledQty,
					CreatedAt:   time.Now().UnixNano(),
				}
				tradesList = append(tradesList, trade)
				order.Fill(filledQty)
				opp.Fill(filledQty)
				if order.IsFilled() {
					break
				}
			}
			if order.IsFilled() {
				break
			}
		}
	} else {
		// 遍历所有买单（从最高价开始）
		for node := ob.bids.SeekLast(); node != nil; node = node.Prev() {
			pl := ob.priceLevels[node.Key]
			if pl == nil {
				continue
			}
			sort.Slice(pl.Orders, func(i, j int) bool {
				return pl.Orders[i].CreatedAt < pl.Orders[j].CreatedAt
			})
			for _, opp := range pl.Orders {
				if !opp.IsActive() {
					continue
				}
				canMatch := false
				if order.OrderType == model.OrderTypeMarket {
					canMatch = true
				} else {
					canMatch = order.CanMatch(opp.Price)
				}
				if !canMatch {
					break
				}
				// Save state for FOK rollback
				restored = append(restored, &restoredOrder{
					opp:           opp,
					filledOrig:    opp.FilledQuantity,
					remainingOrig: opp.RemainingQty,
					statusOrig:    opp.Status,
				})
				filledQty := decimal.Min(order.RemainingQty, opp.RemainingQty)
				trade := &Trade{
					TradeID:     GenerateTradeID(),
					BuyOrderID:  opp.OrderID,
					SellOrderID: order.OrderID,
					BuyUserID:   opp.UserID,
					SellUserID:  order.UserID,
					Symbol:      ob.symbol,
					Price:       opp.Price,
					Quantity:    filledQty,
					CreatedAt:   time.Now().UnixNano(),
				}
				tradesList = append(tradesList, trade)
				order.Fill(filledQty)
				opp.Fill(filledQty)
				if order.IsFilled() {
					break
				}
			}
			if order.IsFilled() {
				break
			}
		}
	}

	// FOK 回滚检查：在清理前执行，否则对侧订单已被清理
	if order.OrderType == model.OrderTypeFOK && order.RemainingQty.GreaterThan(decimal.Zero) {
		// 回滚对侧订单状态
		for _, r := range restored {
			r.opp.FilledQuantity = r.filledOrig
			r.opp.RemainingQty = r.remainingOrig
			r.opp.Status = r.statusOrig
		}
		order.UnfilledQty = order.RemainingQty
		order.Status = model.OrderStatusRejected
		return nil, fmt.Errorf("FOK requires full fill")
	}

	// 清理已完全成交的订单
	ob.cleanupOrdersInternal()

	// 订单类型后处理
	switch order.OrderType {
	case model.OrderTypeMarket:
		if order.RemainingQty.GreaterThan(decimal.Zero) {
			order.UnfilledQty = order.RemainingQty
		}
		order.Status = model.OrderStatusFilled

	case model.OrderTypeIOC:
		if order.RemainingQty.GreaterThan(decimal.Zero) {
			order.UnfilledQty = order.RemainingQty
		}
		order.Status = model.OrderStatusFilled

	case model.OrderTypeLimit:
		if !order.IsFilled() && order.IsActive() {
			ob.addToBookInternal(order)
		}
	}

	return tradesList, nil
}

// addToBookInternal 添加订单到订单簿（内部方法，调用前需持有锁）
func (ob *OrderBook) addToBookInternal(order *OrderInBook) {
	price := order.Price.InexactFloat64()

	// 使用 skip list 查找或创建价格级别
	node := ob.asks.Seek(price)
	var pl *PriceLevel
	if order.Side == model.OrderSideBuy {
		node = ob.bids.Seek(price)
		if node != nil && node.Key == price {
			pl = ob.priceLevels[price]
		} else {
			node = ob.bids.Insert(price)
			pl = &PriceLevel{Price: price, Orders: make([]*OrderInBook, 0)}
			ob.priceLevels[price] = pl
		}
	} else {
		if node != nil && node.Key == price {
			pl = ob.priceLevels[price]
		} else {
			node = ob.asks.Insert(price)
			pl = &PriceLevel{Price: price, Orders: make([]*OrderInBook, 0)}
			ob.priceLevels[price] = pl
		}
	}

	pl.Orders = append(pl.Orders, order)

	switch order.Side {
	case model.OrderSideBuy:
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

// removePriceLevel removes a price level from the skip list and priceLevels map.
func (ob *OrderBook) removePriceLevel(price float64, side model.OrderSide) {
	if side == model.OrderSideBuy {
		ob.bids.Delete(price)
	} else {
		ob.asks.Delete(price)
	}
	delete(ob.priceLevels, price)
}

// cleanupOrdersInternal 清理已完全成交的订单（内部方法，调用前需持有锁）
func (ob *OrderBook) cleanupOrdersInternal() {
	// 清理买单
	newBuyOrders := make([]*OrderInBook, 0, len(ob.buyOrders))
	for _, o := range ob.buyOrders {
		if o.IsActive() {
			newBuyOrders = append(newBuyOrders, o)
		} else {
			// 从价格级别中移除
			price := o.Price.InexactFloat64()
			pl := ob.priceLevels[price]
			if pl != nil {
				newOrders := make([]*OrderInBook, 0, len(pl.Orders))
				for _, ord := range pl.Orders {
					if ord.OrderID != o.OrderID {
						newOrders = append(newOrders, ord)
					}
				}
				pl.Orders = newOrders
				if len(pl.Orders) == 0 {
					ob.removePriceLevel(price, model.OrderSideBuy)
				}
			}
		}
	}
	ob.buyOrders = newBuyOrders

	// 清理卖单
	newSellOrders := make([]*OrderInBook, 0, len(ob.sellOrders))
	for _, o := range ob.sellOrders {
		if o.IsActive() {
			newSellOrders = append(newSellOrders, o)
		} else {
			price := o.Price.InexactFloat64()
			pl := ob.priceLevels[price]
			if pl != nil {
				newOrders := make([]*OrderInBook, 0, len(pl.Orders))
				for _, ord := range pl.Orders {
					if ord.OrderID != o.OrderID {
						newOrders = append(newOrders, ord)
					}
				}
				pl.Orders = newOrders
				if len(pl.Orders) == 0 {
					ob.removePriceLevel(price, model.OrderSideSell)
				}
			}
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
			// 从价格级别中移除
			price := o.Price.InexactFloat64()
			pl := ob.priceLevels[price]
			if pl != nil {
				newOrders := make([]*OrderInBook, 0, len(pl.Orders))
				for _, ord := range pl.Orders {
					if ord.OrderID != o.OrderID {
						newOrders = append(newOrders, ord)
					}
				}
				pl.Orders = newOrders
				if len(pl.Orders) == 0 {
					ob.removePriceLevel(price, model.OrderSideBuy)
				}
			}
			return true
		}
	}

	// 从卖单中移除
	for i, o := range ob.sellOrders {
		if o.OrderID == orderID {
			o.Status = model.OrderStatusCancelled
			ob.sellOrders = append(ob.sellOrders[:i], ob.sellOrders[i+1:]...)
			price := o.Price.InexactFloat64()
			pl := ob.priceLevels[price]
			if pl != nil {
				newOrders := make([]*OrderInBook, 0, len(pl.Orders))
				for _, ord := range pl.Orders {
					if ord.OrderID != o.OrderID {
						newOrders = append(newOrders, ord)
					}
				}
				pl.Orders = newOrders
				if len(pl.Orders) == 0 {
					ob.removePriceLevel(price, model.OrderSideSell)
				}
			}
			return true
		}
	}

	return false
}

// GetBestBid 获取最佳买价
func (ob *OrderBook) GetBestBid() decimal.Decimal {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	if ob.bids.IsEmpty() {
		return decimal.Zero
	}
	node := ob.bids.SeekLast()
	if node == nil {
		return decimal.Zero
	}
	return decimal.NewFromFloat(node.Key)
}

// GetBestAsk 获取最佳卖价
func (ob *OrderBook) GetBestAsk() decimal.Decimal {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	if ob.asks.IsEmpty() {
		return decimal.Zero
	}
	node := ob.asks.SeekFirst()
	if node == nil {
		return decimal.Zero
	}
	return decimal.NewFromFloat(node.Key)
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
