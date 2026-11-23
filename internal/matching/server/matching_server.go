// Package server exposes the matching engine over gRPC.
package server

import (
	"context"
	"errors"
	"math"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/pkg/money"
	"github.com/linxun2025/exchange-project/pkg/symbol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type MatchingServer struct {
	matchingpb.UnimplementedMatchingServiceServer
	matcher *engine.Matcher
	allowed func(symbol.Pair) bool
}

func NewMatchingServer(m *engine.Matcher, symbols []string) *MatchingServer {
	return &MatchingServer{matcher: m, allowed: symbol.Allowed(symbols)}
}

func (s *MatchingServer) parseSymbol(raw string) (string, error) {
	pair, err := symbol.Parse(raw)
	if err != nil || !s.allowed(pair) {
		return "", status.Error(codes.InvalidArgument, "unsupported symbol")
	}
	return pair.String(), nil
}

func (s *MatchingServer) SubmitOrder(ctx context.Context, req *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error) {
	pair, err := s.parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	o := book.Order{ID: req.GetOrderId(), UserID: req.GetUserId()}
	switch req.GetSide() {
	case matchingpb.OrderSide_ORDER_SIDE_BUY:
		o.Side = book.Buy
	case matchingpb.OrderSide_ORDER_SIDE_SELL:
		o.Side = book.Sell
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid side")
	}
	switch req.GetType() {
	case matchingpb.OrderType_ORDER_TYPE_LIMIT:
		o.Kind = book.Limit
	case matchingpb.OrderType_ORDER_TYPE_IOC:
		o.Kind = book.IOC
	case matchingpb.OrderType_ORDER_TYPE_FOK:
		o.Kind = book.FOK
	case matchingpb.OrderType_ORDER_TYPE_MARKET:
		o.Kind = book.Market
		if o.Side == book.Buy {
			return nil, status.Error(codes.InvalidArgument, "market buy not supported")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid order type")
	}
	if o.Kind == book.Market {
		if req.GetPrice() != "" && req.GetPrice() != "0" {
			return nil, status.Error(codes.InvalidArgument, "market order price must be zero or empty")
		}
	} else {
		o.Price, err = positiveTicks(req.GetPrice())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid price")
		}
	}
	o.Qty, err = positiveTicks(req.GetQuantity())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid quantity")
	}
	r, err := s.matcher.Submit(ctx, pair, o)
	if err != nil {
		return nil, rpcError(err)
	}
	reply := &matchingpb.SubmitOrderResponse{
		OrderId: r.OrderID, FilledQuantity: ticksString(r.Filled), RemainingQuantity: ticksString(r.Remaining),
		Resting: r.Resting, RejectReason: matchingpb.RejectReason(r.Reject),
	}
	if len(r.Trades) > 0 {
		reply.Trades = make([]*matchingpb.Trade, 0, len(r.Trades))
		for _, t := range r.Trades {
			reply.Trades = append(reply.Trades, &matchingpb.Trade{
				TradeId: t.ID, Symbol: pair, BuyOrderId: t.BuyOrderID, SellOrderId: t.SellOrderID,
				BuyUserId: t.BuyUserID, SellUserId: t.SellUserID, Price: ticksString(t.Price), Quantity: ticksString(t.Qty),
				TakerSide: matchingpb.OrderSide(t.TakerSide), Lsn: t.LSN, CreatedAt: timestamppb.New(t.At),
			})
		}
	}
	return reply, nil
}

func (s *MatchingServer) CancelOrder(ctx context.Context, req *matchingpb.CancelOrderRequest) (*matchingpb.CancelOrderResponse, error) {
	pair, err := s.parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	r, err := s.matcher.Cancel(ctx, pair, req.GetOrderId())
	if err != nil {
		return nil, rpcError(err)
	}
	return &matchingpb.CancelOrderResponse{Found: r.Found, RemainingQuantity: ticksString(r.Remaining)}, nil
}

func (s *MatchingServer) GetOrderBook(ctx context.Context, req *matchingpb.GetOrderBookRequest) (*matchingpb.GetOrderBookResponse, error) {
	pair, err := s.parseSymbol(req.GetSymbol())
	if err != nil {
		return nil, err
	}
	depth := req.GetDepth()
	if depth == 0 {
		depth = 20
	}
	if depth < 1 || depth > 200 {
		return nil, status.Error(codes.InvalidArgument, "depth must be between 1 and 200")
	}
	res, err := s.matcher.Depth(ctx, pair, int(depth))
	if err != nil {
		return nil, rpcError(err)
	}
	bids, err := priceLevels(res.Bids)
	if err != nil {
		return nil, err
	}
	asks, err := priceLevels(res.Asks)
	if err != nil {
		return nil, err
	}
	return &matchingpb.GetOrderBookResponse{Symbol: pair, Bids: bids, Asks: asks, Lsn: res.LSN}, nil
}

func priceLevels(levels []book.Level) ([]*matchingpb.PriceLevel, error) {
	out := make([]*matchingpb.PriceLevel, len(levels))
	for i, l := range levels {
		if l.Orders > math.MaxInt32 {
			return nil, status.Error(codes.Internal, "order count exceeds response capacity")
		}
		out[i] = &matchingpb.PriceLevel{Price: ticksString(l.Price), Quantity: ticksString(l.Qty), OrderCount: int32(l.Orders)}
	}
	return out, nil
}

func ticksString(n int64) string { return money.FromTicks(n).String() }

func positiveTicks(s string) (int64, error) {
	d, err := money.ParsePositive(s)
	if err != nil {
		return 0, err
	}
	return money.ToTicks(d)
}

func rpcError(err error) error {
	switch {
	case errors.Is(err, engine.ErrUnknownSymbol), errors.Is(err, engine.ErrInvalidOrder):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, engine.ErrClosed):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	default:
		return status.Error(codes.Internal, "matching request failed")
	}
}
