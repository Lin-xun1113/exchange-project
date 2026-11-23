package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"github.com/linxun2025/exchange-project/pkg/symbol"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type Candle struct {
	OpenTime                       int64
	Open, High, Low, Close, Volume decimal.Decimal
}

type Ticker struct {
	Symbol                                     string
	Last, Open, High, Low, Volume, QuoteVolume decimal.Decimal
	ChangePercent                              string
	Count                                      int64
}

func (s *OrderService) marketSymbol(market string) (string, error) {
	pair, err := symbol.Parse(market)
	if err != nil || !s.allowed(pair) {
		return "", status.Error(codes.InvalidArgument, "unsupported symbol")
	}
	return pair.String(), nil
}

func (s *OrderService) ListMarketTrades(ctx context.Context, market string, limit int32) ([]model.Trade, error) {
	market, err := s.marketSymbol(market)
	if err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, status.Error(codes.InvalidArgument, "limit must be between 1 and 200")
	}
	trades, err := repository.ListMarketTrades(s.db.WithContext(ctx), market, int(limit))
	if err != nil {
		return nil, databaseError(ctx, "list market trades", err)
	}
	return trades, nil
}

func (s *OrderService) ListCandles(ctx context.Context, market string, intervalSeconds, limit int32) ([]Candle, error) {
	market, err := s.marketSymbol(market)
	if err != nil {
		return nil, err
	}
	switch intervalSeconds {
	case 60, 300, 900, 3600, 14400, 86400:
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported candle interval")
	}
	if limit == 0 {
		limit = 300
	}
	if limit < 1 || limit > 1000 {
		return nil, status.Error(codes.InvalidArgument, "limit must be between 1 and 1000")
	}
	iv := int64(intervalSeconds)
	end := time.Now().Unix() / iv * iv
	start := end - int64(limit-1)*iv
	trades, err := repository.TradesSince(s.db.WithContext(ctx), market, time.Unix(start, 0).UTC())
	if err != nil {
		return nil, databaseError(ctx, "list candles", err)
	}
	candles := make([]Candle, 0, min(int(limit), len(trades)))
	for _, trade := range trades {
		bucket := trade.CreatedAt.Unix() / iv * iv
		if bucket > end {
			break
		}
		if len(candles) == 0 || candles[len(candles)-1].OpenTime != bucket {
			p := trade.Price
			candles = append(candles, Candle{OpenTime: bucket, Open: p, High: p, Low: p, Close: p, Volume: trade.Quantity})
			continue
		}
		c := &candles[len(candles)-1]
		c.High = decimal.Max(c.High, trade.Price)
		c.Low = decimal.Min(c.Low, trade.Price)
		c.Close = trade.Price
		c.Volume = c.Volume.Add(trade.Quantity)
	}
	return candles, nil
}

func (s *OrderService) ListTickers(ctx context.Context, markets []string) ([]Ticker, error) {
	if len(markets) == 0 {
		return nil, status.Error(codes.InvalidArgument, "symbols required")
	}
	out := make([]Ticker, len(markets))
	for i, market := range markets {
		canonical, err := s.marketSymbol(market)
		if err != nil {
			return nil, err
		}
		out[i].Symbol = canonical
	}

	since := time.Now().UTC().Add(-24 * time.Hour)
	err := s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		aggregated := make(map[string]Ticker)
		for i := range out {
			ticker := &out[i]
			if cached, ok := aggregated[ticker.Symbol]; ok {
				*ticker = cached
				continue
			}
			trades, err := repository.TradesSince(db, ticker.Symbol, since)
			if err != nil {
				return err
			}
			last, found, err := repository.LatestTrade(db, ticker.Symbol)
			if err != nil {
				return err
			}
			if found {
				ticker.Last = last.Price
			}
			ticker.Count = int64(len(trades))
			ticker.ChangePercent = "0.00"
			if len(trades) == 0 {
				ticker.Open, ticker.High, ticker.Low = ticker.Last, ticker.Last, ticker.Last
				aggregated[ticker.Symbol] = *ticker
				continue
			}

			first := trades[0]
			ticker.Open, ticker.High, ticker.Low = first.Price, first.Price, first.Price
			ticker.Volume = first.Quantity
			ticker.QuoteVolume = first.Price.Mul(first.Quantity)
			for _, trade := range trades[1:] {
				ticker.High = decimal.Max(ticker.High, trade.Price)
				ticker.Low = decimal.Min(ticker.Low, trade.Price)
				ticker.Volume = ticker.Volume.Add(trade.Quantity)
				ticker.QuoteVolume = ticker.QuoteVolume.Add(trade.Price.Mul(trade.Quantity))
			}
			if !ticker.Open.IsZero() {
				ticker.ChangePercent = ticker.Last.Sub(ticker.Open).Div(ticker.Open).Mul(decimal.NewFromInt(100)).StringFixed(2)
			}
			aggregated[ticker.Symbol] = *ticker
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, databaseError(ctx, "list tickers", err)
	}
	return out, nil
}
