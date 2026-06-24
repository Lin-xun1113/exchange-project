// Package engine 提供撮合引擎核心实现
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/snapshot"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/notify/consumer"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/shopspring/decimal"
)

// PublisherInterface 发布者接口
type PublisherInterface interface {
	Publish(ctx context.Context, eventType string, event *consumer.Event) error
}

// SnapshotConfig holds configuration for automatic snapshot triggering.
type SnapshotConfig struct {
	MaxTradesPerSnapshot int
	SnapshotInterval     time.Duration
}

// DefaultSnapshotConfig returns sensible defaults for snapshot configuration.
func DefaultSnapshotConfig() SnapshotConfig {
	return SnapshotConfig{
		MaxTradesPerSnapshot: 1000,
		SnapshotInterval:     60 * time.Second,
	}
}

// snapshotTrigger tracks state for automatic snapshot triggering per symbol.
type snapshotTrigger struct {
	mu                   sync.Mutex
	tradesSinceLastSnapshot int
	lastSnapshotTime     time.Time
}

// Matcher 撮合引擎
type Matcher struct {
	actorsMu          sync.Mutex
	actors            map[string]*actor
	exitedChans       map[string]chan struct{}
	actorTimeout      time.Duration
	tradeRepo         TradeRepositoryInterface
	publisher         PublisherInterface
	walManager        *WALManager
	notify            chan *MatchResult
	ctx               context.Context
	cancel            context.CancelFunc
	running           atomic.Bool
	counter           atomic.Int64
	snapshotConfig    SnapshotConfig
	snapshotTriggers  map[string]*snapshotTrigger
	snapshotDir       string
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
	WALDir              string
	SnapshotDir         string
	MaxTradesPerSnapshot int
	SnapshotInterval    time.Duration
}

// WALManager manages WAL instances per symbol.
type WALManager struct {
	mu      sync.Mutex
	walDir  string
	wals    map[string]*wal.WAL
}

// NewWALManager creates a new WAL manager.
func NewWALManager(walDir string) *WALManager {
	return &WALManager{
		walDir: walDir,
		wals:   make(map[string]*wal.WAL),
	}
}

// GetWAL gets or creates a WAL for a symbol.
func (m *WALManager) GetWAL(symbol string) (*wal.WAL, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if w, ok := m.wals[symbol]; ok {
		return w, nil
	}

	w, err := wal.NewWAL(symbol, m.walDir)
	if err != nil {
		return nil, err
	}

	m.wals[symbol] = w
	return w, nil
}

// Close closes all WAL instances.
func (m *WALManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for symbol, w := range m.wals {
		if err := w.Close(); err != nil {
			logger.Error("failed to close WAL",
				logger.S("symbol", symbol),
				logger.Err(err),
			)
			lastErr = err
		}
	}
	return lastErr
}

// Symbols returns all symbols with open WALs.
func (m *WALManager) Symbols() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	symbols := make([]string, 0, len(m.wals))
	for s := range m.wals {
		symbols = append(symbols, s)
	}
	return symbols
}

