// Package engine 提供撮合引擎核心实现
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/workerpool"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/notify/consumer"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/shopspring/decimal"
)

// PublisherInterface 发布者接口
type PublisherInterface interface {
	Publish(ctx context.Context, eventType string, event *consumer.Event) error
}

// Matcher 撮合引擎
type Matcher struct {
	mu        sync.RWMutex
	books     map[string]*book.OrderBook // symbol -> OrderBook
	workers   *workerpool.WorkerPool
	tradeRepo TradeRepositoryInterface
	publisher PublisherInterface
	notify    chan *MatchResult
	ctx       context.Context
	cancel    context.CancelFunc
	running   atomic.Bool
	counter   atomic.Int64
}

// TradeRepositoryInterface 交易记录仓储接口（便于测试 Mock）
type TradeRepositoryInterface interface {
	Create(ctx context.Context, trade *model.Trade) error
	CreateBatch(ctx context.Context, trades []*model.Trade) error
}

// MatchResult 撮合结果
type MatchResult struct {
	OrderID   string
	Trades    []*book.Trade
	Remaining decimal.Decimal
	Status    string
	Timestamp time.Time
}

// Config 撮合引擎配置
type Config struct {
	Workers   int
	QueueSize int
}

// NewMatcher 创建撮合引擎
func NewMatcher(cfg Config) *Matcher {
	if cfg.Workers <= 0 {
		cfg.Workers = 10
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &Matcher{
		books: make(map[string]*book.OrderBook),
		workers: workerpool.New(workerpool.Config{
			Workers:   cfg.Workers,
			QueueSize: cfg.QueueSize,
			Name:      "matching-engine",
		}),
		notify:  make(chan *MatchResult, 10000),
		ctx:    ctx,
		cancel: cancel,
	}

	m.running.Store(true)

	// 启动通知处理协程
	go m.processNotifications()

	logger.Info("matching engine created",
		logger.I("workers", cfg.Workers),
		logger.I("queue_size", cfg.QueueSize),
	)

	return m
}

// SetTradeRepo 设置交易记录仓储
func (m *Matcher) SetTradeRepo(repo TradeRepositoryInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tradeRepo = repo
}

// SetPublisher 设置事件发布者
func (m *Matcher) SetPublisher(publisher PublisherInterface) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publisher = publisher
}

// NewMatcherWithTradeRepo 创建撮合引擎（带交易持久化）
func NewMatcherWithTradeRepo(cfg Config, tradeRepo TradeRepositoryInterface) *Matcher {
	m := NewMatcher(cfg)
	m.SetTradeRepo(tradeRepo)
	return m
}

// processNotifications 处理撮合通知
func (m *Matcher) processNotifications() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case result := <-m.notify:
			logger.Debug("match result",
				logger.S("order_id", result.OrderID),
				logger.I("trade_count", len(result.Trades)),
				logger.S("remaining", result.Remaining.String()),
			)

			// 持久化交易记录
			m.persistTrades(result)

			// 发布交易事件通知
			m.publishTrades(result)
		}
	}
}

// publishTrades 发布交易事件
func (m *Matcher) publishTrades(result *MatchResult) {
	if m.publisher == nil || len(result.Trades) == 0 {
		return
	}

	m.mu.RLock()
	publisher := m.publisher
	m.mu.RUnlock()

	for _, t := range result.Trades {
		tradeEvent := &consumer.TradeEvent{
			TradeID:     t.TradeID,
			BuyOrderID:  t.BuyOrderID,
			SellOrderID: t.SellOrderID,
			BuyUserID:   t.BuyUserID,
			SellUserID:  t.SellUserID,
			Price:       t.Price.InexactFloat64(),
			Quantity:    t.Quantity.InexactFloat64(),
		}

		data, _ := json.Marshal(tradeEvent)
		event := &consumer.Event{
			Type:      "trade",
			OrderID:   result.OrderID,
			Symbol:    t.Symbol,
			Data:      data,
			Timestamp: time.Now().Unix(),
		}

		go func(e *consumer.Event) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := publisher.Publish(ctx, "trade", e); err != nil {
				logger.Error("failed to publish trade event",
					logger.S("trade_id", tradeEvent.TradeID),
					logger.Err(err),
				)
			}
		}(event)
	}
}

// persistTrades 持久化交易记录
func (m *Matcher) persistTrades(result *MatchResult) {
	if m.tradeRepo == nil || len(result.Trades) == 0 {
		return
	}

	m.mu.RLock()
	tradeRepo := m.tradeRepo
	m.mu.RUnlock()

	// 转换 book.Trade 为 model.Trade
	modelTrades := make([]*model.Trade, 0, len(result.Trades))
	for _, t := range result.Trades {
		modelTrades = append(modelTrades, &model.Trade{
			TradeID:     t.TradeID,
			BuyOrderID:  t.BuyOrderID,
			SellOrderID: t.SellOrderID,
			Price:       t.Price.InexactFloat64(),
			Quantity:    t.Quantity.InexactFloat64(),
			BuyUserID:   t.BuyUserID,
			SellUserID:  t.SellUserID,
			Symbol:      t.Symbol,
		})
	}

	// 异步写入数据库（不阻塞撮合流程）
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := tradeRepo.CreateBatch(ctx, modelTrades); err != nil {
			logger.Error("failed to persist trades",
				logger.S("order_id", result.OrderID),
				logger.I("trade_count", len(modelTrades)),
				logger.Err(err),
			)
		} else {
			logger.Info("trades persisted",
				logger.S("order_id", result.OrderID),
				logger.I("trade_count", len(modelTrades)),
			)
		}
	}()
}

