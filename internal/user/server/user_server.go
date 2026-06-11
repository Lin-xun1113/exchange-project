// Package server 提供 gRPC 服务端实现
package server

import (
	"context"

	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/user/service"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// UserServer 用户服务 gRPC 服务端实现
type UserServer struct {
	userpb.UnimplementedUserServiceServer
	svc *service.UserService
}

// NewUserServer 创建用户服务 gRPC 服务端
func NewUserServer(svc *service.UserService) *UserServer {
	return &UserServer{
		svc: svc,
	}
}

// GetUser 获取用户信息
func (s *UserServer) GetUser(ctx context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	user, err := s.svc.GetUser(ctx, req.UserId)
	if err != nil {
		logger.Error("GetUser failed", zap.Int64("user_id", req.UserId), zap.Error(err))
		return nil, err
	}

	return &userpb.GetUserResponse{
		User: modelToProtoUser(user),
	}, nil
}

// Login 用户登录
func (s *UserServer) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	user, err := s.svc.Authenticate(ctx, req.Username, req.Password)
	if err != nil {
		logger.Error("Login failed", zap.String("username", req.Username), zap.Error(err))
		return nil, err
	}

	// 获取用户角色
	roles, _ := s.svc.GetUserRoles(ctx, user.ID)
	role := "trader"
	if len(roles) > 0 {
		role = roles[0]
	}

	return &userpb.LoginResponse{
		UserId:    user.ID,
		Username:  user.Username,
		Role:      role,
	}, nil
}

// CreateUser 创建用户
func (s *UserServer) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	initialBalance := decimal.Zero
	if req.InitialBalance != "" {
		var err error
		initialBalance, err = decimal.NewFromString(req.InitialBalance)
		if err != nil {
			logger.Error("invalid initial_balance", zap.String("initial_balance", req.InitialBalance))
			return nil, err
		}
	}

	user, err := s.svc.CreateUser(ctx, req.Username, req.Password, req.Email, initialBalance.InexactFloat64())
	if err != nil {
		logger.Error("CreateUser failed", zap.String("username", req.Username), zap.Error(err))
		return nil, err
	}

	return &userpb.CreateUserResponse{
		User: modelToProtoUser(user),
	}, nil
}

// GetBalance 获取用户余额
func (s *UserServer) GetBalance(ctx context.Context, req *userpb.GetBalanceRequest) (*userpb.GetBalanceResponse, error) {
	available, frozen, total, err := s.svc.GetBalance(ctx, req.UserId)
	if err != nil {
		logger.Error("GetBalance failed", zap.Int64("user_id", req.UserId), zap.Error(err))
		return nil, err
	}

	return &userpb.GetBalanceResponse{
		UserId:           req.UserId,
		AvailableBalance: decimal.NewFromFloat(available).String(),
		FrozenBalance:    decimal.NewFromFloat(frozen).String(),
		TotalBalance:     decimal.NewFromFloat(total).String(),
	}, nil
}

// FreezeAmount 冻结余额
func (s *UserServer) FreezeAmount(ctx context.Context, req *userpb.FreezeAmountRequest) (*userpb.FreezeAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.Error("invalid freeze amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.FreezeAmount(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.Error("FreezeAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
		return nil, err
	}

	available, frozen, _, err := s.svc.GetBalance(ctx, req.UserId)
	if err != nil {
		return nil, err
	}

	return &userpb.FreezeAmountResponse{
		Success:             true,
		NewAvailableBalance: decimal.NewFromFloat(available).String(),
		NewFrozenBalance:    decimal.NewFromFloat(frozen).String(),
	}, nil
}

// UnfreezeAmount 解冻余额
func (s *UserServer) UnfreezeAmount(ctx context.Context, req *userpb.UnfreezeAmountRequest) (*userpb.UnfreezeAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.Error("invalid unfreeze amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.UnfreezeAmount(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.Error("UnfreezeAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
		return nil, err
	}

	available, frozen, _, err := s.svc.GetBalance(ctx, req.UserId)
	if err != nil {
		return nil, err
	}

	return &userpb.UnfreezeAmountResponse{
		Success:             true,
		NewAvailableBalance: decimal.NewFromFloat(available).String(),
		NewFrozenBalance:    decimal.NewFromFloat(frozen).String(),
	}, nil
}

// DeductAmount 扣减余额（撮合成交后调用）
func (s *UserServer) DeductAmount(ctx context.Context, req *userpb.DeductAmountRequest) (*userpb.DeductAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.Error("invalid deduct amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.DeductFrozenBalance(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.Error("DeductAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
		return nil, err
	}

	available, _, _, err := s.svc.GetBalance(ctx, req.UserId)
	if err != nil {
		return nil, err
	}

	return &userpb.DeductAmountResponse{
		Success:    true,
		NewBalance: decimal.NewFromFloat(available).String(),
	}, nil
}

// AddAmount 增加余额
func (s *UserServer) AddAmount(ctx context.Context, req *userpb.AddAmountRequest) (*userpb.AddAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.Error("invalid add amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.AddBalance(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.Error("AddAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
		return nil, err
	}

	available, _, _, err := s.svc.GetBalance(ctx, req.UserId)
	if err != nil {
		return nil, err
	}

	return &userpb.AddAmountResponse{
		Success:    true,
		NewBalance: decimal.NewFromFloat(available).String(),
	}, nil
}

// modelToProtoUser 将 model.User 转换为 proto User
func modelToProtoUser(user *model.User) *userpb.User {
	if user == nil {
		return nil
	}

	return &userpb.User{
		Id:            user.ID,
		Username:      user.Username,
		Email:         user.Email,
		Balance:       decimal.NewFromFloat(user.Balance).String(),
		FrozenBalance: decimal.NewFromFloat(user.FrozenBalance).String(),
		Status:        int32(user.Status),
		CreatedAt:     timestamppb.New(user.CreatedAt),
		UpdatedAt:     timestamppb.New(user.UpdatedAt),
	}
}
