// Package server exposes the order gRPC service.
package server

import (
	"context"
	"fmt"

	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/service"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type OrderServer struct {
	orderpb.UnimplementedOrderServiceServer
	svc *service.OrderService
}

func NewOrderServer(svc *service.OrderService) *OrderServer {
	return &OrderServer{svc: svc}
}

func (s *OrderServer) CreateOrder(ctx context.Context, req *orderpb.CreateOrderRequest) (*orderpb.CreateOrderResponse, error) {
	side, valid := sides.toModel[req.GetSide()]
	if !valid {
		return nil, status.Error(codes.InvalidArgument, "invalid side")
	}
	kind, valid := orderTypes.toModel[req.GetType()]
	if !valid {
		return nil, status.Error(codes.InvalidArgument, "invalid order type")
	}
	o, replayed, err := s.svc.CreateOrder(ctx, service.CreateOrderRequest{
		UserID: req.UserId, Symbol: req.Symbol, Side: side, Type: kind,
		Price: req.Price, Quantity: req.Quantity, IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	order, err := modelToProtoOrder(ctx, o)
	if err != nil {
		return nil, err
	}
	return &orderpb.CreateOrderResponse{Order: order, Replayed: replayed}, nil
}

func (s *OrderServer) CancelOrder(ctx context.Context, req *orderpb.CancelOrderRequest) (*orderpb.CancelOrderResponse, error) {
	o, err := s.svc.CancelOrder(ctx, req.GetUserId(), req.GetOrderId())
	if err != nil {
		return nil, err
	}
	order, err := modelToProtoOrder(ctx, o)
	if err != nil {
		return nil, err
	}
	return &orderpb.CancelOrderResponse{Order: order}, nil
}

func (s *OrderServer) GetOrder(ctx context.Context, req *orderpb.GetOrderRequest) (*orderpb.GetOrderResponse, error) {
	o, err := s.svc.GetOrder(ctx, req.GetUserId(), req.GetOrderId())
	if err != nil {
		return nil, err
	}
	order, err := modelToProtoOrder(ctx, o)
	if err != nil {
		return nil, err
	}
	return &orderpb.GetOrderResponse{Order: order}, nil
}

func (s *OrderServer) ListOrders(ctx context.Context, req *orderpb.ListOrdersRequest) (*orderpb.ListOrdersResponse, error) {
	var filter model.OrderStatus
	if req.GetStatus() != orderpb.OrderStatus_ORDER_STATUS_UNSPECIFIED {
		var ok bool
		filter, ok = orderStatuses.toModel[req.Status]
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "invalid order status")
		}
	}
	orders, next, err := s.svc.ListOrders(ctx, req.GetUserId(), req.GetSymbol(), filter, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := make([]*orderpb.Order, len(orders))
	for i, o := range orders {
		out[i], err = modelToProtoOrder(ctx, o)
		if err != nil {
			return nil, err
		}
	}
	return &orderpb.ListOrdersResponse{Orders: out, NextPageToken: next}, nil
}

func (s *OrderServer) ListTrades(ctx context.Context, req *orderpb.ListTradesRequest) (*orderpb.ListTradesResponse, error) {
	trades, next, err := s.svc.ListTrades(ctx, req.GetUserId(), req.GetSymbol(), req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := make([]*orderpb.Trade, len(trades))
	for i, trade := range trades {
		side, orderID := model.OrderSideSell, trade.SellOrderID
		if req.UserId == trade.BuyUserID {
			side, orderID = model.OrderSideBuy, trade.BuyOrderID
		}
		out[i] = &orderpb.Trade{
			TradeId: trade.TradeID, Symbol: trade.Symbol, OrderId: orderID, Side: sides.toProto[side],
			Price: trade.Price.String(), Quantity: trade.Quantity.String(), Maker: trade.TakerSide != side,
			CreatedAt: timestamppb.New(trade.CreatedAt),
		}
	}
	return &orderpb.ListTradesResponse{Trades: out, NextPageToken: next}, nil
}

func modelToProtoOrder(ctx context.Context, o model.Order) (*orderpb.Order, error) {
	side, sideOK := sides.toProto[o.Side]
	kind, kindOK := orderTypes.toProto[o.Type]
	state, stateOK := orderStatuses.toProto[o.Status]
	if !sideOK || !kindOK || !stateOK {
		logger.WithContext(ctx).Error("invalid persisted order enum", logger.S("order_id", o.OrderID),
			logger.Err(fmt.Errorf("side=%q type=%q status=%q", o.Side, o.Type, o.Status)))
		return nil, status.Error(codes.Internal, "invalid persisted order state")
	}
	return &orderpb.Order{
		OrderId: o.OrderID, UserId: o.UserID, Symbol: o.Symbol, Side: side, Type: kind,
		Price: o.Price.String(), Quantity: o.Quantity.String(), FilledQuantity: o.FilledQuantity.String(), Status: state,
		RejectReason: o.RejectReason, CancelRequested: o.CancelRequested,
		CreatedAt: timestamppb.New(o.CreatedAt), UpdatedAt: timestamppb.New(o.UpdatedAt),
	}, nil
}

type enumMap[P comparable, M comparable] struct {
	toModel map[P]M
	toProto map[M]P
}

func enumMapping[P comparable, M comparable](protoToModel map[P]M) enumMap[P, M] {
	modelToProto := make(map[M]P, len(protoToModel))
	for p, m := range protoToModel {
		modelToProto[m] = p
	}
	return enumMap[P, M]{toModel: protoToModel, toProto: modelToProto}
}

var sides = enumMapping(map[orderpb.OrderSide]model.OrderSide{
	orderpb.OrderSide_ORDER_SIDE_BUY:  model.OrderSideBuy,
	orderpb.OrderSide_ORDER_SIDE_SELL: model.OrderSideSell,
})

var orderTypes = enumMapping(map[orderpb.OrderType]model.OrderType{
	orderpb.OrderType_ORDER_TYPE_LIMIT:  model.OrderTypeLimit,
	orderpb.OrderType_ORDER_TYPE_MARKET: model.OrderTypeMarket,
	orderpb.OrderType_ORDER_TYPE_IOC:    model.OrderTypeIOC,
	orderpb.OrderType_ORDER_TYPE_FOK:    model.OrderTypeFOK,
})

var orderStatuses = enumMapping(map[orderpb.OrderStatus]model.OrderStatus{
	orderpb.OrderStatus_ORDER_STATUS_PENDING:        model.OrderStatusPending,
	orderpb.OrderStatus_ORDER_STATUS_OPEN:           model.OrderStatusOpen,
	orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED: model.OrderStatusPartialFilled,
	orderpb.OrderStatus_ORDER_STATUS_FILLED:         model.OrderStatusFilled,
	orderpb.OrderStatus_ORDER_STATUS_CANCELLED:      model.OrderStatusCancelled,
	orderpb.OrderStatus_ORDER_STATUS_REJECTED:       model.OrderStatusRejected,
})

func (s *OrderServer) ListMarketTrades(ctx context.Context, req *orderpb.ListMarketTradesRequest) (*orderpb.ListMarketTradesResponse, error) {
	trades, err := s.svc.ListMarketTrades(ctx, req.GetSymbol(), req.GetLimit())
	if err != nil {
		return nil, err
	}
	out := make([]*orderpb.MarketTrade, len(trades))
	for i, trade := range trades {
		out[i] = &orderpb.MarketTrade{
			TradeId: trade.TradeID, Price: trade.Price.String(), Quantity: trade.Quantity.String(),
			TakerSide: sides.toProto[trade.TakerSide], CreatedAt: timestamppb.New(trade.CreatedAt),
		}
	}
	return &orderpb.ListMarketTradesResponse{Trades: out}, nil
}

func (s *OrderServer) ListCandles(ctx context.Context, req *orderpb.ListCandlesRequest) (*orderpb.ListCandlesResponse, error) {
	candles, err := s.svc.ListCandles(ctx, req.GetSymbol(), req.GetIntervalSeconds(), req.GetLimit())
	if err != nil {
		return nil, err
	}
	out := make([]*orderpb.Candle, len(candles))
	for i, candle := range candles {
		out[i] = &orderpb.Candle{
			OpenTime: candle.OpenTime, Open: candle.Open.String(), High: candle.High.String(),
			Low: candle.Low.String(), Close: candle.Close.String(), Volume: candle.Volume.String(),
		}
	}
	return &orderpb.ListCandlesResponse{Candles: out}, nil
}

func (s *OrderServer) ListTickers(ctx context.Context, req *orderpb.ListTickersRequest) (*orderpb.ListTickersResponse, error) {
	tickers, err := s.svc.ListTickers(ctx, req.GetSymbols())
	if err != nil {
		return nil, err
	}
	out := make([]*orderpb.Ticker, len(tickers))
	for i, ticker := range tickers {
		out[i] = &orderpb.Ticker{
			Symbol: ticker.Symbol, LastPrice: ticker.Last.String(), Open_24H: ticker.Open.String(),
			High_24H: ticker.High.String(), Low_24H: ticker.Low.String(), Volume_24H: ticker.Volume.String(),
			QuoteVolume_24H: ticker.QuoteVolume.String(), ChangePercent_24H: ticker.ChangePercent, TradeCount_24H: ticker.Count,
		}
	}
	return &orderpb.ListTickersResponse{Tickers: out}, nil
}
