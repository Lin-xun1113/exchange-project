// Package server provides gRPC service implementation
package server

import (
	"context"
	"errors"
	"unicode/utf8"

	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/user/ledger"
	"github.com/linxun2025/exchange-project/internal/user/service"
	bizerr "github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/money"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// UserServer user service gRPC server implementation
type UserServer struct {
	userpb.UnimplementedUserServiceServer
	svc    *service.UserService
	ledger *ledger.Ledger
}

// NewUserServer creates user service gRPC server
func NewUserServer(svc *service.UserService, l *ledger.Ledger) *UserServer {
	return &UserServer{svc: svc, ledger: l}
}

// GetUser gets user info
func (s *UserServer) GetUser(ctx context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	if req.GetUserId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id must be positive")
	}
	user, err := s.svc.GetUser(ctx, req.GetUserId())
	if errors.Is(err, bizerr.ErrUserNotFound) {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	if err != nil {
		return nil, rpcError(ctx, "GetUser", err)
	}

	return &userpb.GetUserResponse{
		User: modelToProtoUser(user),
	}, nil
}

// Login user login
func (s *UserServer) Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	if req.GetUsername() == "" || req.GetPassword() == "" || len(req.GetPassword()) > 72 {
		return nil, status.Error(codes.Unauthenticated, "invalid username or password")
	}
	user, err := s.svc.Authenticate(ctx, req.GetUsername(), req.GetPassword())
	if errors.Is(err, bizerr.ErrInvalidPassword) {
		return nil, status.Error(codes.Unauthenticated, "invalid username or password")
	}
	if err != nil {
		return nil, rpcError(ctx, "Login", err)
	}
	roles, err := s.svc.GetUserRoles(ctx, user.ID)
	if err != nil {
		return nil, rpcError(ctx, "Login roles", err)
	}
	role := "trader"
	if len(roles) > 0 {
		role = roles[0]
	}

	return &userpb.LoginResponse{
		UserId:   user.ID,
		Username: user.Username,
		Role:     role,
	}, nil
}

// CreateUser creates user
func (s *UserServer) CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	username, password, email := req.GetUsername(), req.GetPassword(), req.GetEmail()
	if username == "" || utf8.RuneCountInString(username) > 64 || password == "" || len(password) > 72 || utf8.RuneCountInString(email) > 128 {
		return nil, status.Error(codes.InvalidArgument, "invalid username, password or email length")
	}
	user, err := s.svc.CreateUser(ctx, username, password, email)
	if errors.Is(err, bizerr.ErrUserExists) {
		return nil, status.Error(codes.AlreadyExists, "user already exists")
	}
	if err != nil {
		return nil, rpcError(ctx, "CreateUser", err)
	}

	return &userpb.CreateUserResponse{
		User: modelToProtoUser(user),
	}, nil
}

func (s *UserServer) GetBalances(ctx context.Context, req *userpb.GetBalancesRequest) (*userpb.GetBalancesResponse, error) {
	if req.GetUserId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "user_id must be positive")
	}
	rows, err := s.ledger.Balances(ctx, req.GetUserId())
	if err != nil {
		return nil, rpcError(ctx, "GetBalances", err)
	}
	balances := make([]*userpb.Balance, 0, len(rows))
	for _, row := range rows {
		balances = append(balances, balanceToProto(row))
	}
	return &userpb.GetBalancesResponse{Balances: balances}, nil
}

func (s *UserServer) Deposit(ctx context.Context, req *userpb.DepositRequest) (*userpb.DepositResponse, error) {
	if err := validatePosting(req.GetUserId(), req.GetAsset(), req.GetRefId()); err != nil {
		return nil, err
	}
	amount, err := money.ParsePositive(req.GetAmount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive with at most 8 fractional digits and fit DECIMAL(36,18)")
	}
	balance, err := s.ledger.Deposit(ctx, req.GetUserId(), req.GetAsset(), amount, req.GetRefId())
	if err != nil {
		if errors.Is(err, ledger.ErrDepositConflict) {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, rpcError(ctx, "Deposit", err)
	}
	return &userpb.DepositResponse{Balance: balanceToProto(balance)}, nil
}

func (s *UserServer) Freeze(ctx context.Context, req *userpb.FreezeRequest) (*userpb.FreezeResponse, error) {
	if err := validatePosting(req.GetUserId(), req.GetAsset(), req.GetOrderId()); err != nil {
		return nil, err
	}
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid freeze amount")
	}
	err = s.ledger.Freeze(ctx, req.GetUserId(), req.GetAsset(), amount, req.GetOrderId())
	if errors.Is(err, ledger.ErrInsufficientBalance) {
		return &userpb.FreezeResponse{Reason: "insufficient_balance"}, nil
	}
	if err != nil {
		return nil, rpcError(ctx, "Freeze", err)
	}
	return &userpb.FreezeResponse{Ok: true}, nil
}

