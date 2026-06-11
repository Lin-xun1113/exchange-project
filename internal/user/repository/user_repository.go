// Package repository 提供用户仓储层实现
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
)

// UserRepository 用户仓储
type UserRepository struct {
	db    *gorm.DB
	redis *redis.Client
}

// NewUserRepository 创建用户仓储
func NewUserRepository(db *gorm.DB, redisClient *redis.Client) *UserRepository {
	return &UserRepository{
		db:    db,
		redis: redisClient,
	}
}

// Create 创建用户
func (r *UserRepository) Create(ctx context.Context, user *model.User) error {
	result := r.db.WithContext(ctx).Create(user)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// GetByID 根据ID获取用户
func (r *UserRepository) GetByID(ctx context.Context, id int64) (*model.User, error) {
	// Cache-Aside: 先查缓存
	cacheKey := fmt.Sprintf("user:%d", id)
	cached, err := r.redis.Get(ctx, cacheKey).Result()
	if err == nil {
		var user model.User
		if json.Unmarshal([]byte(cached), &user) == nil {
			return &user, nil
		}
	}

	// 缓存未命中，查数据库
	var user model.User
	result := r.db.WithContext(ctx).First(&user, id)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, errors.ErrUserNotFound
		}
		return nil, result.Error
	}

	// 写入缓存
	if data, err := json.Marshal(user); err == nil {
		r.redis.Set(ctx, cacheKey, data, 5*time.Minute)
	}

	return &user, nil
}

// GetByUsername 根据用户名获取用户
func (r *UserRepository) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	result := r.db.WithContext(ctx).Where("username = ?", username).First(&user)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, errors.ErrUserNotFound
		}
		return nil, result.Error
	}
	return &user, nil
}

// Update 更新用户
func (r *UserRepository) Update(ctx context.Context, user *model.User) error {
	result := r.db.WithContext(ctx).Save(user)
	if result.Error != nil {
		return result.Error
	}

	// 清除缓存
	cacheKey := fmt.Sprintf("user:%d", user.ID)
	r.redis.Del(ctx, cacheKey)

	return nil
}

// GetBalance 获取余额（带锁）
func (r *UserRepository) GetBalance(ctx context.Context, userID int64) (available, frozen float64, err error) {
	var user model.User
	result := r.db.WithContext(ctx).Where("id = ?", userID).First(&user)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return 0, 0, errors.ErrUserNotFound
		}
		return 0, 0, result.Error
	}
	return user.Balance, user.FrozenBalance, nil
}

// FreezeAmount 冻结余额（事务内执行）
func (r *UserRepository) FreezeAmount(ctx context.Context, userID int64, amount float64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses().Where("id = ?", userID).First(&user).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.ErrUserNotFound
			}
			return err
		}

		if user.Balance < amount {
			return errors.ErrBalance
		}

		user.Balance -= amount
		user.FrozenBalance += amount

		return tx.Save(&user).Error
	})
}

// UnfreezeAmount 解冻余额（事务内执行）
func (r *UserRepository) UnfreezeAmount(ctx context.Context, userID int64, amount float64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses().Where("id = ?", userID).First(&user).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.ErrUserNotFound
			}
			return err
		}

		if user.FrozenBalance < amount {
			return fmt.Errorf("frozen balance insufficient")
		}

		user.FrozenBalance -= amount
		user.Balance += amount

		return tx.Save(&user).Error
	})
}

// DeductFrozenBalance 扣减冻结余额（成交时调用）
func (r *UserRepository) DeductFrozenBalance(ctx context.Context, userID int64, amount float64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses().Where("id = ?", userID).First(&user).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.ErrUserNotFound
			}
			return err
		}

		if user.FrozenBalance < amount {
			return fmt.Errorf("frozen balance insufficient")
		}

		user.FrozenBalance -= amount
		return tx.Save(&user).Error
	})
}

// AddBalance 增加余额
func (r *UserRepository) AddBalance(ctx context.Context, userID int64, amount float64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses().Where("id = ?", userID).First(&user).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.ErrUserNotFound
			}
			return err
		}

		user.Balance += amount
		return tx.Save(&user).Error
	})
}

// GetUserRoles 获取用户角色
func (r *UserRepository) GetUserRoles(ctx context.Context, userID int64) ([]string, error) {
	var user model.User
	result := r.db.WithContext(ctx).Preload("Roles").First(&user, userID)
	if result.Error != nil {
		return nil, result.Error
	}

	roleNames := make([]string, 0, len(user.Roles))
	for _, role := range user.Roles {
		roleNames = append(roleNames, role.Name)
	}
	return roleNames, nil
}
