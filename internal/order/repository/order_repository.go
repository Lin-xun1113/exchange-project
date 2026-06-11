// Package repository 提供订单仓储层实现
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OrderRepository 订单仓储
type OrderRepository struct {
	db    *gorm.DB
	redis *redis.Client
}

// NewOrderRepository 创建订单仓储
func NewOrderRepository(db *gorm.DB, redisClient *redis.Client) *OrderRepository {
	return &OrderRepository{
		db:    db,
		redis: redisClient,
	}
}

// Create 创建订单
func (r *OrderRepository) Create(ctx context.Context, order *model.Order) error {
	result := r.db.WithContext(ctx).Create(order)
	return result.Error
}

// GetByID 根据ID获取订单
func (r *OrderRepository) GetByID(ctx context.Context, id int64) (*model.Order, error) {
	var order model.Order
	result := r.db.WithContext(ctx).First(&order, id)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, errors.ErrOrderNotFound
		}
		return nil, result.Error
	}
	return &order, nil
}

// GetByOrderID 根据订单ID获取订单
func (r *OrderRepository) GetByOrderID(ctx context.Context, orderID string) (*model.Order, error) {
	// Cache-Aside: 先查缓存
	cacheKey := fmt.Sprintf("order:%s", orderID)
	cached, err := r.redis.Get(ctx, cacheKey).Result()
	if err == nil {
		var order model.Order
		if json.Unmarshal([]byte(cached), &order) == nil {
			return &order, nil
		}
	}

	var order model.Order
	result := r.db.WithContext(ctx).Where("order_id = ?", orderID).First(&order)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, errors.ErrOrderNotFound
		}
		return nil, result.Error
	}

	// 写入缓存
	if data, err := json.Marshal(order); err == nil {
		r.redis.Set(ctx, cacheKey, data, 5*time.Minute)
	}

	return &order, nil
}

// GetByIdempotencyKey 根据幂等键获取订单
func (r *OrderRepository) GetByIdempotencyKey(ctx context.Context, key string) (*model.Order, error) {
	var order model.Order
	result := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&order)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &order, nil
}

// Update 更新订单
func (r *OrderRepository) Update(ctx context.Context, order *model.Order) error {
	result := r.db.WithContext(ctx).Save(order)
	if result.Error != nil {
		return result.Error
	}

	// 清除缓存
	cacheKey := fmt.Sprintf("order:%s", order.OrderID)
	r.redis.Del(ctx, cacheKey)

	return nil
}

// UpdateStatus 更新订单状态
func (r *OrderRepository) UpdateStatus(ctx context.Context, orderID string, status model.OrderStatus, filledQty float64) error {
	result := r.db.WithContext(ctx).Model(&model.Order{}).
		Where("order_id = ?", orderID).
		Updates(map[string]interface{}{
			"status":          status,
			"filled_quantity": filledQty,
		})
	if result.Error != nil {
		return result.Error
	}

	// 清除缓存
	cacheKey := fmt.Sprintf("order:%s", orderID)
	r.redis.Del(ctx, cacheKey)

	return nil
}

// ListByUserID 列出用户的订单
func (r *OrderRepository) ListByUserID(ctx context.Context, userID int64, symbol string, status string, page, pageSize int) ([]*model.Order, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Order{}).Where("user_id = ?", userID)

	if symbol != "" {
		query = query.Where("symbol = ?", symbol)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	// 统计总数
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 分页查询
	offset := (page - 1) * pageSize
	var orders []*model.Order
	result := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&orders)
	if result.Error != nil {
		return nil, 0, result.Error
	}

	return orders, total, nil
}

// CancelOrder 取消订单
func (r *OrderRepository) CancelOrder(ctx context.Context, orderID string, userID int64) error {
	result := r.db.WithContext(ctx).Model(&model.Order{}).
		Where("order_id = ? AND user_id = ? AND status IN ?", orderID, userID, []string{"pending", "partial_filled"}).
		Update("status", model.OrderStatusCancelled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.ErrOrderInvalid
	}

	// 清除缓存
	cacheKey := fmt.Sprintf("order:%s", orderID)
	r.redis.Del(ctx, cacheKey)

	return nil
}

// GetPendingOrders 获取待撮合订单
func (r *OrderRepository) GetPendingOrders(ctx context.Context, symbol string, limit int) ([]*model.Order, error) {
	var orders []*model.Order
	result := r.db.WithContext(ctx).
		Where("symbol = ? AND status = ?", symbol, model.OrderStatusPending).
		Order("price DESC, created_at ASC").
		Limit(limit).
		Find(&orders)
	if result.Error != nil {
		return nil, result.Error
	}
	return orders, nil
}

// Upsert 幂等插入或更新
func (r *OrderRepository) Upsert(ctx context.Context, order *model.Order) error {
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "idempotency_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"filled_quantity", "status", "updated_at"}),
	}).Create(order)
	return result.Error
}