func (s *UserServer) Unfreeze(ctx context.Context, req *userpb.UnfreezeRequest) (*userpb.UnfreezeResponse, error) {
	if err := validatePosting(req.GetUserId(), req.GetAsset(), req.GetOrderId()); err != nil {
		return nil, err
	}
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid unfreeze amount")
	}
	if err := s.ledger.Unfreeze(ctx, req.GetUserId(), req.GetAsset(), amount, req.GetOrderId()); err != nil {
		return nil, rpcError(ctx, "Unfreeze", err)
	}
	return &userpb.UnfreezeResponse{}, nil
}

func (s *UserServer) SettleTrade(ctx context.Context, req *userpb.SettleTradeRequest) (*userpb.SettleTradeResponse, error) {
	if err := validatePosting(req.GetBuyUserId(), req.GetBaseAsset(), req.GetTradeId()); err != nil {
		return nil, err
	}
	if req.GetSellUserId() <= 0 || !validAsset(req.GetQuoteAsset()) || req.GetBaseAsset() == req.GetQuoteAsset() {
		return nil, status.Error(codes.InvalidArgument, "settlement requires positive user IDs and distinct valid assets")
	}
	price, err := money.ParsePositive(req.GetPrice())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid price")
	}
	qty, err := money.ParsePositive(req.GetQuantity())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid quantity")
	}
	buyPrice, err := money.ParsePositive(req.GetBuyOrderPrice())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid buy_order_price")
	}
	if err := s.ledger.SettleTrade(ctx, ledger.Settlement{
		TradeID: req.GetTradeId(), Base: req.GetBaseAsset(), Quote: req.GetQuoteAsset(),
		BuyUserID: req.GetBuyUserId(), SellUserID: req.GetSellUserId(),
		Price: price, Qty: qty, BuyOrderPrice: buyPrice,
	}); err != nil {
		return nil, rpcError(ctx, "SettleTrade", err)
	}
	return &userpb.SettleTradeResponse{}, nil
}

func validatePosting(userID int64, asset, refID string) error {
	if userID <= 0 {
		return status.Error(codes.InvalidArgument, "user_id must be positive")
	}
	if !validAsset(asset) {
		return status.Error(codes.InvalidArgument, "asset must contain 2–16 uppercase ASCII letters or digits")
	}
	if refID == "" || utf8.RuneCountInString(refID) > 96 {
		return status.Error(codes.InvalidArgument, "reference ID must contain 1–96 characters")
	}
	return nil
}

func validAsset(asset string) bool {
	if len(asset) < 2 || len(asset) > 16 {
		return false
	}
	for i := range len(asset) {
		c := asset[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func rpcError(ctx context.Context, method string, err error) error {
	switch {
	case errors.Is(err, money.ErrInvalidAmount):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return status.Error(codes.FailedPrecondition, ledger.ErrInsufficientBalance.Error())
	case errors.Is(err, ledger.ErrBalanceCapacity):
		return status.Error(codes.FailedPrecondition, ledger.ErrBalanceCapacity.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		logger.WithContext(ctx).Error("user RPC failed", zap.String("method", method), zap.Error(err))
		return status.Error(codes.Unavailable, "user service unavailable")
	}
}

func balanceToProto(b model.Balance) *userpb.Balance {
	return &userpb.Balance{Asset: b.Asset, Available: b.Available.String(), Frozen: b.Frozen.String()}
}

// modelToProtoUser converts model.User to proto User
func modelToProtoUser(user *model.User) *userpb.User {
	if user == nil {
		return nil
	}

	return &userpb.User{
		Id:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Status:    int32(user.Status),
		CreatedAt: timestamppb.New(user.CreatedAt),
	}
}
