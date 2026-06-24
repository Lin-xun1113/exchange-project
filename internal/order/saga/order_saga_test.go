package saga

import (
	"context"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestDB 创建测试用的内存SQLite数据库
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	// 自动迁移
	err = db.AutoMigrate(&model.Order{})
	require.NoError(t, err)

	return db
}

// mockOrderRepo 模拟订单仓储
type mockOrderRepo struct {
	orders map[string]*model.Order
}

func newMockOrderRepo() *mockOrderRepo {
	return &mockOrderRepo{
		orders: make(map[string]*model.Order),
	}
}

func (m *mockOrderRepo) GetByIdempotencyKey(ctx context.Context, key string) (*model.Order, error) {
	for _, order := range m.orders {
		if order.IdempotencyKey == key {
			return order, nil
		}
	}
	return nil, nil
}

func (m *mockOrderRepo) GetByOrderID(ctx context.Context, orderID string) (*model.Order, error) {
	order, ok := m.orders[orderID]
	if !ok {
		return nil, nil
	}
	return order, nil
}

func (m *mockOrderRepo) UpdateStatus(ctx context.Context, orderID string, status model.OrderStatus, filledQty float64) error {
	order, ok := m.orders[orderID]
	if !ok {
		return nil
	}
	order.Status = string(status)
	order.FilledQuantity = filledQty
	return nil
}

// mockOutboxRepo 模拟outbox仓储
type mockOutboxRepo struct {
	entries []*mockOutboxEntry
}

type mockOutboxEntry struct {
	SagaID    string
	StepName  string
	ActionType string
	Payload   string
	Status    string
}

func newMockOutboxRepo() *mockOutboxRepo {
	return &mockOutboxRepo{
		entries: make([]*mockOutboxEntry, 0),
	}
}

func (m *mockOutboxRepo) Create(ctx context.Context, entry interface{}) error {
	// Simplified mock
	return nil
}

func (m *mockOutboxRepo) UpdateStatus(ctx context.Context, id int64, status interface{}, errMsg string) error {
	return nil
}

func (m *mockOutboxRepo) GetPending(ctx context.Context, limit int) (interface{}, error) {
	return nil, nil
}

func (m *mockOutboxRepo) GetBySagaID(ctx context.Context, sagaID string) (interface{}, error) {
	return nil, nil
}

func (m *mockOutboxRepo) GetStalePending(ctx context.Context, olderThan time.Duration, limit int) (interface{}, error) {
	return nil, nil
}

func (m *mockOutboxRepo) IncrementRetry(ctx context.Context, id int64) error {
	return nil
}

func TestOrderSaga_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name     string
		from     model.OrderStatus
		to       model.OrderStatus
		expected bool
	}{
		{"Created to Frozen", model.OrderStatusCreated, model.OrderStatusFrozen, true},
		{"Created to Cancelled", model.OrderStatusCreated, model.OrderStatusCancelled, true},
		{"Frozen to Pending", model.OrderStatusFrozen, model.OrderStatusPending, true},
		{"Frozen to Cancelled", model.OrderStatusFrozen, model.OrderStatusCancelled, true},
		{"Pending to PartialFilled", model.OrderStatusPending, model.OrderStatusPartialFilled, true},
		{"Pending to Filled", model.OrderStatusPending, model.OrderStatusFilled, true},
		{"Pending to Cancelled", model.OrderStatusPending, model.OrderStatusCancelled, true},
		{"PartialFilled to Filled", model.OrderStatusPartialFilled, model.OrderStatusFilled, true},
		{"Filled to Settled", model.OrderStatusFilled, model.OrderStatusSettled, true},
		{"Cancelled to Filled", model.OrderStatusCancelled, model.OrderStatusFilled, false},
		{"Filled to Cancelled", model.OrderStatusFilled, model.OrderStatusCancelled, false},
		{"Created to Filled", model.OrderStatusCreated, model.OrderStatusFilled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.from.CanTransitionTo(tt.to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOrderSaga_IsFinalStatus(t *testing.T) {
	tests := []struct {
		status   model.OrderStatus
		expected bool
	}{
		{model.OrderStatusFilled, true},
		{model.OrderStatusCancelled, true},
		{model.OrderStatusRejected, true},
		{model.OrderStatusSettled, true},
		{model.OrderStatusPending, false},
		{model.OrderStatusPartialFilled, false},
		{model.OrderStatusCreated, false},
		{model.OrderStatusFrozen, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			result := tt.status.IsFinalStatus()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOrderSaga_GenerateOrderID(t *testing.T) {
	id1 := generateOrderID()
	id2 := generateOrderID()

	// 验证格式
	assert.True(t, len(id1) > 10)
	assert.True(t, len(id2) > 10)

	// 验证包含前缀
	assert.Contains(t, id1, "ORD")
	assert.Contains(t, id2, "ORD")

	// 验证唯一性
	assert.NotEqual(t, id1, id2)
}
