// Package service 提供用户服务层实现
package service

import (
	"context"
	"errors"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/user/repository"
	"github.com/linxun2025/exchange-project/pkg/dberr"
	bizerr "github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// UserService 用户服务
type UserService struct {
	repo *repository.UserRepository
}

// NewUserService 创建用户服务
func NewUserService(repo *repository.UserRepository) *UserService {
	return &UserService{repo: repo}
}

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, username, password, email string) (*model.User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("failed to hash password", zap.Error(err))
		return nil, bizerr.ErrInternal
	}

	user := &model.User{
		Username:     username,
		PasswordHash: string(hashedPassword),
		Email:        email,
		Status:       1,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		if dberr.IsDuplicate(err) {
			return nil, bizerr.ErrUserExists
		}
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
		if errors.Is(err, bizerr.ErrUserNotFound) {
			return nil, bizerr.ErrInvalidPassword
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, bizerr.ErrInvalidPassword
	}

	return user, nil
}

// GetUser 获取用户信息
func (s *UserService) GetUser(ctx context.Context, userID int64) (*model.User, error) {
	return s.repo.GetByID(ctx, userID)
}

// GetUserRoles 获取用户角色
func (s *UserService) GetUserRoles(ctx context.Context, userID int64) ([]string, error) {
	return s.repo.GetUserRoles(ctx, userID)
}