// GetOrCreateOrderBook 获取或创建订单簿
func (m *Matcher) GetOrCreateOrderBook(symbol string) *book.OrderBook {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ob, ok := m.books[symbol]; ok {
		return ob
	}

	ob := book.NewOrderBook(symbol)
	m.books[symbol] = ob

	logger.Info("order book created", logger.S("symbol", symbol))

	return ob
}

// SubmitOrder 提交订单进行撮合
func (m *Matcher) SubmitOrder(ctx context.Context, orderID string, userID int64, symbol string, side model.OrderSide, price, quantity decimal.Decimal) (*MatchResult, error) {
	if !m.running.Load() {
		return nil, fmt.Errorf("matching engine is not running")
	}

	// 获取或创建订单簿
	ob := m.GetOrCreateOrderBook(symbol)

	// 创建订单
	order := book.NewOrderInBook(
		m.counter.Add(1),
		orderID,
		userID,
		symbol,
		side,
		price,
		quantity,
	)

	// 添加订单进行撮合
	trades := ob.AddOrder(order)

	// 构建结果
	result := &MatchResult{
		OrderID:   orderID,
		Trades:    trades,
		Remaining: order.RemainingQty,
		Status:    string(order.Status),
		Timestamp: time.Now(),
	}

	// 发送通知
	select {
	case m.notify <- result:
	default:
		logger.Warn("notification channel is full", logger.S("order_id", orderID))
	}

	logger.Info("order submitted for matching",
		logger.S("order_id", orderID),
		logger.S("symbol", symbol),
		logger.S("side", string(side)),
		logger.S("price", price.String()),
		logger.S("quantity", quantity.String()),
		logger.I("trades", len(trades)),
		logger.S("remaining", order.RemainingQty.String()),
	)

	return result, nil
}

// SubmitOrderAsync 异步提交订单
func (m *Matcher) SubmitOrderAsync(ctx context.Context, orderID string, userID int64, symbol string, side model.OrderSide, price, quantity decimal.Decimal) error {
	return m.workers.SubmitWithContext(ctx, func() error {
		_, err := m.SubmitOrder(ctx, orderID, userID, symbol, side, price, quantity)
		return err
	})
}

// CancelOrder 取消订单
func (m *Matcher) CancelOrder(symbol, orderID string) bool {
	ob := m.GetOrCreateOrderBook(symbol)
	success := ob.CancelOrder(orderID)

	if success {
		logger.Info("order cancelled",
			logger.S("symbol", symbol),
			logger.S("order_id", orderID),
		)
	}

	return success
}

// GetOrderBook 获取订单簿快照
func (m *Matcher) GetOrderBook(symbol string, depth int) (bids, asks []*book.OrderInBook) {
	ob := m.GetOrCreateOrderBook(symbol)
	return ob.GetDepth(depth)
}

// GetBestPrice 获取最佳买卖价
func (m *Matcher) GetBestPrice(symbol string) (bestBid, bestAsk decimal.Decimal) {
	ob := m.GetOrCreateOrderBook(symbol)
	return ob.GetBestBid(), ob.GetBestAsk()
}

// GetSpread 获取价差
func (m *Matcher) GetSpread(symbol string) decimal.Decimal {
	ob := m.GetOrCreateOrderBook(symbol)
	return ob.GetSpread()
}

// GetOrder 获取订单信息
func (m *Matcher) GetOrder(symbol, orderID string) *book.OrderInBook {
	ob := m.GetOrCreateOrderBook(symbol)
	return ob.GetOrderByID(orderID)
}

// Subscribe 订阅撮合结果
func (m *Matcher) Subscribe() <-chan *MatchResult {
	return m.notify
}

// Shutdown 关闭撮合引擎
func (m *Matcher) Shutdown() {
	if !m.running.CompareAndSwap(true, false) {
		return
	}

	logger.Info("shutting down matching engine")

	// 取消上下文
	m.cancel()

	// 关闭 worker pool
	m.workers.Shutdown()

	logger.Info("matching engine stopped")
}

// ShutdownNow 立即关闭
func (m *Matcher) ShutdownNow() {
	if !m.running.CompareAndSwap(true, false) {
		return
	}

	logger.Info("shutting down matching engine immediately")

	m.cancel()
	m.workers.ShutdownNow()

	logger.Info("matching engine stopped immediately")
}

// IsRunning 检查是否运行中
func (m *Matcher) IsRunning() bool {
	return m.running.Load()
}

// Stats 返回统计信息
func (m *Matcher) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["running"] = m.running.Load()
	stats["symbols"] = len(m.books)
	stats["worker_pool"] = m.workers.GetMetrics()

	return stats
}

// GenerateOrderID 生成订单ID
func GenerateOrderID() string {
	return fmt.Sprintf("ORD%s%s", time.Now().Format("20060102150405"), uuid.New().String()[:8])
}

// GenerateTradeID 生成交易ID
func GenerateTradeID() string {
	return fmt.Sprintf("TRD%s%s", time.Now().Format("20060102150405"), uuid.New().String()[:8])
}
