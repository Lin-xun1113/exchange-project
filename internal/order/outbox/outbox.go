// Package outbox implements the transactional outbox pattern for saga orchestration.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"gorm.io/gorm"
)

// randSource 用于生成 jitter 的随机数源
var randSource = rand.New(rand.NewSource(time.Now().UnixNano()))

// ActionType 定义可用的操作类型
type ActionType string

const (
	ActionFreezeBalance   ActionType = "freeze_balance"
	ActionUnfreezeBalance ActionType = "unfreeze_balance"
	ActionSubmitMatching  ActionType = "submit_matching"
	ActionUpdateStatus    ActionType = "update_status"
)

// EntryStatus 定义条目状态
type EntryStatus string

const (
	StatusPending    EntryStatus = "pending"
	StatusProcessing EntryStatus = "processing"
	StatusDone       EntryStatus = "done"
	StatusFailed     EntryStatus = "failed"
	StatusDeadLetter EntryStatus = "dead_letter"
)

// OutboxEntry 事务性发件箱条目
type OutboxEntry struct {
	ID           int64       `gorm:"primaryKey;autoIncrement" json:"id"`
	SagaID      string      `gorm:"type:varchar(64);not null;index" json:"saga_id"`
	StepName    string      `gorm:"type:varchar(64);not null" json:"step_name"`
	ActionType  ActionType  `gorm:"type:varchar(32);not null" json:"action_type"`
	Payload     string      `gorm:"type:text;not null" json:"payload"`
	Status      EntryStatus `gorm:"type:varchar(32);default:'pending';not null;index" json:"status"`
	RetryCount  int         `gorm:"default:0" json:"retry_count"`
	MaxRetries  int         `gorm:"default:5" json:"max_retries"`
	NextRetryAt *time.Time  `gorm:"default:null;index" json:"next_retry_at,omitempty"`
	CreatedAt   time.Time   `gorm:"autoCreateTime;index" json:"created_at"`
	ProcessedAt *time.Time  `gorm:"default:null" json:"processed_at,omitempty"`
	ErrorMsg    string      `gorm:"type:text" json:"error_msg,omitempty"`
}

// TableName 指定表名
func (OutboxEntry) TableName() string {
	return "outbox"
}

// PayloadData 解析后的payload结构
type PayloadData map[string]interface{}

// GetPayload 解析JSON payload
func (e *OutboxEntry) GetPayload() (PayloadData, error) {
	var data PayloadData
	if err := json.Unmarshal([]byte(e.Payload), &data); err != nil {
		return nil, err
	}
	return data, nil
}

// FreezeBalancePayload 冻结余额的payload
type FreezeBalancePayload struct {
	UserID    int64   `json:"user_id"`
	Amount    float64 `json:"amount"`
	OrderID   string  `json:"order_id,omitempty"`
}

// SubmitMatchingPayload 提交撮合的payload
type SubmitMatchingPayload struct {
	OrderID   string `json:"order_id"`
	UserID    int64  `json:"user_id"`
	Symbol    string `json:"symbol"`
	Side      string `json:"side"`
	OrderType string `json:"order_type"`
	Price     string `json:"price"`
	Quantity  string `json:"quantity"`
}

// UpdateStatusPayload 更新状态的payload
type UpdateStatusPayload struct {
	OrderID        string `json:"order_id"`
	Status         string `json:"status"`
	FilledQuantity string `json:"filled_quantity,omitempty"`
}

// UnfreezeBalancePayload 解冻余额的payload
type UnfreezeBalancePayload struct {
	UserID  int64   `json:"user_id"`
	Amount  float64 `json:"amount"`
	OrderID string  `json:"order_id,omitempty"`
}

// Repository 发件箱仓储接口
type Repository interface {
	Create(ctx context.Context, entry *OutboxEntry) error
	UpdateStatus(ctx context.Context, id int64, status EntryStatus, errMsg string) error
	GetPending(ctx context.Context, limit int) ([]*OutboxEntry, error)
	GetPendingReady(ctx context.Context, limit int) ([]*OutboxEntry, error)
	GetBySagaID(ctx context.Context, sagaID string) ([]*OutboxEntry, error)
	GetStalePending(ctx context.Context, olderThan time.Duration, limit int) ([]*OutboxEntry, error)
	IncrementRetry(ctx context.Context, id int64) error
	UpdateNextRetry(ctx context.Context, id int64, nextRetryAt time.Time) error
}

// GormRepository GORM实现的发件箱仓储
type GormRepository struct {
	db *gorm.DB
}

// NewGormRepository 创建GORM发件箱仓储
func NewGormRepository(db *gorm.DB) *GormRepository {
	return &GormRepository{db: db}
}

