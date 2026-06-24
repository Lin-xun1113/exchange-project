package outbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	err = db.AutoMigrate(&OutboxEntry{})
	require.NoError(t, err)

	return db
}

func TestOutboxEntry_GetPayload(t *testing.T) {
	payload := map[string]interface{}{
		"user_id": 123,
		"amount":  100.50,
		"order_id": "ORD123",
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	entry := &OutboxEntry{
		ID:        1,
		SagaID:    "saga-123",
		StepName:  "freeze_balance",
		ActionType: ActionFreezeBalance,
		Payload:   string(payloadBytes),
	}

	data, err := entry.GetPayload()
	require.NoError(t, err)
	assert.Equal(t, float64(123), data["user_id"])
	assert.Equal(t, 100.50, data["amount"])
	assert.Equal(t, "ORD123", data["order_id"])
}

func TestOutboxEntry_GetPayload_InvalidJSON(t *testing.T) {
	entry := &OutboxEntry{
		Payload: "invalid json",
	}

	_, err := entry.GetPayload()
	assert.Error(t, err)
}

func TestGormRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	repo := NewGormRepository(db)

	entry := &OutboxEntry{
		SagaID:     "saga-123",
		StepName:   "freeze_balance",
		ActionType: ActionFreezeBalance,
		Payload:    `{"user_id": 123}`,
		Status:     StatusPending,
	}

	err := repo.Create(context.Background(), entry)
	require.NoError(t, err)
	assert.NotZero(t, entry.ID)
}

func TestGormRepository_GetPending(t *testing.T) {
	db := setupTestDB(t)
	repo := NewGormRepository(db)

	// 创建多个条目
	entries := []*OutboxEntry{
		{
			SagaID:     "saga-1",
			StepName:   "step1",
			ActionType: ActionFreezeBalance,
			Payload:    "{}",
			Status:     StatusPending,
		},
		{
			SagaID:     "saga-2",
			StepName:   "step1",
			ActionType: ActionFreezeBalance,
			Payload:    "{}",
			Status:     StatusDone,
		},
		{
			SagaID:     "saga-3",
			StepName:   "step1",
			ActionType: ActionFreezeBalance,
			Payload:    "{}",
			Status:     StatusPending,
		},
	}

	for _, entry := range entries {
		err := repo.Create(context.Background(), entry)
		require.NoError(t, err)
	}

	pending, err := repo.GetPending(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, pending, 2)
}

func TestGormRepository_GetBySagaID(t *testing.T) {
	db := setupTestDB(t)
	repo := NewGormRepository(db)

	sagaID := "saga-123"
	entries := []*OutboxEntry{
		{
			SagaID:     sagaID,
			StepName:   "freeze_balance",
			ActionType: ActionFreezeBalance,
			Payload:    "{}",
			Status:     StatusPending,
		},
		{
			SagaID:     sagaID,
			StepName:   "submit_matching",
			ActionType: ActionSubmitMatching,
			Payload:    "{}",
			Status:     StatusPending,
		},
		{
			SagaID:     "other-saga",
			StepName:   "step1",
			ActionType: ActionFreezeBalance,
			Payload:    "{}",
			Status:     StatusPending,
		},
	}

	for _, entry := range entries {
		err := repo.Create(context.Background(), entry)
		require.NoError(t, err)
	}

	sagaEntries, err := repo.GetBySagaID(context.Background(), sagaID)
	require.NoError(t, err)
	assert.Len(t, sagaEntries, 2)
}

