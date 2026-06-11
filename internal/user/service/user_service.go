// Package service 提供用户服务层实现
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/user/repository"
	"github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// UserService 用户服务
type UserService struct {
	repo       *repository.UserRepository
	redis      *redis.Client
	jwtSecret  string
	expireTime time.Duration
}

// NewUserService 创建用户服务
func NewUserService(repo *repository.UserRepository, redisClient *redis.Client, jwtSecret string, expireTime time.Duration) *UserService {
	return &UserService{
		repo:       repo,
		redis:      redisClient,
		jwtSecret:  jwtSecret,
		expireTime: expireTime,
	}
}

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, username, password, email string, initialBalance float64) (*model.User, error) {
	existing, _ := s.repo.GetByUsername(ctx, username)
	if existing != nil {
		return nil, errors.ErrUserExists
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("failed to hash password", zap.Error(err))
		return nil, errors.ErrInternal
	}

	user := &model.User{
		Username:     username,
		PasswordHash: string(hashedPassword),
		Email:        email,
		Balance:      initialBalance,
		Status:       1,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		logger.Error("failed to create user", zap.Error(err))
		return nil, err
	}

	logger.Info("user created",
		logger.I64("user_id", user.ID),
		logger.S("username", username),
	)

	return user, nil
}

// Authenticate 用户认证
func (s *UserService) Authenticate(ctx context.Context, username, password string) (*model.User, error) {
	user, err := s.repo.GetByUsername(ctx, username)
	if err != nil {
		if err == errors.ErrUserNotFound {
			return nil, errors.ErrInvalidPassword
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, errors.ErrInvalidPassword
	}

	return user, nil
}

// GetUser 获取用户信息
func (s *UserService) GetUser(ctx context.Context, userID int64) (*model.User, error) {
	return s.repo.GetByID(ctx, userID)
}

// GetBalance 获取余额
func (s *UserService) GetBalance(ctx context.Context, userID int64) (available, frozen, total float64, err error) {
	available, frozen, err = s.repo.GetBalance(ctx, userID)
	if err != nil {
		return 0, 0, 0, err
	}
	total = available + frozen
	return
}

// FreezeAmount 冻结余额
func (s *UserService) FreezeAmount(ctx context.Context, userID int64, amount float64) error {
	lockKey := fmt.Sprintf("lock:balance:freeze:%d", userID)
	locked, err := s.redis.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
	if err != nil || !locked {
		return errors.New(errors.CodeConflict, "concurrent request")
	}
	defer s.redis.Del(ctx, lockKey)

	return s.repo.FreezeAmount(ctx, userID, amount)
}

// UnfreezeAmount 解冻余额
func (s *UserService) UnfreezeAmount(ctx context.Context, userID int64, amount float64) error {
	lockKey := fmt.Sprintf("lock:balance:unfreeze:%d", userID)
	locked, err := s.redis.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
	if err != nil || !locked {
		return errors.New(errors.CodeConflict, "concurrent request")
	}
	defer s.redis.Del(ctx, lockKey)

	return s.repo.UnfreezeAmount(ctx, userID, amount)
}

// DeductFrozenBalance 扣减冻结余额
func (s *UserService) DeductFrozenBalance(ctx context.Context, userID int64, amount float64) error {
	lockKey := fmt.Sprintf("lock:balance:deduct:%d", userID)
	locked, err := s.redis.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
	if err != nil || !locked {
		return errors.New(errors.CodeConflict, "concurrent request")
	}
	defer s.redis.Del(ctx, lockKey)

	return s.repo.DeductFrozenBalance(ctx, userID, amount)
}

// AddBalance 增加余额
func (s *UserService) AddBalance(ctx context.Context, userID int64, amount float64) error {
	lockKey := fmt.Sprintf("lock:balance:add:%d", userID)
	locked, err := s.redis.SetNX(ctx, lockKey, "1", 10*time.Second).Result()
	if err != nil || !locked {
		return errors.New(errors.CodeConflict, "concurrent request")
	}
	defer s.redis.Del(ctx, lockKey)

	return s.repo.AddBalance(ctx, userID, amount)
}

// GetUserRoles 获取用户角色
func (s *UserService) GetUserRoles(ctx context.Context, userID int64) ([]string, error) {
	return s.repo.GetUserRoles(ctx, userID)
}