// Create 创建发件箱条目
func (r *GormRepository) Create(ctx context.Context, entry *OutboxEntry) error {
	return r.db.WithContext(ctx).Create(entry).Error
}

// UpdateStatus 更新条目状态
func (r *GormRepository) UpdateStatus(ctx context.Context, id int64, status EntryStatus, errMsg string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	if status == StatusDone {
		now := time.Now()
		updates["processed_at"] = &now
	}
	return r.db.WithContext(ctx).Model(&OutboxEntry{}).Where("id = ?", id).Updates(updates).Error
}

// GetPending 获取待处理条目
func (r *GormRepository) GetPending(ctx context.Context, limit int) ([]*OutboxEntry, error) {
	var entries []*OutboxEntry
	query := r.db.WithContext(ctx).
		Where("status = ?", StatusPending).
		Order("created_at ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&entries).Error
	return entries, err
}

// GetPendingReady 获取已到重试时间的待处理条目
func (r *GormRepository) GetPendingReady(ctx context.Context, limit int) ([]*OutboxEntry, error) {
	var entries []*OutboxEntry
	now := time.Now()
	query := r.db.WithContext(ctx).
		Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", StatusPending, now).
		Order("created_at ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&entries).Error
	return entries, err
}

// GetBySagaID 根据Saga ID获取所有条目
func (r *GormRepository) GetBySagaID(ctx context.Context, sagaID string) ([]*OutboxEntry, error) {
	var entries []*OutboxEntry
	err := r.db.WithContext(ctx).
		Where("saga_id = ?", sagaID).
		Order("created_at ASC").
		Find(&entries).Error
	return entries, err
}

// GetStalePending 获取过期的待处理条目（用于恢复）
func (r *GormRepository) GetStalePending(ctx context.Context, olderThan time.Duration, limit int) ([]*OutboxEntry, error) {
	cutoff := time.Now().Add(-olderThan)
	var entries []*OutboxEntry
	err := r.db.WithContext(ctx).
		Where("status IN ? AND created_at < ?", []EntryStatus{StatusPending, StatusProcessing}, cutoff).
		Order("created_at ASC").
		Limit(limit).
		Find(&entries).Error
	return entries, err
}

// IncrementRetry 增加重试计数
func (r *GormRepository) IncrementRetry(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).
		Model(&OutboxEntry{}).
		Where("id = ?", id).
		UpdateColumn("retry_count", gorm.Expr("retry_count + 1")).Error
}

// UpdateNextRetry 更新下次重试时间
func (r *GormRepository) UpdateNextRetry(ctx context.Context, id int64, nextRetryAt time.Time) error {
	return r.db.WithContext(ctx).
		Model(&OutboxEntry{}).
		Where("id = ?", id).
		Update("next_retry_at", &nextRetryAt).Error
}

// DeliveryHandler handles message delivery. Returns nil on success, error on failure.
// The handler should also trigger saga callbacks on success.
type DeliveryHandler func(ctx context.Context, entry *OutboxEntry) error

// ActionHandlers maps action types to delivery handlers
type ActionHandlers map[ActionType]DeliveryHandler

// SuccessCallback is called after an entry is successfully processed
type SuccessCallback func(ctx context.Context, entry *OutboxEntry) error

// CalculateNextRetry 计算下次重试时间（指数退避 + jitter）
// 公式: min(baseDelay * 2^retryCount, maxDelay) + jitter
func CalculateNextRetry(retryCount int, baseDelay time.Duration) time.Time {
	const maxDelay = 5 * time.Minute
	const jitterFraction = 0.25

	// 指数退避: baseDelay * 2^retryCount
	delay := baseDelay
	for i := 0; i < retryCount && delay < maxDelay; i++ {
		delay *= 2
	}
	if delay > maxDelay {
		delay = maxDelay
	}

	// 添加 jitter: ±jitterFraction
	jitter := time.Duration(0)
	jitterRange := time.Duration(float64(delay) * jitterFraction)
	if jitterRange > 0 {
		jitter = time.Duration(randSource.Int63n(int64(jitterRange*2))) - jitterRange
	}

	return time.Now().Add(delay + jitter)
}

// Worker outbox background worker
type Worker struct {
	repo      Repository
	handlers  ActionHandlers
	callbacks map[ActionType]SuccessCallback
	config    WorkerConfig
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// WorkerConfig worker configuration
type WorkerConfig struct {
	PollInterval     time.Duration // Polling interval, default 1 second
	RetryDelay       time.Duration // Base retry delay for exponential backoff, default 5 seconds
	MaxRetries       int          // Maximum retries, default 5
	StaleTimeout     time.Duration // Timeout for stuck processing entries, default 30 seconds
}

// DefaultWorkerConfig returns default configuration
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		PollInterval: 1 * time.Second,
		RetryDelay:   5 * time.Second,
		MaxRetries:   5,
		StaleTimeout: 30 * time.Second,
	}
}

