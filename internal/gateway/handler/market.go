package handler

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/pkg/response"
)

type levelView struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
	Orders   int32  `json:"orders"`
}

func levelsJSON(levels []*matchingpb.PriceLevel) []levelView {
	out := make([]levelView, len(levels))
	for i, level := range levels {
		out[i] = levelView{Price: level.GetPrice(), Quantity: level.GetQuantity(), Orders: level.GetOrderCount()}
	}
	return out
}

func (h *Handler) OrderBook(c *gin.Context) {
	market, ok := h.canonicalSymbol(c, c.Param("symbol"))
	if !ok {
		return
	}
	const defaultDepth = 20
	depth := int32(defaultDepth)
	if value := c.Query("depth"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil || parsed < 0 || parsed > 200 {
			response.BadRequest(c, "depth must be between 0 and 200")
			return
		}
		if parsed != 0 {
			depth = int32(parsed)
		}
	}
	result, err := h.cl.Matching.GetOrderBook(c.Request.Context(), &matchingpb.GetOrderBookRequest{Symbol: market, Depth: depth})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	response.Success(c, gin.H{
		"symbol": result.GetSymbol(), "bids": levelsJSON(result.GetBids()), "asks": levelsJSON(result.GetAsks()), "lsn": result.GetLsn(),
	})
}

type marketTradeView struct {
	TradeID   string `json:"trade_id"`
	Price     string `json:"price"`
	Quantity  string `json:"quantity"`
	TakerSide string `json:"taker_side"`
	CreatedAt string `json:"created_at"`
}

func (h *Handler) MarketTrades(c *gin.Context) {
	market, ok := h.canonicalSymbol(c, c.Param("symbol"))
	if !ok {
		return
	}
	limit, ok := querySize(c, "limit", 50, 200)
	if !ok {
		return
	}
	result, err := h.cl.Order.ListMarketTrades(c.Request.Context(), &orderpb.ListMarketTradesRequest{
		Symbol: market, Limit: limit,
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	trades := make([]marketTradeView, len(result.GetTrades()))
	for i, trade := range result.GetTrades() {
		trades[i] = marketTradeView{
			TradeID: trade.GetTradeId(), Price: trade.GetPrice(), Quantity: trade.GetQuantity(),
			TakerSide: enumName(orderSides[:], int32(trade.GetTakerSide())), CreatedAt: timestamp(trade.GetCreatedAt()),
		}
	}
	response.Success(c, gin.H{"symbol": market, "trades": trades})
}

var candleIntervals = map[string]int32{
	"1m": 60, "5m": 300, "15m": 900, "1h": 3600, "4h": 14400, "1d": 86400,
}

type candleView struct {
	Time   int64  `json:"time"`
	Open   string `json:"open"`
	High   string `json:"high"`
	Low    string `json:"low"`
	Close  string `json:"close"`
	Volume string `json:"volume"`
}

func (h *Handler) Candles(c *gin.Context) {
	market, ok := h.canonicalSymbol(c, c.Param("symbol"))
	if !ok {
		return
	}
	interval := c.DefaultQuery("interval", "15m")
	seconds, ok := candleIntervals[interval]
	if !ok {
		response.BadRequest(c, "invalid interval")
		return
	}
	limit, ok := querySize(c, "limit", 300, 1000)
	if !ok {
		return
	}
	result, err := h.cl.Order.ListCandles(c.Request.Context(), &orderpb.ListCandlesRequest{
		Symbol: market, IntervalSeconds: seconds, Limit: limit,
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	candles := make([]candleView, len(result.GetCandles()))
	for i, candle := range result.GetCandles() {
		candles[i] = candleView{
			Time: candle.GetOpenTime(), Open: candle.GetOpen(), High: candle.GetHigh(),
			Low: candle.GetLow(), Close: candle.GetClose(), Volume: candle.GetVolume(),
		}
	}
	response.Success(c, gin.H{"symbol": market, "interval": interval, "candles": candles})
}

type tickerView struct {
	Symbol         string `json:"symbol"`
	LastPrice      string `json:"last_price"`
	Open24H        string `json:"open_24h"`
	High24H        string `json:"high_24h"`
	Low24H         string `json:"low_24h"`
	Volume24H      string `json:"volume_24h"`
	QuoteVolume24H string `json:"quote_volume_24h"`
	ChangePercent  string `json:"change_percent_24h"`
	TradeCount24H  int64  `json:"trade_count_24h"`
}

func (h *Handler) Tickers(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.BadRequest(c, "invalid symbols query")
		return
	}
	markets := h.symbols
	if raw := query.Get("symbols"); raw != "" {
		markets = strings.Split(raw, ",")
		for i, value := range markets {
			market, ok := h.canonicalSymbol(c, value)
			if !ok {
				return
			}
			markets[i] = market
		}
	}
	result, err := h.cl.Order.ListTickers(c.Request.Context(), &orderpb.ListTickersRequest{Symbols: markets})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	tickers := make([]tickerView, len(result.GetTickers()))
	for i, ticker := range result.GetTickers() {
		tickers[i] = tickerView{
			Symbol: ticker.GetSymbol(), LastPrice: ticker.GetLastPrice(), Open24H: ticker.GetOpen_24H(),
			High24H: ticker.GetHigh_24H(), Low24H: ticker.GetLow_24H(), Volume24H: ticker.GetVolume_24H(),
			QuoteVolume24H: ticker.GetQuoteVolume_24H(), ChangePercent: ticker.GetChangePercent_24H(),
			TradeCount24H: ticker.GetTradeCount_24H(),
		}
	}
	response.Success(c, tickers)
}
