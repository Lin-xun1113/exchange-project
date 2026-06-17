// Package server provides gRPC service implementation
package server

import (
	"context"
	"strings"
	"time"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/internal/matching/client"
	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// MatchingServer matching service gRPC server implementation
type MatchingServer struct {
	matchingpb.UnimplementedMatchingServiceServer
	matcher       *engine.Matcher
	orderClient   *client.OrderClient
	tradeRepo     *repository.TradeRepository
}

// NewMatchingServer creates matching service gRPC server
func NewMatchingServer(matcher *engine.Matcher, orderClient *client.OrderClient, tradeRepo *repository.TradeRepository) *MatchingServer {
	return &MatchingServer{
		matcher:     matcher,
		orderClient: orderClient,
		tradeRepo:   tradeRepo,
	}
}

// SubmitOrder submits order for matching
func (s *MatchingServer) SubmitOrder(ctx context.Context, req *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error) {
	price, err := decimal.NewFromString(req.Price)
	if err != nil {
		logger.WithContext(ctx).Error("invalid price", zap.String("price", req.Price))
		return nil, err
	}

	quantity, err := decimal.NewFromString(req.Quantity)
	if err != nil {
		logger.WithContext(ctx).Error("invalid quantity", zap.String("quantity", req.Quantity))
		return nil, err
	}

	var side model.OrderSide
	switch req.Side {
	case matchingpb.OrderSide_ORDER_SIDE_BUY:
		side = model.OrderSideBuy
	case matchingpb.OrderSide_ORDER_SIDE_SELL:
		side = model.OrderSideSell
	default:
		side = model.OrderSideBuy
	}

	result, err := s.matcher.SubmitOrder(ctx, req.OrderId, req.UserId, req.Symbol, side, price, quantity)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") || err == context.DeadlineExceeded {
			return nil, status.Error(codes.DeadlineExceeded, err.Error())
		}
		logger.WithContext(ctx).Error("SubmitOrder failed",
			logger.S("order_id", req.OrderId),
			logger.I64("user_id", req.UserId),
			logger.S("symbol", req.Symbol),
			zap.Error(err),
		)
		return nil, err
	}

	// Update order status in Order Service
	if s.orderClient != nil {
		go s.updateOrderStatusAfterMatching(ctx, req.OrderId, result)
	}

	trades := make([]*matchingpb.Trade, len(result.Trades))
	for i, trade := range result.Trades {
		trades[i] = &matchingpb.Trade{
			TradeId:     trade.TradeID,
			BuyOrderId:  trade.BuyOrderID,
			SellOrderId: trade.SellOrderID,
			Symbol:      trade.Symbol,
			Price:       decimal.NewFromFloat(trade.Price.InexactFloat64()).String(),
			Quantity:    decimal.NewFromFloat(trade.Quantity.InexactFloat64()).String(),
			BuyUserId:   trade.BuyUserID,
			SellUserId:  trade.SellUserID,
			CreatedAt:   timestamppb.New(time.Unix(0, trade.CreatedAt)),
		}
	}

	return &matchingpb.SubmitOrderResponse{
		Success:           true,
		Trades:            trades,
		RemainingQuantity: result.Remaining.String(),
	}, nil
}

// GetOrderBook gets order book snapshot
func (s *MatchingServer) GetOrderBook(ctx context.Context, req *matchingpb.GetOrderBookRequest) (*matchingpb.GetOrderBookResponse, error) {
	depth := int(req.Depth)
	if depth <= 0 {
		depth = 10
	}

	bids, asks := s.matcher.GetOrderBook(req.Symbol, depth)

	protoBids := make([]*matchingpb.OrderBookEntry, len(bids))
	for i, bid := range bids {
		protoBids[i] = &matchingpb.OrderBookEntry{
			Price:      bid.Price.String(),
			Quantity:   bid.RemainingQty.String(),
			OrderCount: 1,
		}
	}

	protoAsks := make([]*matchingpb.OrderBookEntry, len(asks))
	for i, ask := range asks {
		protoAsks[i] = &matchingpb.OrderBookEntry{
			Price:      ask.Price.String(),
			Quantity:   ask.RemainingQty.String(),
			OrderCount: 1,
		}
	}

	return &matchingpb.GetOrderBookResponse{
		OrderBook: &matchingpb.OrderBook{
			Symbol:    req.Symbol,
			Bids:      protoBids,
			Asks:      protoAsks,
			Timestamp: timestamppb.Now(),
		},
	}, nil
}

