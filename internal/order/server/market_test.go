package server_test

import (
	"fmt"
	"testing"
	"time"

	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/server"
	"github.com/linxun2025/exchange-project/internal/order/service"
	"github.com/linxun2025/exchange-project/internal/testutil/mysqltest"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMarketQueries(t *testing.T) {
	db := mysqltest.Open(t)
	api := server.NewOrderServer(service.New(db, []string{"BTC/USDT", "ETH/USDT"}, nil))
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	prints := []struct {
		at              time.Time
		price, quantity string
		side            model.OrderSide
	}{
		{now.Add(-30 * time.Hour), "100", "1", model.OrderSideBuy},
		{now.Add(-23 * time.Hour), "110", "1", model.OrderSideSell},
		{now.Add(-2 * time.Hour), "99", "2", model.OrderSideBuy},
		{now.Add(-5 * time.Minute), "120", "1", model.OrderSideSell},
		{now.Add(-5 * time.Minute), "121", "1", model.OrderSideBuy},
	}
	for i, p := range prints {
		trade := model.Trade{
			TradeID: fmt.Sprintf("t%d", i+1), Symbol: "BTC/USDT",
			BuyOrderID: fmt.Sprintf("o%d", 2*i+1), SellOrderID: fmt.Sprintf("o%d", 2*i+2),
			BuyUserID: 1, SellUserID: 2, TakerSide: p.side, CreatedAt: p.at,
			Price: decimal.RequireFromString(p.price), Quantity: decimal.RequireFromString(p.quantity),
		}
		require.NoError(t, db.Create(&trade).Error)
	}

	t.Run("24h ticker and empty market", func(t *testing.T) {
		resp, err := api.ListTickers(ctx, &orderpb.ListTickersRequest{Symbols: []string{"btc-usdt", "ETH/USDT"}})
		require.NoError(t, err)
		require.Len(t, resp.Tickers, 2)
		requireTicker(t, resp.Tickers[0], "BTC/USDT", "121", "110", "121", "99", "5", "549", "10.00", 4)
		requireTicker(t, resp.Tickers[1], "ETH/USDT", "0", "0", "0", "0", "0", "0", "0.00", 0)
		duplicates, err := api.ListTickers(ctx, &orderpb.ListTickersRequest{Symbols: []string{"btc-usdt", "ETH/USDT", "BTC/USDT"}})
		require.NoError(t, err)
		require.Len(t, duplicates.Tickers, 3)
		requireTicker(t, duplicates.Tickers[2], "BTC/USDT", "121", "110", "121", "99", "5", "549", "10.00", 4)
	})

	t.Run("latest print outside 24h", func(t *testing.T) {
		trade := model.Trade{
			TradeID: "eth-old", Symbol: "ETH/USDT", BuyOrderID: "o11", SellOrderID: "o12",
			BuyUserID: 1, SellUserID: 2, TakerSide: model.OrderSideBuy, CreatedAt: now.Add(-25 * time.Hour),
			Price: decimal.NewFromInt(80), Quantity: decimal.NewFromInt(3),
		}
		require.NoError(t, db.Create(&trade).Error)
		resp, err := api.ListTickers(ctx, &orderpb.ListTickersRequest{Symbols: []string{"eth-usdt"}})
		require.NoError(t, err)
		require.Len(t, resp.Tickers, 1)
		requireTicker(t, resp.Tickers[0], "ETH/USDT", "80", "80", "80", "80", "0", "0", "0.00", 0)
	})

	t.Run("latest trades break timestamp ties by ID", func(t *testing.T) {
		resp, err := api.ListMarketTrades(ctx, &orderpb.ListMarketTradesRequest{Symbol: "BTC/USDT", Limit: 2})
		require.NoError(t, err)
		require.Len(t, resp.Trades, 2)
		require.Equal(t, "t5", resp.Trades[0].TradeId)
		requireMarketDecimal(t, "121", resp.Trades[0].Price)
		requireMarketDecimal(t, "1", resp.Trades[0].Quantity)
		require.Equal(t, orderpb.OrderSide_ORDER_SIDE_BUY, resp.Trades[0].TakerSide)
		require.True(t, prints[4].at.Equal(resp.Trades[0].CreatedAt.AsTime()))
		require.Equal(t, "t4", resp.Trades[1].TradeId)
		requireMarketDecimal(t, "120", resp.Trades[1].Price)
		require.Equal(t, orderpb.OrderSide_ORDER_SIDE_SELL, resp.Trades[1].TakerSide)
	})

	t.Run("hourly candles break timestamp ties by ID", func(t *testing.T) {
		resp, err := api.ListCandles(ctx, &orderpb.ListCandlesRequest{Symbol: "BTC/USDT", IntervalSeconds: 3600, Limit: 48})
		require.NoError(t, err)
		require.Len(t, resp.Candles, 4)
		wantTimes := []int64{prints[0].at.Truncate(time.Hour).Unix(), prints[1].at.Truncate(time.Hour).Unix(),
			prints[2].at.Truncate(time.Hour).Unix(), prints[3].at.Truncate(time.Hour).Unix()}
		for i, want := range wantTimes {
			require.Equal(t, want, resp.Candles[i].OpenTime)
		}
		last := resp.Candles[3]
		requireMarketDecimal(t, "120", last.Open)
		requireMarketDecimal(t, "121", last.High)
		requireMarketDecimal(t, "120", last.Low)
		requireMarketDecimal(t, "121", last.Close)
		requireMarketDecimal(t, "2", last.Volume)
	})
}

func TestMarketQueryValidation(t *testing.T) {
	api := server.NewOrderServer(service.New(nil, []string{"BTC/USDT", "ETH/USDT"}, nil))
	ctx := t.Context()

	_, err := api.ListCandles(ctx, &orderpb.ListCandlesRequest{Symbol: "BTC/USDT", IntervalSeconds: 61})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "unsupported candle interval", status.Convert(err).Message())
	_, err = api.ListCandles(ctx, &orderpb.ListCandlesRequest{Symbol: "BTC/USDT", IntervalSeconds: 60, Limit: 1001})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = api.ListTickers(ctx, &orderpb.ListTickersRequest{})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "symbols required", status.Convert(err).Message())
	_, err = api.ListTickers(ctx, &orderpb.ListTickersRequest{Symbols: []string{"BTC/USDT", "SOL/USDT"}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = api.ListMarketTrades(ctx, &orderpb.ListMarketTradesRequest{Symbol: "SOL/USDT"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "unsupported symbol", status.Convert(err).Message())
	_, err = api.ListMarketTrades(ctx, &orderpb.ListMarketTradesRequest{Symbol: "BTC/USDT", Limit: 201})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "limit must be between 1 and 200", status.Convert(err).Message())
}

func requireTicker(t *testing.T, ticker *orderpb.Ticker, symbol, last, open, high, low, volume, quoteVolume, change string, count int64) {
	t.Helper()
	require.Equal(t, symbol, ticker.Symbol)
	requireMarketDecimal(t, last, ticker.LastPrice)
	requireMarketDecimal(t, open, ticker.Open_24H)
	requireMarketDecimal(t, high, ticker.High_24H)
	requireMarketDecimal(t, low, ticker.Low_24H)
	requireMarketDecimal(t, volume, ticker.Volume_24H)
	requireMarketDecimal(t, quoteVolume, ticker.QuoteVolume_24H)
	require.Equal(t, change, ticker.ChangePercent_24H)
	require.Equal(t, count, ticker.TradeCount_24H)
}

func requireMarketDecimal(t *testing.T, want, got string) {
	t.Helper()
	value, err := decimal.NewFromString(got)
	require.NoError(t, err)
	require.Truef(t, decimal.RequireFromString(want).Equal(value), "want %s, got %s", want, got)
}