func TestGormRepository_UpdateStatus(t *testing.T) {
	db := setupTestDB(t)
	repo := NewGormRepository(db)

	entry := &OutboxEntry{
		SagaID:     "saga-123",
		StepName:   "freeze_balance",
		ActionType: ActionFreezeBalance,
		Payload:    "{}",
		Status:     StatusPending,
	}
	err := repo.Create(context.Background(), entry)
	require.NoError(t, err)

	err = repo.UpdateStatus(context.Background(), entry.ID, StatusDone, "")
	require.NoError(t, err)

	// 验证状态已更新
	entries, err := repo.GetPending(context.Background(), 10)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestGormRepository_IncrementRetry(t *testing.T) {
	db := setupTestDB(t)
	repo := NewGormRepository(db)

	entry := &OutboxEntry{
		SagaID:     "saga-123",
		StepName:   "freeze_balance",
		ActionType: ActionFreezeBalance,
		Payload:    "{}",
		Status:     StatusPending,
		RetryCount: 0,
	}
	err := repo.Create(context.Background(), entry)
	require.NoError(t, err)

	err = repo.IncrementRetry(context.Background(), entry.ID)
	require.NoError(t, err)

	// 验证重试次数已增加
	entries, err := repo.GetPending(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, entries[0].RetryCount)
}

func TestWorkerConfig_DefaultConfig(t *testing.T) {
	cfg := DefaultWorkerConfig()

	assert.Equal(t, 1*time.Second, cfg.PollInterval)
	assert.Equal(t, 5*time.Second, cfg.RetryDelay)
	assert.Equal(t, 5, cfg.MaxRetries)
}

func TestActionTypes(t *testing.T) {
	assert.Equal(t, ActionType("freeze_balance"), ActionFreezeBalance)
	assert.Equal(t, ActionType("unfreeze_balance"), ActionUnfreezeBalance)
	assert.Equal(t, ActionType("submit_matching"), ActionSubmitMatching)
	assert.Equal(t, ActionType("update_status"), ActionUpdateStatus)
}

func TestEntryStatuses(t *testing.T) {
	assert.Equal(t, EntryStatus("pending"), StatusPending)
	assert.Equal(t, EntryStatus("processing"), StatusProcessing)
	assert.Equal(t, EntryStatus("done"), StatusDone)
	assert.Equal(t, EntryStatus("failed"), StatusFailed)
	assert.Equal(t, EntryStatus("dead_letter"), StatusDeadLetter)
}

func TestCalculateNextRetry(t *testing.T) {
	baseDelay := 5 * time.Second

	// Test exponential backoff
	t.Run("exponential backoff", func(t *testing.T) {
		retry0 := CalculateNextRetry(0, baseDelay)
		retry1 := CalculateNextRetry(1, baseDelay)
		retry2 := CalculateNextRetry(2, baseDelay)

		// Each retry should have a later time than the previous
		assert.True(t, retry1.After(retry0))
		assert.True(t, retry2.After(retry1))
	})

	t.Run("capped at max delay", func(t *testing.T) {
		// Very high retry count should be capped at 5 minutes
		retryHigh := CalculateNextRetry(100, baseDelay)
		maxDelay := 5 * time.Minute

		actualDelay := retryHigh.Sub(time.Now())
		assert.LessOrEqual(t, actualDelay, maxDelay+maxDelay*25/100) // Account for jitter
	})

	t.Run("jitter variation", func(t *testing.T) {
		// Multiple calculations with same retry count should give different results
		results := make(map[int64]bool)
		for i := 0; i < 100; i++ {
			retry := CalculateNextRetry(2, baseDelay)
			// Group by 100ms buckets
			bucket := retry.UnixNano() / (100 * time.Millisecond.Nanoseconds())
			results[bucket] = true
		}
		// Should have multiple buckets (jitter working)
		assert.Greater(t, len(results), 1)
	})
}

func TestGormRepository_GetPendingReady(t *testing.T) {
	db := setupTestDB(t)
	repo := NewGormRepository(db)
	now := time.Now()

	entries := []*OutboxEntry{
		{
			SagaID:      "saga-ready",
			StepName:    "step1",
			ActionType:  ActionFreezeBalance,
			Payload:     "{}",
			Status:      StatusPending,
			NextRetryAt: nil, // Should be ready immediately
		},
		{
			SagaID:      "saga-past",
			StepName:    "step2",
			ActionType:  ActionFreezeBalance,
			Payload:     "{}",
			Status:      StatusPending,
			NextRetryAt: func() *time.Time { t := now.Add(-time.Second); return &t }(), // Past, should be ready
		},
		{
			SagaID:      "saga-future",
			StepName:    "step3",
			ActionType:  ActionFreezeBalance,
			Payload:     "{}",
			Status:      StatusPending,
			NextRetryAt: func() *time.Time { t := now.Add(time.Hour); return &t }(), // Future, not ready
		},
		{
			SagaID:      "saga-done",
			StepName:    "step4",
			ActionType:  ActionFreezeBalance,
			Payload:     "{}",
			Status:      StatusDone,
			NextRetryAt: nil,
		},
	}

	for _, entry := range entries {
		err := repo.Create(context.Background(), entry)
		require.NoError(t, err)
	}

	ready, err := repo.GetPendingReady(context.Background(), 100)
	require.NoError(t, err)
	assert.Len(t, ready, 2) // "saga-ready" and "saga-past"
}

func TestGormRepository_UpdateNextRetry(t *testing.T) {
	db := setupTestDB(t)
	repo := NewGormRepository(db)

	entry := &OutboxEntry{
		SagaID:     "saga-123",
		StepName:   "freeze_balance",
		ActionType: ActionFreezeBalance,
		Payload:    "{}",
		Status:     StatusPending,
	}
	err := repo.Create(context.Background(), entry)
	require.NoError(t, err)

	nextRetry := time.Now().Add(10 * time.Second)
	err = repo.UpdateNextRetry(context.Background(), entry.ID, nextRetry)
	require.NoError(t, err)

	// Verify the update
	var updated *OutboxEntry
	err = db.First(&updated, entry.ID).Error
	require.NoError(t, err)
	assert.NotNil(t, updated.NextRetryAt)
	assert.WithinDuration(t, nextRetry, *updated.NextRetryAt, time.Second)
}
