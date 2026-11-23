package handler

import (
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/response"
	"github.com/linxun2025/exchange-project/pkg/symbol"
)

func (h *Handler) CreateOrder(c *gin.Context) {
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var req struct {
		Symbol   string `json:"symbol" binding:"required"`
		Side     string `json:"side" binding:"required"`
		Type     string `json:"type" binding:"required"`
		Price    string `json:"price"`
		Quantity string `json:"quantity" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	market, ok := h.canonicalSymbol(c, req.Symbol)
	if !ok {
		return
	}
	side := slices.Index(orderSides[:], req.Side)
	if side <= 0 {
		response.BadRequest(c, "invalid side")
		return
	}
	kind := slices.Index(orderTypes[:], req.Type)
	if kind <= 0 {
		response.BadRequest(c, "invalid order type")
		return
	}
	result, err := h.cl.Order.CreateOrder(c.Request.Context(), &orderpb.CreateOrderRequest{
		UserId: middleware.GetUserID(c), Symbol: market,
		Side: orderpb.OrderSide(side), Type: orderpb.OrderType(kind),
		Price: req.Price, Quantity: req.Quantity, IdempotencyKey: key,
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"order": orderJSON(result.GetOrder()), "replayed": result.GetReplayed()})
}

func (h *Handler) CancelOrder(c *gin.Context) {
	result, err := h.cl.Order.CancelOrder(c.Request.Context(), &orderpb.CancelOrderRequest{
		UserId: middleware.GetUserID(c), OrderId: c.Param("order_id"),
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"order": orderJSON(result.GetOrder())})
}

func (h *Handler) GetOrder(c *gin.Context) {
	result, err := h.cl.Order.GetOrder(c.Request.Context(), &orderpb.GetOrderRequest{
		UserId: middleware.GetUserID(c), OrderId: c.Param("order_id"),
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"order": orderJSON(result.GetOrder())})
}

func (h *Handler) ListOrders(c *gin.Context) {
	market, size, ok := h.listFilter(c)
	if !ok {
		return
	}
	var state orderpb.OrderStatus
	if filter := c.Query("status"); filter != "" {
		index := slices.Index(orderStatuses[:], filter)
		if index <= 0 {
			response.BadRequest(c, "invalid order status")
			return
		}
		state = orderpb.OrderStatus(index)
	}
	result, err := h.cl.Order.ListOrders(c.Request.Context(), &orderpb.ListOrdersRequest{
		UserId: middleware.GetUserID(c), Symbol: market, Status: state, PageSize: size, PageToken: c.Query("page_token"),
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	orders := make([]orderView, len(result.GetOrders()))
	for i, order := range result.GetOrders() {
		orders[i] = orderJSON(order)
	}
	response.Success(c, gin.H{"orders": orders, "next_page_token": result.GetNextPageToken()})
}

func (h *Handler) ListTrades(c *gin.Context) {
	market, size, ok := h.listFilter(c)
	if !ok {
		return
	}
	result, err := h.cl.Order.ListTrades(c.Request.Context(), &orderpb.ListTradesRequest{
		UserId: middleware.GetUserID(c), Symbol: market, PageSize: size, PageToken: c.Query("page_token"),
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	trades := make([]tradeView, len(result.GetTrades()))
	for i, trade := range result.GetTrades() {
		trades[i] = tradeJSON(trade)
	}
	response.Success(c, gin.H{"trades": trades, "next_page_token": result.GetNextPageToken()})
}

func (h *Handler) listFilter(c *gin.Context) (market string, size int32, ok bool) {
	if raw := c.Query("symbol"); raw != "" {
		if market, ok = h.canonicalSymbol(c, raw); !ok {
			return "", 0, false
		}
	}
	size, ok = querySize(c, "page_size", 50)
	return market, size, ok
}

func (h *Handler) canonicalSymbol(c *gin.Context, raw string) (string, bool) {
	p, err := symbol.Parse(raw)
	if err != nil || !h.allowed(p) {
		response.BadRequest(c, "invalid symbol")
		return "", false
	}
	return p.String(), true
}

func querySize(c *gin.Context, param string, fallback int32) (int32, bool) {
	value := c.Query(param)
	if value == "" {
		return fallback, true
	}
	size, err := strconv.ParseInt(value, 10, 32)
	if err != nil || size < 1 || size > 200 {
		response.BadRequest(c, param+" must be between 1 and 200")
		return 0, false
	}
	return int32(size), true
}