// NewMatcher 创建撮合引擎
func NewMatcher(cfg Config) *Matcher {
	if cfg.ActorTimeout <= 0 {
		cfg.ActorTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	snapshotCfg := DefaultSnapshotConfig()
	if cfg.MaxTradesPerSnapshot > 0 {
		snapshotCfg.MaxTradesPerSnapshot = cfg.MaxTradesPerSnapshot
	}
	if cfg.SnapshotInterval > 0 {
		snapshotCfg.SnapshotInterval = cfg.SnapshotInterval
	}

	m := &Matcher{
		actors:           make(map[string]*actor),
		exitedChans:      make(map[string]chan struct{}),
		actorTimeout:     cfg.ActorTimeout,
		notify:           make(chan *MatchResult, 10000),
		ctx:              ctx,
		cancel:           cancel,
		snapshotConfig:   snapshotCfg,
		snapshotTriggers: make(map[string]*snapshotTrigger),
		snapshotDir:      cfg.SnapshotDir,
	}

	m.running.Store(true)

	go m.processNotifications()
	go m.runSnapshotMonitor()

	logger.Info("matching engine created",
		logger.S("actor_timeout", cfg.ActorTimeout.String()),
		logger.I("max_trades_per_snapshot", snapshotCfg.MaxTradesPerSnapshot),
		logger.S("snapshot_interval", snapshotCfg.SnapshotInterval.String()),
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
	if tradeRepo == nil && m.walManager == nil {
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

	symbol := ""
	if len(result.Trades) > 0 {
		symbol = result.Trades[0].Symbol
	}

	// WAL: write trade entry before persisting
	if m.walManager != nil && symbol != "" {
		if w, err := m.walManager.GetWAL(symbol); err == nil {
			for _, t := range result.Trades {
				entry := &wal.Entry{
					Type: wal.EntryTypeTrade,
					Payload: wal.TradePayload{
						TradeID:     t.TradeID,
						BuyOrderID:  t.BuyOrderID,
						SellOrderID: t.SellOrderID,
						BuyUserID:   t.BuyUserID,
						SellUserID:  t.SellUserID,
						Price:       t.Price.String(),
						Quantity:    t.Quantity.String(),
						Symbol:      t.Symbol,
						OrderID:     result.OrderID,
					},
				}
				if _, err := w.Append(entry); err != nil {
					logger.Error("failed to write WAL trade entry",
						logger.S("symbol", symbol),
						logger.S("trade_id", t.TradeID),
						logger.Err(err),
					)
				}
			}
		}
	}

	// Check and trigger automatic snapshot
	if symbol != "" && len(result.Trades) > 0 {
		m.checkAndTriggerSnapshot(symbol, len(result.Trades))
	}

	// Persist trades to database asynchronously
	if tradeRepo != nil && len(modelTrades) > 0 {
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

	// WAL: write command entry before dispatch
	if m.walManager != nil {
		if w, err := m.walManager.GetWAL(symbol); err == nil {
			entry := &wal.Entry{
				Type: wal.EntryTypeCommand,
				Payload: wal.CommandPayload{
					OrderID:   orderID,
					UserID:    userID,
					Side:      string(side),
					OrderType: string(orderType),
					Price:     price.String(),
					Quantity:  quantity.String(),
				},
			}
			if _, err := w.Append(entry); err != nil {
				logger.Error("failed to write WAL command entry",
					logger.S("symbol", symbol),
					logger.S("order_id", orderID),
					logger.Err(err),
				)
				return nil, fmt.Errorf("failed to persist WAL entry: %w", err)
			}
		}
	}

	start := time.Now()
	result, err := m.dispatch(ctx, symbol, cmd)
	duration := time.Since(start)

	metrics.GetMetrics().RecordMatchingLatency("submit", symbol, duration)

	if err != nil {
		return nil, err
	}

	if len(result.Trades) > 0 {
		sideStr := "buy"
		if side == model.OrderSideSell {
			sideStr = "sell"
		}
		metrics.GetMetrics().RecordMatchingMatch(sideStr, symbol, float64(len(result.Trades)))
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

	// WAL: write cancel entry before dispatch
	if m.walManager != nil {
		if w, err := m.walManager.GetWAL(symbol); err == nil {
			entry := &wal.Entry{
				Type: wal.EntryTypeCancel,
				Payload: wal.CancelPayload{
					OrderID: orderID,
				},
			}
			if _, err := w.Append(entry); err != nil {
				logger.Error("failed to write WAL cancel entry",
					logger.S("symbol", symbol),
					logger.S("order_id", orderID),
					logger.Err(err),
				)
				return false
			}
		}
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

	start := time.Now()
	select {
	case success := <-replyCh:
		duration := time.Since(start)
		metrics.GetMetrics().RecordMatchingLatency("cancel", symbol, duration)
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

	bids, asks = act.book.GetDepth(depth)

	// Record orderbook depth levels
	if bids != nil {
		metrics.GetMetrics().SetOrderbookDepthLevels("buy", symbol, float64(len(bids)))
		// Phase 3: Record depth bucket histogram
		metrics.GetMetrics().ObserveOrderbookDepthBucket("buy", symbol, float64(len(bids)))
	}
	if asks != nil {
		metrics.GetMetrics().SetOrderbookDepthLevels("sell", symbol, float64(len(asks)))
		// Phase 3: Record depth bucket histogram
		metrics.GetMetrics().ObserveOrderbookDepthBucket("sell", symbol, float64(len(asks)))
	}

	// Phase 3: Record total orders in orderbook
	buyOrders, sellOrders := act.book.GetTotalOrders()
	metrics.GetMetrics().SetOrderbookOrdersTotal("buy", symbol, float64(buyOrders))
	metrics.GetMetrics().SetOrderbookOrdersTotal("sell", symbol, float64(sellOrders))

	return bids, asks
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

	bestBid = act.book.GetBestBid()
	bestAsk = act.book.GetBestAsk()

	if !bestBid.IsZero() {
		metrics.GetMetrics().SetOrderbookBestBid(symbol, bestBid.InexactFloat64())
	}
	if !bestAsk.IsZero() {
		metrics.GetMetrics().SetOrderbookBestAsk(symbol, bestAsk.InexactFloat64())
	}

	return bestBid, bestAsk
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

// RecoveryConfig holds recovery-related configuration.
type RecoveryConfig struct {
	WALDir              string
	SnapshotDir         string
	MaxTradesPerSnapshot int
	SnapshotInterval    time.Duration
}

// Recover performs crash recovery by loading snapshots and replaying WAL entries.
func (m *Matcher) Recover(cfg RecoveryConfig) error {
	if cfg.WALDir == "" || cfg.SnapshotDir == "" {
		logger.Info("WAL/Snapshot dirs not configured, skipping recovery")
		return nil
	}

	logger.Info("starting crash recovery",
		logger.S("wal_dir", cfg.WALDir),
		logger.S("snapshot_dir", cfg.SnapshotDir),
	)

	// Scan for all snapshot symlinks to get symbols that had state.
	symbols := make(map[string]struct{})
	entries, err := os.ReadDir(cfg.SnapshotDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".latest") {
				symbol := strings.TrimSuffix(name, ".latest")
				symbols[symbol] = struct{}{}
			}
		}
	}

	// Also scan WAL directory for symbols.
	// WAL files may be in subdirectories (e.g., walDir/BTC_USDT/BTC_USDT.wal)
	walEntries, err := os.ReadDir(cfg.WALDir)
	if err == nil {
		for _, entry := range walEntries {
			if entry.IsDir() {
				// Check subdirectory for WAL files
				subDir := filepath.Join(cfg.WALDir, entry.Name())
				subEntries, err := os.ReadDir(subDir)
				if err != nil {
					continue
				}
				for _, subEntry := range subEntries {
					if subEntry.IsDir() {
						continue
					}
					name := subEntry.Name()
					if strings.HasSuffix(name, ".wal") {
						// Convert sanitized name back to original (e.g., BTC_USDT -> BTC/USDT)
						sanitizedName := strings.TrimSuffix(name, ".wal")
						originalSymbol := strings.ReplaceAll(sanitizedName, "_", "/")
						symbols[originalSymbol] = struct{}{}
					}
				}
			} else {
				name := entry.Name()
				if strings.HasSuffix(name, ".wal") {
					symbol := strings.TrimSuffix(name, ".wal")
					symbols[symbol] = struct{}{}
				}
			}
		}
	}

	// Also scan snapshot directory for symbols.
	entries, err = os.ReadDir(cfg.SnapshotDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".latest") {
				// Convert sanitized name back to original (e.g., BTC_USDT -> BTC/USDT)
				originalSymbol := strings.ReplaceAll(strings.TrimSuffix(name, ".latest"), "_", "/")
				symbols[originalSymbol] = struct{}{}
			}
		}
	}

	totalSymbols := len(symbols)
	recoveredSymbols := 0
	totalEntries := 0
	startTime := time.Now()

	for symbol := range symbols {
		logger.Info("recovering symbol",
			logger.S("symbol", symbol),
		)

		// Try to load snapshot.
		snap, err := snapshot.Load(symbol, cfg.SnapshotDir)
		startLSN := uint64(0)
		if err == nil {
			startLSN = snap.EndingLSN
			logger.Info("loaded snapshot",
				logger.S("symbol", symbol),
				logger.I64("ending_lsn", int64(startLSN)),
			)
		} else {
			logger.Info("no snapshot found, starting fresh",
				logger.S("symbol", symbol),
			)
		}

		// Create or get actor.
		act := m.getOrCreateActor(symbol)

		// Restore snapshot state.
		if snap != nil {
			if err := snapshot.Restore(snap, act.book); err != nil {
				logger.Error("failed to restore snapshot",
					logger.S("symbol", symbol),
					logger.Err(err),
				)
			}
		}

		// Replay WAL.
		w, err := wal.NewWAL(symbol, cfg.WALDir)
		var entriesReplayed int
		if err != nil {
			logger.Warn("failed to open WAL for replay",
				logger.S("symbol", symbol),
				logger.Err(err),
			)
		} else {
			entriesReplayed = 0
			if err := w.Replay(startLSN, func(entry *wal.Entry) error {
				switch entry.Type {
				case wal.EntryTypeCommand:
					if cmd, ok := entry.Payload.(wal.CommandPayload); ok {
						price, _ := decimal.NewFromString(cmd.Price)
						qty, _ := decimal.NewFromString(cmd.Quantity)
						side := model.OrderSide(cmd.Side)
						orderType := model.OrderType(cmd.OrderType)

						replyCh := make(chan *MatchResult, 1)
						cmd2 := command{
							id:        m.counter.Add(1),
							orderID:   cmd.OrderID,
							userID:    cmd.UserID,
							side:      side,
							orderType: orderType,
							price:     price,
							qty:       qty,
							replyCh:   replyCh,
						}
						act.cmdCh <- cmd2

						// Wait for command execution result before continuing
						select {
						case <-replyCh:
						case <-time.After(m.actorTimeout):
							logger.Warn("replay command timeout",
								logger.S("symbol", symbol),
								logger.S("order_id", cmd.OrderID),
							)
						}
						entriesReplayed++
					}
				case wal.EntryTypeCancel:
					if cancel, ok := entry.Payload.(wal.CancelPayload); ok {
						replyCh := make(chan bool, 1)
						cmd := cancelCommand{
							orderID: cancel.OrderID,
							replyCh: replyCh,
						}
						act.cancelCh <- cmd

						// Wait for cancel execution result before continuing
						select {
						case <-replyCh:
						case <-time.After(m.actorTimeout):
							logger.Warn("replay cancel timeout",
								logger.S("symbol", symbol),
								logger.S("order_id", cancel.OrderID),
							)
						}
						entriesReplayed++
					}
				case wal.EntryTypeTrade:
					entriesReplayed++
				}
				return nil
			}); err != nil {
				logger.Error("WAL replay error",
					logger.S("symbol", symbol),
					logger.Err(err),
				)
			}
			w.Close()
			totalEntries += entriesReplayed
		}
		recoveredSymbols++
		logger.Info("symbol recovered",
			logger.S("symbol", symbol),
			logger.I("entries_replayed", entriesReplayed),
		)
	}

	elapsed := time.Since(startTime)
	logger.Info("recovery complete",
		logger.I("total_symbols", totalSymbols),
		logger.I("recovered_symbols", recoveredSymbols),
		logger.I("total_entries_replayed", totalEntries),
		logger.F("elapsed_seconds", elapsed.Seconds()),
	)

	return nil
}

// TakeSnapshot takes a snapshot for a given symbol and saves it.
func (m *Matcher) TakeSnapshot(symbol string, snapshotDir string) error {
	m.actorsMu.Lock()
	act, ok := m.actors[symbol]
	m.actorsMu.Unlock()
	if !ok {
		return fmt.Errorf("no actor for symbol %s", symbol)
	}

	walLSN := uint64(0)

	// Get WAL reference for atomic truncation (we'll truncate under WAL lock)
	var walForTruncate *wal.WAL
	if m.walManager != nil {
		if w, err := m.walManager.GetWAL(symbol); err == nil {
			// Capture LSN atomically within WAL's lock
			walLSN = w.SnapshotLSN()
			walForTruncate = w
		}
	}

	snap := snapshot.Take(symbol, act.book, walLSN)

	// Save snapshot atomically to proper directory
	if err := snapshot.Save(snap, snapshotDir); err != nil {
		return fmt.Errorf("failed to save snapshot: %w", err)
	}

	// Atomically truncate WAL after snapshot is saved
	// This uses WAL's internal lock to ensure atomicity of save+truncate
	if walForTruncate != nil {
		if err := walForTruncate.Truncate(walLSN); err != nil {
			logger.Warn("failed to truncate WAL after snapshot",
				logger.S("symbol", symbol),
				logger.Err(err),
			)
		}
	}

	return nil
}

// getSnapshotTrigger gets or creates a snapshot trigger for a symbol.
func (m *Matcher) getSnapshotTrigger(symbol string) *snapshotTrigger {
	m.actorsMu.Lock()
	defer m.actorsMu.Unlock()

	if trigger, ok := m.snapshotTriggers[symbol]; ok {
		return trigger
	}

	trigger := &snapshotTrigger{
		tradesSinceLastSnapshot: 0,
		lastSnapshotTime:        time.Now(),
	}
	m.snapshotTriggers[symbol] = trigger
	return trigger
}

// checkAndTriggerSnapshot checks if a snapshot should be triggered and initiates it asynchronously.
func (m *Matcher) checkAndTriggerSnapshot(symbol string, tradeCount int) {
	if m.snapshotDir == "" {
		return
	}

	trigger := m.getSnapshotTrigger(symbol)

	trigger.mu.Lock()
	trigger.tradesSinceLastSnapshot += tradeCount
	shouldTrigger := false
	reason := ""

	if trigger.tradesSinceLastSnapshot >= m.snapshotConfig.MaxTradesPerSnapshot {
		shouldTrigger = true
		reason = "max_trades"
	} else if time.Since(trigger.lastSnapshotTime) >= m.snapshotConfig.SnapshotInterval {
		shouldTrigger = true
		reason = "time_interval"
	}
	trigger.mu.Unlock()

	if shouldTrigger {
		logger.Info("snapshot trigger condition met",
			logger.S("symbol", symbol),
			logger.S("reason", reason),
			logger.I("trades_since_last", trigger.tradesSinceLastSnapshot),
			logger.S("time_since_last", time.Since(trigger.lastSnapshotTime).String()),
		)

		// Reset counter immediately to avoid duplicate triggers
		trigger.mu.Lock()
		trigger.tradesSinceLastSnapshot = 0
		trigger.lastSnapshotTime = time.Now()
		trigger.mu.Unlock()

		// Trigger snapshot asynchronously
		go func(sym string) {
			if err := m.TakeSnapshot(sym, m.snapshotDir); err != nil {
				logger.Error("automatic snapshot failed",
					logger.S("symbol", sym),
					logger.Err(err),
				)
			} else {
				logger.Info("automatic snapshot completed",
					logger.S("symbol", sym),
				)
			}
		}(symbol)
	}
}

// SetSnapshotConfig sets the snapshot configuration.
func (m *Matcher) SetSnapshotConfig(cfg SnapshotConfig) {
	m.snapshotConfig = cfg
}

// SetSnapshotDir sets the snapshot directory.
func (m *Matcher) SetSnapshotDir(dir string) {
	m.snapshotDir = dir
}

// runSnapshotMonitor periodically checks all symbols for time-based snapshot triggers.
// This ensures snapshots are taken even when there are no new trades.
func (m *Matcher) runSnapshotMonitor() {
	// Check interval should be shorter than the snapshot interval to catch triggers close to the deadline
	checkInterval := m.snapshotConfig.SnapshotInterval / 4
	if checkInterval < time.Second {
		checkInterval = time.Second
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkTimeBasedSnapshots()
		}
	}
}

// checkTimeBasedSnapshots checks all symbols for time-based snapshot triggers.
func (m *Matcher) checkTimeBasedSnapshots() {
	if m.snapshotDir == "" {
		return
	}

	m.actorsMu.Lock()
	symbols := make([]string, 0, len(m.actors))
	for symbol := range m.actors {
		symbols = append(symbols, symbol)
	}
	m.actorsMu.Unlock()

	for _, symbol := range symbols {
		m.checkAndTriggerTimeSnapshot(symbol)
	}
}

// checkAndTriggerTimeSnapshot checks if a time-based snapshot should be triggered for a symbol.
func (m *Matcher) checkAndTriggerTimeSnapshot(symbol string) {
	trigger := m.getSnapshotTrigger(symbol)

	trigger.mu.Lock()
	timeSinceLast := time.Since(trigger.lastSnapshotTime)
	shouldTrigger := timeSinceLast >= m.snapshotConfig.SnapshotInterval

	if !shouldTrigger {
		trigger.mu.Unlock()
		return
	}

	// Reset timer immediately
	trigger.lastSnapshotTime = time.Now()
	trigger.mu.Unlock()

	// Check if there are any trades that need a snapshot
	trigger.mu.Lock()
	hasTrades := trigger.tradesSinceLastSnapshot > 0
	trigger.mu.Unlock()

	if !hasTrades {
		// No trades since last snapshot, no need to snapshot
		return
	}

	logger.Info("time-based snapshot trigger condition met",
		logger.S("symbol", symbol),
		logger.S("time_since_last", timeSinceLast.String()),
	)

	// Trigger snapshot asynchronously
	go func(sym string) {
		if err := m.TakeSnapshot(sym, m.snapshotDir); err != nil {
			logger.Error("time-based automatic snapshot failed",
				logger.S("symbol", sym),
				logger.Err(err),
			)
		} else {
			logger.Info("time-based automatic snapshot completed",
				logger.S("symbol", sym),
			)
		}
	}(symbol)
}

// SetWALManager sets the WAL manager.
func (m *Matcher) SetWALManager(wm *WALManager) {
	m.walManager = wm
}