// NewWorker creates a new outbox worker
func NewWorker(repo Repository, handlers ActionHandlers, config WorkerConfig) *Worker {
	return &Worker{
		repo:      repo,
		handlers:  handlers,
		callbacks: make(map[ActionType]SuccessCallback),
		config:    config,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// RegisterCallback registers a success callback for an action type
func (w *Worker) RegisterCallback(actionType ActionType, callback SuccessCallback) {
	w.callbacks[actionType] = callback
}

// Start starts the worker
func (w *Worker) Start(ctx context.Context) {
	logger.Info("outbox worker started")
	go w.run(ctx)
}

// Stop stops the worker
func (w *Worker) Stop() {
	logger.Info("stopping outbox worker")
	close(w.stopCh)
	<-w.doneCh
	logger.Info("outbox worker stopped")
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.doneCh)

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.processEntries(ctx)
		}
	}
}

func (w *Worker) processEntries(ctx context.Context) {
	// 首先处理过期的 processing 条目（防止 worker crash 后遗留下来的条目）
	w.recoverStaleProcessing(ctx)

	entries, err := w.repo.GetPendingReady(ctx, 100)
	if err != nil {
		logger.Error("failed to get ready pending outbox entries", logger.Err(err))
		return
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		default:
			w.processEntry(ctx, entry)
		}
	}
}

// recoverStaleProcessing recovers entries stuck in processing state
func (w *Worker) recoverStaleProcessing(ctx context.Context) {
	entries, err := w.repo.GetStalePending(ctx, w.config.StaleTimeout, 100)
	if err != nil {
		logger.Error("failed to get stale processing entries", logger.Err(err))
		return
	}

	for _, entry := range entries {
		logger.Warn("recovering stale outbox entry",
			logger.I64("entry_id", entry.ID),
			logger.S("saga_id", entry.SagaID),
			logger.S("status", string(entry.Status)),
		)
		if err := w.repo.UpdateStatus(ctx, entry.ID, StatusPending, "stale recovery"); err != nil {
			logger.Error("failed to reset stale processing entry",
				logger.I64("entry_id", entry.ID),
				logger.Err(err),
			)
		}
	}
}

func (w *Worker) processEntry(ctx context.Context, entry *OutboxEntry) {
	startTime := time.Now()

	handler, ok := w.handlers[entry.ActionType]
	if !ok {
		logger.Error("no handler for action type",
			logger.S("action_type", string(entry.ActionType)),
			logger.S("saga_id", entry.SagaID),
			logger.S("step_name", entry.StepName),
		)
		w.repo.UpdateStatus(ctx, entry.ID, StatusFailed, fmt.Sprintf("no handler for action type: %s", entry.ActionType))
		return
	}

	if err := w.repo.UpdateStatus(ctx, entry.ID, StatusProcessing, ""); err != nil {
		logger.Error("failed to update entry status to processing",
			logger.I64("entry_id", entry.ID),
			logger.Err(err),
		)
		return
	}

	err := handler(ctx, entry)
	duration := time.Since(startTime)

	// Phase 3: Record processing duration
	metrics.GetMetrics().RecordOutboxProcessingDuration(string(entry.ActionType), duration)

	if err != nil {
		logger.Error("handler failed",
			logger.S("saga_id", entry.SagaID),
			logger.S("step_name", entry.StepName),
			logger.Err(err),
		)

		metrics.GetMetrics().RecordSagaRetry(entry.StepName)
		w.repo.IncrementRetry(ctx, entry.ID)

		newRetryCount := entry.RetryCount + 1
		if newRetryCount >= w.config.MaxRetries {
			w.repo.UpdateStatus(ctx, entry.ID, StatusDeadLetter, err.Error())
			logger.Error("outbox entry moved to dead letter",
				logger.I64("entry_id", entry.ID),
				logger.S("saga_id", entry.SagaID),
				logger.I("retry_count", newRetryCount),
			)
		} else {
		// 计算下次重试时间（指数退避 + jitter）
		nextRetryAt := CalculateNextRetry(newRetryCount, w.config.RetryDelay)
		w.repo.UpdateNextRetry(ctx, entry.ID, nextRetryAt)
		w.repo.UpdateStatus(ctx, entry.ID, StatusPending, err.Error())
		logger.Warn("outbox entry scheduled for retry",
			logger.I64("entry_id", entry.ID),
			logger.S("saga_id", entry.SagaID),
			logger.I("retry_count", newRetryCount),
			logger.Any("next_retry_at", nextRetryAt),
		)
		}
		return
	}

	// Mark as done
	if err := w.repo.UpdateStatus(ctx, entry.ID, StatusDone, ""); err != nil {
		logger.Error("failed to mark entry as done",
			logger.I64("entry_id", entry.ID),
			logger.Err(err),
		)
		return
	}

	logger.Debug("outbox entry processed successfully",
		logger.S("saga_id", entry.SagaID),
		logger.S("step_name", entry.StepName),
		logger.S("action_type", string(entry.ActionType)),
		logger.F("duration_ms", duration.Seconds()*1000),
	)

	// Call success callback if registered
	if callback, ok := w.callbacks[entry.ActionType]; ok {
		if err := callback(ctx, entry); err != nil {
			logger.Error("success callback failed, creating retry entry",
				logger.S("saga_id", entry.SagaID),
				logger.S("step_name", entry.StepName),
				logger.Err(err),
			)

			// Create a new outbox entry to retry the callback
			retryEntry := &OutboxEntry{
				SagaID:     entry.SagaID,
				StepName:   entry.StepName + "_callback_retry",
				ActionType: entry.ActionType,
				Payload:    entry.Payload,
				Status:     StatusPending,
				MaxRetries: 3,
			}
			if createErr := w.repo.Create(ctx, retryEntry); createErr != nil {
				logger.Error("failed to create callback retry entry",
					logger.S("saga_id", entry.SagaID),
					logger.Err(createErr),
				)
			} else {
				logger.Info("created callback retry entry",
					logger.I64("retry_entry_id", retryEntry.ID),
					logger.S("saga_id", entry.SagaID),
					logger.S("step_name", entry.StepName),
				)
			}
		}
	}
}

