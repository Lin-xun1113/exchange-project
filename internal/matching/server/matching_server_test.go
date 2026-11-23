package server

import (
	"testing"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMatchingServer_ConvertsTicksAndRejectsInvalidCommands(t *testing.T) {
	ctx := t.Context()
	m, err := engine.New(engine.Config{WALDir: t.TempDir(), SnapshotDir: t.TempDir(), Sync: wal.SyncNone, Symbols: []string{"BTC/USDT"}})
	require.NoError(t, err)
	defer func() { require.NoError(t, m.Close(ctx)) }()
	s := NewMatchingServer(m, []string{"BTC/USDT"})

	limit := func() *matchingpb.SubmitOrderRequest {
		return &matchingpb.SubmitOrderRequest{OrderId: "maker", UserId: 10, Symbol: "btc-usdt",
			Side: matchingpb.OrderSide_ORDER_SIDE_BUY, Type: matchingpb.OrderType_ORDER_TYPE_LIMIT, Price: "90071992.54740993", Quantity: "1.00000001"}
	}
	for _, invalid := range []func(*matchingpb.SubmitOrderRequest){
		func(r *matchingpb.SubmitOrderRequest) { r.Symbol = "ETH/USDT" },
		func(r *matchingpb.SubmitOrderRequest) { r.OrderId = "" },
		func(r *matchingpb.SubmitOrderRequest) { r.UserId = 0 },
		func(r *matchingpb.SubmitOrderRequest) { r.Side = 99 },
		func(r *matchingpb.SubmitOrderRequest) { r.Type = 99 },
		func(r *matchingpb.SubmitOrderRequest) { r.Quantity = "0.123456789" },
		func(r *matchingpb.SubmitOrderRequest) { r.Quantity = "92233720368.54775808" },
		func(r *matchingpb.SubmitOrderRequest) { r.Price = "0" },
		func(r *matchingpb.SubmitOrderRequest) { r.Type, r.Price = matchingpb.OrderType_ORDER_TYPE_MARKET, "0" },
		func(r *matchingpb.SubmitOrderRequest) {
			r.Side, r.Type, r.Price = matchingpb.OrderSide_ORDER_SIDE_SELL, matchingpb.OrderType_ORDER_TYPE_MARKET, "1"
		},
	} {
		req := limit()
		invalid(req)
		_, err := s.SubmitOrder(ctx, req)
		require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", req)
	}

	maker, err := s.SubmitOrder(ctx, limit())
	require.NoError(t, err)
	require.True(t, maker.Resting)
	depth, err := s.GetOrderBook(ctx, &matchingpb.GetOrderBookRequest{Symbol: "btc-usdt"})
	require.NoError(t, err)
	require.Equal(t, "BTC/USDT", depth.Symbol)
	require.EqualValues(t, 1, depth.Lsn)
	require.Len(t, depth.Bids, 1)
	require.Equal(t, "90071992.54740993", depth.Bids[0].Price)
	require.Equal(t, "1.00000001", depth.Bids[0].Quantity)
	require.EqualValues(t, 1, depth.Bids[0].OrderCount)
	for _, n := range []int32{-1, 201} {
		_, err := s.GetOrderBook(ctx, &matchingpb.GetOrderBookRequest{Symbol: "BTC/USDT", Depth: n})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}

	taker := limit()
	taker.OrderId, taker.UserId, taker.Side, taker.Type, taker.Price, taker.Quantity = "taker", 11,
		matchingpb.OrderSide_ORDER_SIDE_SELL, matchingpb.OrderType_ORDER_TYPE_MARKET, "", "0.1"
	r, err := s.SubmitOrder(ctx, taker)
	require.NoError(t, err)
	require.Equal(t, "0.1", r.FilledQuantity)
	require.Equal(t, "0", r.RemainingQuantity)
	require.Len(t, r.Trades, 1)
	trade := r.Trades[0]
	require.Equal(t, "BTC/USDT-2-0", trade.TradeId)
	require.Equal(t, "BTC/USDT", trade.Symbol)
	require.Equal(t, "maker", trade.BuyOrderId)
	require.Equal(t, "taker", trade.SellOrderId)
	require.EqualValues(t, 10, trade.BuyUserId)
	require.EqualValues(t, 11, trade.SellUserId)
	require.Equal(t, "90071992.54740993", trade.Price)
	require.Equal(t, "0.1", trade.Quantity)
	require.Equal(t, taker.Side, trade.TakerSide)
	require.EqualValues(t, 2, trade.Lsn)
	require.NoError(t, trade.CreatedAt.CheckValid())

	c, err := s.CancelOrder(ctx, &matchingpb.CancelOrderRequest{Symbol: "btc-usdt", OrderId: "maker"})
	require.NoError(t, err)
	require.True(t, c.Found)
	require.Equal(t, "0.90000001", c.RemainingQuantity)

	require.NoError(t, m.Close(ctx))
	_, err = s.SubmitOrder(ctx, limit())
	require.Equal(t, codes.Unavailable, status.Code(err))
}
