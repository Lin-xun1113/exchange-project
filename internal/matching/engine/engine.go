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
	actorsMu     sync.Mutex
	actors       map[string]*actor
	exitedChans  map[string]chan struct{}
	actorTimeout time.Duration
	tradeRepo    TradeRepositoryInterface
	publisher    PublisherInterface
	notify       chan *MatchResult
	ctx          context.Context
	cancel       context.CancelFunc
	running      atomic.Bool
	counter      atomic.Int64
}

// TradeRepositoryInterface 交易记录仓储接口（便于测试 Mock）
type TradeRepositoryInterface interface {
	Create(ctx context.Context, trade *model.Trade) error
	CreateBatch(ctx context.Context, trades []*model.Trade) error
}

// MatchResult 撮合结果
type MatchResult struct {
	OrderID     string
	Trades      []*book.Trade
	Remaining   decimal.Decimal
	UnfilledQty decimal.Decimal
	Status      string
	Timestamp   time.Time
	Error       error
}

// Config 撮合引擎配置
type Config struct {
	ActorTimeout time.Duration
}

// NewMatcher 创建撮合引擎
func NewMatcher(cfg Config) *Matcher {
	if cfg.ActorTimeout <= 0 {
		cfg.ActorTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &Matcher{
		actors:       make(map[string]*actor),
		exitedChans:  make(map[string]chan struct{}),
		actorTimeout: cfg.ActorTimeout,
		notify:       make(chan *MatchResult, 10000),
		ctx:          ctx,
		cancel:       cancel,
	}

	m.running.Store(true)

	go m.processNotifications()

	logger.Info("matching engine created",
		logger.S("actor_timeout", cfg.ActorTimeout.String()),
	)

	return m
}

// SetTradeRepo 设置交易记录仓储
func (m *Matcher) SetTradeRepo(repo TradeRepositoryInterface) {
	m.tradeRepo = repo
}

// SetPublisher 设置事件发布者
func (m *Matcher) SetPublisher(publisher PublisherInterface) {
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
		case result, ok := <-m.notify:
			if !ok {
				return
			}
			logger.Debug("match result",
				logger.S("order_id", result.OrderID),
				logger.I("trade_count", len(result.Trades)),
				logger.S("remaining", result.Remaining.String()),
			)

			m.persistTrades(result)
			m.publishTrades(result)
		}
	}
}

// publishTrades 发布交易事件
func (m *Matcher) publishTrades(result *MatchResult) {
	publisher := m.publisher
	if publisher == nil || len(result.Trades) == 0 {
		return
	}

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
	tradeRepo := m.tradeRepo
	if tradeRepo == nil || len(result.Trades) == 0 {
		return
	}

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

// getOrCreateActor lazily creates and starts an actor goroutine for the given symbol.
func (m *Matcher) getOrCreateActor(symbol string) *actor {
	m.actorsMu.Lock()
	defer m.actorsMu.Unlock()

	if act, ok := m.actors[symbol]; ok {
		return act
	}

	actCtx, actCancel := context.WithCancel(m.ctx)
	exitedCh := make(chan struct{})
	act := &actor{
		symbol:   symbol,
		cmdCh:    make(chan command, 1000),
		cancelCh: make(chan cancelCommand, 1000),
		book:     book.NewOrderBook(symbol),
		cancel:   actCancel,
	}

	m.actors[symbol] = act
	m.exitedChans[symbol] = exitedCh
	go func() {
		act.run(actCtx)
		close(exitedCh)
		m.actorsMu.Lock()
		delete(m.exitedChans, symbol)
		m.actorsMu.Unlock()
	}()

	logger.Info("actor started", logger.S("symbol", symbol))

	return act
}

// dispatch sends a command to the symbol's actor and waits for a result.
func (m *Matcher) dispatch(ctx context.Context, symbol string, cmd command) (*MatchResult, error) {
	if !m.running.Load() {
		return nil, fmt.Errorf("matching engine is not running")
	}

	act := m.getOrCreateActor(symbol)

	timeout := m.actorTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}

	resultCh := make(chan *MatchResult, 1)
	cmd.replyCh = resultCh

	select {
	case act.cmdCh <- cmd:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, fmt.Errorf("matching timeout")
	}

	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, fmt.Errorf("matching timeout")
	}
}