// GetTrades gets trade history
func (s *MatchingServer) GetTrades(ctx context.Context, req *matchingpb.GetTradesRequest) (*matchingpb.GetTradesResponse, error) {
	if s.tradeRepo == nil {
		logger.WithContext(ctx).Error("trade repository not initialized")
		return &matchingpb.GetTradesResponse{
			Trades:      []*matchingpb.Trade{},
			NextCursor: "",
			HasMore:    false,
		}, nil
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	offset := 0
	if req.Cursor != "" {
		// Parse cursor for pagination (simple offset calculation)
		if _, err := parseCursor(req.Cursor); err == nil {
			offset = int(req.Limit)
		}
	}

	var trades []*model.Trade
	var err error

	if req.Symbol != "" {
		trades, err = s.tradeRepo.ListBySymbol(ctx, req.Symbol, limit+1, offset)
	} else if req.UserId > 0 {
		trades, err = s.tradeRepo.ListByUser(ctx, req.UserId, "", limit+1, offset)
	} else if req.OrderId != "" {
		trades, err = s.tradeRepo.ListByOrderID(ctx, req.OrderId, limit+1, offset)
	} else {
		trades, err = s.tradeRepo.GetRecentTrades(ctx, "", limit+1)
	}

	if err != nil {
		logger.WithContext(ctx).Error("failed to get trades",
			logger.S("symbol", req.Symbol),
			logger.I64("user_id", req.UserId),
			logger.S("order_id", req.OrderId),
			logger.Err(err),
		)
		return nil, err
	}

	hasMore := len(trades) > limit
	if hasMore {
		trades = trades[:limit]
	}

	protoTrades := make([]*matchingpb.Trade, len(trades))
	for i, trade := range trades {
		protoTrades[i] = &matchingpb.Trade{
			TradeId:     trade.TradeID,
			BuyOrderId:  trade.BuyOrderID,
			SellOrderId: trade.SellOrderID,
			Symbol:      trade.Symbol,
			Price:       decimal.NewFromFloat(trade.Price).String(),
			Quantity:    decimal.NewFromFloat(trade.Quantity).String(),
			BuyUserId:   trade.BuyUserID,
			SellUserId:  trade.SellUserID,
			CreatedAt:   timestamppb.New(trade.CreatedAt),
		}
	}

	var nextCursor string
	if hasMore {
		nextCursor = encodeCursor(offset + limit)
	}

	return &matchingpb.GetTradesResponse{
		Trades:      protoTrades,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

// parseCursor parses a cursor string (base64 encoded offset)
func parseCursor(cursor string) (int, error) {
	data, err := decodeBase64(cursor)
	if err != nil {
		return 0, err
	}
	var offset int
	for _, b := range data {
		offset = offset*256 + int(b)
	}
	return offset, nil
}

// encodeCursor encodes an offset as a cursor string
func encodeCursor(offset int) string {
	data := []byte{}
	for offset > 0 {
		data = append([]byte{byte(offset % 256)}, data...)
		offset /= 256
	}
	return encodeBase64(data)
}

// decodeBase64 decodes a base64 string
func decodeBase64(s string) ([]byte, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	decoder := make([]byte, 256)
	for i := range decoder {
		decoder[i] = 0xFF
	}
	for i := 0; i < len(alphabet); i++ {
		decoder[alphabet[i]] = byte(i)
	}

	result := []byte{}
	padding := 0
	for i := 0; i < len(s); i += 4 {
		var val int
		for j := 0; j < 4 && i+j < len(s); j++ {
			c := s[i+j]
			if c == '=' {
				padding++
			} else {
				val = val<<6 | int(decoder[c])
			}
		}
		val <<= (6 * padding)
		result = append(result, byte(val>>16), byte(val>>8), byte(val))
	}
	return result[:len(result)-padding], nil
}

// encodeBase64 encodes bytes to base64
func encodeBase64(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	result := ""
	for i := 0; i < len(data); i += 3 {
		var val int
		var padding int
		for j := 0; j < 3 && i+j < len(data); j++ {
			val = val<<8 | int(data[i+j])
		}
		padding = 3 - (len(data)-i)%3
		if padding == 3 {
			padding = 0
		}
		val <<= (padding * 8)
		for j := 0; j < 4; j++ {
			if j < 4-padding {
				result += string(alphabet[(val>>18)&0x3F])
				val <<= 6
			} else {
				result += "="
			}
		}
	}
	return result
}

// updateOrderStatusAfterMatching updates order status after matching
func (s *MatchingServer) updateOrderStatusAfterMatching(ctx context.Context, orderID string, result *engine.MatchResult) {
	if result.Remaining.IsZero() {
		// Fully filled
		if err := s.orderClient.UpdateOrderStatus(ctx, orderID, orderpb.OrderStatus_ORDER_STATUS_FILLED, result.Remaining.String()); err != nil {
			logger.WithContext(ctx).Error("failed to update order status to FILLED",
				logger.S("order_id", orderID),
				logger.Err(err),
			)
		}
	} else if !result.Remaining.IsZero() && len(result.Trades) > 0 {
		// Partially filled
		filledQty := decimal.Zero.Sub(result.Remaining).String()
		if err := s.orderClient.UpdateOrderStatus(ctx, orderID, orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED, filledQty); err != nil {
			logger.WithContext(ctx).Error("failed to update order status to PARTIAL_FILLED",
				logger.S("order_id", orderID),
				logger.Err(err),
			)
		}
	}
}