// RecoverStaleEntries 恢复过期的条目（用于启动时）
func (w *Worker) RecoverStaleEntries(ctx context.Context, olderThan time.Duration) error {
	entries, err := w.repo.GetStalePending(ctx, olderThan, 100)
	if err != nil {
		return fmt.Errorf("failed to get stale entries: %w", err)
	}

	for _, entry := range entries {
		logger.Info("recovering stale outbox entry",
			logger.I64("entry_id", entry.ID),
			logger.S("saga_id", entry.SagaID),
			logger.S("status", string(entry.Status)),
		)
		if err := w.repo.UpdateStatus(ctx, entry.ID, StatusPending, ""); err != nil {
			logger.Error("failed to reset stale entry",
				logger.I64("entry_id", entry.ID),
				logger.Err(err),
			)
		}
	}

	// Phase 3: Update pending entries gauge after recovery
	w.updatePendingEntriesGauge(ctx)

	return nil
}

// updatePendingEntriesGauge updates the pending entries gauge metric
func (w *Worker) updatePendingEntriesGauge(ctx context.Context) {
	entries, err := w.repo.GetPending(ctx, 0) // Get all pending entries
	if err != nil {
		logger.Error("failed to get pending entries for metrics", logger.Err(err))
		return
	}

	// Count by status
	statusCounts := make(map[EntryStatus]int)
	for _, e := range entries {
		statusCounts[e.Status]++
	}

	// Update metrics
	metrics.GetMetrics().SetOutboxPendingEntries(string(StatusPending), float64(statusCounts[StatusPending]))
	metrics.GetMetrics().SetOutboxPendingEntries(string(StatusProcessing), float64(statusCounts[StatusProcessing]))
	metrics.GetMetrics().SetOutboxPendingEntries(string(StatusFailed), float64(statusCounts[StatusFailed]))
	metrics.GetMetrics().SetOutboxPendingEntries(string(StatusDone), float64(statusCounts[StatusDone]))
	metrics.GetMetrics().SetOutboxPendingEntries(string(StatusDeadLetter), float64(statusCounts[StatusDeadLetter]))
}

// StartWithMetrics starts the worker with periodic metrics collection
func (w *Worker) StartWithMetrics(ctx context.Context) {
	logger.Info("outbox worker started with metrics")
	go w.runWithMetrics(ctx)
}

// runWithMetrics runs the worker with periodic metrics collection
func (w *Worker) runWithMetrics(ctx context.Context) {
	defer close(w.doneCh)

	pollTicker := time.NewTicker(w.config.PollInterval)
	defer pollTicker.Stop()

	// Update metrics every 5 seconds
	metricsTicker := time.NewTicker(5 * time.Second)
	defer metricsTicker.Stop()

	// Initial metrics update
	w.updatePendingEntriesGauge(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-pollTicker.C:
			w.processEntries(ctx)
		case <-metricsTicker.C:
			w.updatePendingEntriesGauge(ctx)
		}
	}
}

// StopWithMetrics stops the worker with metrics collection
func (w *Worker) StopWithMetrics() {
	logger.Info("stopping outbox worker with metrics")
	close(w.stopCh)
	<-w.doneCh
	logger.Info("outbox worker stopped")
}
