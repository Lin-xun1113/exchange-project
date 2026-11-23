package service

import (
	"context"
	"encoding/base64"
	"strconv"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"github.com/linxun2025/exchange-project/pkg/symbol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *OrderService) GetOrder(ctx context.Context, userID int64, orderID string) (model.Order, error) {
	if err := validateOrderRef(userID, orderID); err != nil {
		return model.Order{}, err
	}
	o, err := repository.GetOrder(s.db.WithContext(ctx), userID, orderID)
	if err != nil {
		return model.Order{}, orderError(ctx, "get order", err)
	}
	return o, nil
}

func (s *OrderService) ListOrders(ctx context.Context, userID int64, market string, filter model.OrderStatus, pageSize int32, token string) ([]model.Order, string, error) {
	switch filter {
	case "", model.OrderStatusPending, model.OrderStatusOpen, model.OrderStatusPartialFilled,
		model.OrderStatusFilled, model.OrderStatusCancelled, model.OrderStatusRejected:
	default:
		return nil, "", status.Error(codes.InvalidArgument, "invalid order status")
	}
	market, size, beforeID, err := s.validateList(userID, market, pageSize, token)
	if err != nil {
		return nil, "", err
	}
	rows, err := repository.ListOrders(s.db.WithContext(ctx), userID, market, filter, beforeID, size+1)
	if err != nil {
		return nil, "", databaseError(ctx, "list orders", err)
	}
	rows, next := pageResult(rows, size, func(o *model.Order) int64 { return o.ID })
	return rows, next, nil
}

func (s *OrderService) ListTrades(ctx context.Context, userID int64, market string, pageSize int32, token string) ([]model.Trade, string, error) {
	market, size, beforeID, err := s.validateList(userID, market, pageSize, token)
	if err != nil {
		return nil, "", err
	}
	rows, err := repository.ListTrades(s.db.WithContext(ctx), userID, market, beforeID, size+1)
	if err != nil {
		return nil, "", databaseError(ctx, "list trades", err)
	}
	rows, next := pageResult(rows, size, func(t *model.Trade) int64 { return t.ID })
	return rows, next, nil
}

func (s *OrderService) validateList(userID int64, market string, pageSize int32, token string) (string, int, int64, error) {
	if userID <= 0 {
		return "", 0, 0, status.Error(codes.InvalidArgument, "positive user ID required")
	}
	if market != "" {
		pair, err := symbol.Parse(market)
		if err != nil || !s.allowed(pair) {
			return "", 0, 0, status.Error(codes.InvalidArgument, "unsupported symbol")
		}
		market = pair.String()
	}
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize < 0 || pageSize > 200 {
		return "", 0, 0, status.Error(codes.InvalidArgument, "page size must be between 1 and 200")
	}
	before, err := decodePageToken(token)
	return market, int(pageSize), before, err
}

func decodePageToken(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	invalid := status.Error(codes.InvalidArgument, "invalid page token")
	if len(token) > 28 { // Base64-encoded length of the largest int64 ID.
		return 0, invalid
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(token)
	if err != nil {
		return 0, invalid
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalid
	}
	return id, nil
}

func pageResult[T any](rows []T, size int, rowID func(*T) int64) ([]T, string) {
	if len(rows) <= size {
		return rows, ""
	}
	id := strconv.FormatInt(rowID(&rows[size-1]), 10)
	return rows[:size], base64.StdEncoding.EncodeToString([]byte(id))
}
