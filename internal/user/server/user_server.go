// Package server provides gRPC service implementation
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

// UserServer user service gRPC server implementation
type UserServer struct {
	userpb.UnimplementedUserServiceServer
	svc *service.UserService
}

// NewUserServer creates user service gRPC server
func NewUserServer(svc *service.UserService) *UserServer {
	return &UserServer{
		svc: svc,
	}
}

// GetUser gets user info
func (s *UserServer) GetUser(ctx context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	user, err := s.svc.GetUser(ctx, req.UserId)
	if err != nil {
		logger.WithContext(ctx).Error("GetUser failed", zap.Int64("user_id", req.UserId), zap.Error(err))
		return nil, err
	}

	return &userpb.GetUserResponse{
		User: modelToProtoUser(user),
	}, nil
}

// Login user login
func (s *UserServer) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	user, err := s.svc.Authenticate(ctx, req.Username, req.Password)
	if err != nil {
		logger.WithContext(ctx).Error("Login failed", zap.String("username", req.Username), zap.Error(err))
		return nil, err
	}

	// Get user roles
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

// CreateUser creates user
func (s *UserServer) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	initialBalance := decimal.Zero
	if req.InitialBalance != "" {
		var err error
		initialBalance, err = decimal.NewFromString(req.InitialBalance)
		if err != nil {
			logger.WithContext(ctx).Error("invalid initial_balance", zap.String("initial_balance", req.InitialBalance))
			return nil, err
		}
	}

	user, err := s.svc.CreateUser(ctx, req.Username, req.Password, req.Email, initialBalance.InexactFloat64())
	if err != nil {
		logger.WithContext(ctx).Error("CreateUser failed", zap.String("username", req.Username), zap.Error(err))
		return nil, err
	}

	return &userpb.CreateUserResponse{
		User: modelToProtoUser(user),
	}, nil
}

// GetBalance gets user balance
func (s *UserServer) GetBalance(ctx context.Context, req *userpb.GetBalanceRequest) (*userpb.GetBalanceResponse, error) {
	available, frozen, total, err := s.svc.GetBalance(ctx, req.UserId)
	if err != nil {
		logger.WithContext(ctx).Error("GetBalance failed", zap.Int64("user_id", req.UserId), zap.Error(err))
		return nil, err
	}

	return &userpb.GetBalanceResponse{
		UserId:            req.UserId,
		AvailableBalance:  decimal.NewFromFloat(available).String(),
		FrozenBalance:     decimal.NewFromFloat(frozen).String(),
		TotalBalance:      decimal.NewFromFloat(total).String(),
	}, nil
}

// FreezeAmount freezes amount
func (s *UserServer) FreezeAmount(ctx context.Context, req *userpb.FreezeAmountRequest) (*userpb.FreezeAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.WithContext(ctx).Error("invalid freeze amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.FreezeAmount(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.WithContext(ctx).Error("FreezeAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
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

// UnfreezeAmount unfreezes amount
func (s *UserServer) UnfreezeAmount(ctx context.Context, req *userpb.UnfreezeAmountRequest) (*userpb.UnfreezeAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.WithContext(ctx).Error("invalid unfreeze amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.UnfreezeAmount(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.WithContext(ctx).Error("UnfreezeAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
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

// DeductAmount deducts amount (called after matching)
func (s *UserServer) DeductAmount(ctx context.Context, req *userpb.DeductAmountRequest) (*userpb.DeductAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.WithContext(ctx).Error("invalid deduct amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.DeductFrozenBalance(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.WithContext(ctx).Error("DeductAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
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

// AddAmount adds amount
func (s *UserServer) AddAmount(ctx context.Context, req *userpb.AddAmountRequest) (*userpb.AddAmountResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		logger.WithContext(ctx).Error("invalid add amount", zap.String("amount", req.Amount))
		return nil, err
	}

	if err := s.svc.AddBalance(ctx, req.UserId, amount.InexactFloat64()); err != nil {
		logger.WithContext(ctx).Error("AddAmount failed", zap.Int64("user_id", req.UserId), zap.String("amount", req.Amount), zap.Error(err))
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

// modelToProtoUser converts model.User to proto User
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