// SubmitOrder 提交订单进行撮合
func (m *Matcher) SubmitOrder(ctx context.Context, orderID string, userID int64, symbol string, side model.OrderSide, orderType model.OrderType, price, quantity decimal.Decimal) (*MatchResult, error) {
	if !m.running.Load() {
		return nil, fmt.Errorf("matching engine is not running")
	}

	cmd := command{
		id:        m.counter.Add(1),
		orderID:   orderID,
		userID:    userID,
		side:      side,
		orderType: orderType,
		price:     price,
		qty:       quantity,
	}

	result, err := m.dispatch(ctx, symbol, cmd)
	if err != nil {
		return nil, err
	}

	select {
	case m.notify <- result:
	default:
		logger.Warn("notification channel is full", logger.S("order_id", orderID))
	}

	logger.Info("order submitted for matching",
		logger.S("order_id", orderID),
		logger.S("symbol", symbol),
		logger.S("side", string(side)),
		logger.S("order_type", string(orderType)),
		logger.S("price", price.String()),
		logger.S("quantity", quantity.String()),
		logger.I("trades", len(result.Trades)),
		logger.S("remaining", result.Remaining.String()),
	)

	return result, nil
}

// CancelOrder 取消订单
func (m *Matcher) CancelOrder(symbol, orderID string) bool {
	if !m.running.Load() {
		return false
	}

	m.actorsMu.Lock()
	act, ok := m.actors[symbol]
	m.actorsMu.Unlock()
	if !ok {
		return false
	}

	replyCh := make(chan bool, 1)
	cmd := cancelCommand{
		orderID: orderID,
		replyCh: replyCh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.actorTimeout)
	defer cancel()

	select {
	case act.cancelCh <- cmd:
	case <-ctx.Done():
		logger.Warn("cancel command timeout",
			logger.S("symbol", symbol),
			logger.S("order_id", orderID),
		)
		return false
	}

	select {
	case success := <-replyCh:
		if success {
			logger.Info("order cancelled",
				logger.S("symbol", symbol),
				logger.S("order_id", orderID),
			)
		}
		return success
	case <-ctx.Done():
		logger.Warn("cancel command timeout waiting for reply",
			logger.S("symbol", symbol),
			logger.S("order_id", orderID),
		)
		return false
	}
}

// GetOrderBook 获取订单簿快照
func (m *Matcher) GetOrderBook(symbol string, depth int) (bids, asks []*book.OrderInBook) {
	if !m.running.Load() {
		return nil, nil
	}

	m.actorsMu.Lock()
	act, ok := m.actors[symbol]
	m.actorsMu.Unlock()
	if !ok {
		return nil, nil
	}

	return act.book.GetDepth(depth)
}

// GetBestPrice 获取最佳买卖价
func (m *Matcher) GetBestPrice(symbol string) (bestBid, bestAsk decimal.Decimal) {
	if !m.running.Load() {
		return decimal.Zero, decimal.Zero
	}

	m.actorsMu.Lock()
	act, ok := m.actors[symbol]
	m.actorsMu.Unlock()
	if !ok {
		return decimal.Zero, decimal.Zero
	}

	return act.book.GetBestBid(), act.book.GetBestAsk()
}

// GetSpread 获取价差
func (m *Matcher) GetSpread(symbol string) decimal.Decimal {
	if !m.running.Load() {
		return decimal.Zero
	}

	m.actorsMu.Lock()
	act, ok := m.actors[symbol]
	m.actorsMu.Unlock()
	if !ok {
		return decimal.Zero
	}

	return act.book.GetSpread()
}

// GetOrder 获取订单信息
func (m *Matcher) GetOrder(symbol, orderID string) *book.OrderInBook {
	if !m.running.Load() {
		return nil
	}

	m.actorsMu.Lock()
	act, ok := m.actors[symbol]
	m.actorsMu.Unlock()
	if !ok {
		return nil
	}

	return act.book.GetOrderByID(orderID)
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

	m.cancel()

	m.actorsMu.Lock()
	for _, act := range m.actors {
		act.cancel()
	}
	exitedChans := m.exitedChans
	m.exitedChans = nil
	// Keep m.actors non-nil so actor goroutines can safely call delete() inside it.
	// Nil it only after all actors have exited.
	m.actorsMu.Unlock()

	// Wait for all actor goroutines to exit by receiving from their done channels.
	for symbol, ch := range exitedChans {
		<-ch
		delete(exitedChans, symbol)
	}

	m.actorsMu.Lock()
	m.actors = nil
	m.actorsMu.Unlock()

	close(m.notify)

	logger.Info("matching engine stopped")
}

// ShutdownNow 立即关闭
func (m *Matcher) ShutdownNow() {
	m.Shutdown()
}

// IsRunning 检查是否运行中
func (m *Matcher) IsRunning() bool {
	return m.running.Load()
}

// Stats 返回统计信息
func (m *Matcher) Stats() map[string]interface{} {
	stats := make(map[string]interface{})
	stats["running"] = m.running.Load()

	m.actorsMu.Lock()
	symbolCount := len(m.actors)
	m.actorsMu.Unlock()
	stats["symbols"] = symbolCount

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
