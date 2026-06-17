// Package handler provides HTTP Handler implementation
package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/response"
	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
)

// UserServiceClient defines the interface for user service operations needed by handlers
type UserServiceClient interface {
	Login(ctx context.Context, req *userpb.LoginRequest) (*userpb.LoginResponse, error)
	CreateUser(ctx context.Context, req *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error)
	GetUser(ctx context.Context, req *userpb.GetUserRequest) (*userpb.GetUserResponse, error)
	GetBalance(ctx context.Context, req *userpb.GetBalanceRequest) (*userpb.GetBalanceResponse, error)
	FreezeAmount(ctx context.Context, req *userpb.FreezeAmountRequest) (*userpb.FreezeAmountResponse, error)
	UnfreezeAmount(ctx context.Context, req *userpb.UnfreezeAmountRequest) (*userpb.UnfreezeAmountResponse, error)
	DeductAmount(ctx context.Context, req *userpb.DeductAmountRequest) (*userpb.DeductAmountResponse, error)
	AddAmount(ctx context.Context, req *userpb.AddAmountRequest) (*userpb.AddAmountResponse, error)
}

// AuthHandler authentication Handler
type AuthHandler struct {
	userClient UserServiceClient
	jwtSecret  string
	expireTime time.Duration
}

// Verify that *client.UserClient implements UserServiceClient at compile time
var _ UserServiceClient = (*client.UserClient)(nil)

func NewAuthHandler(userClient UserServiceClient, secret string, expire time.Duration) *AuthHandler {
	return &AuthHandler{
		userClient: userClient,
		jwtSecret:  secret,
		expireTime: expire,
	}
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
}

// Login 登录
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}

	// 调用 User Service 进行认证
	loginResp, err := h.userClient.Login(c.Request.Context(), &userpb.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("login failed", logger.Err(err), logger.S("username", req.Username))
		response.Unauthorized(c, "invalid username or password")
		return
	}

	// 生成 JWT token
	token, err := middleware.GenerateToken(loginResp.UserId, loginResp.Username, loginResp.Role, h.jwtSecret, h.expireTime)
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to generate token", logger.Err(err))
		response.InternalServerError(c, "failed to generate token")
		return
	}
	expiresAt := time.Now().Add(h.expireTime).Unix()

	response.Success(c, LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		UserID:    loginResp.UserId,
		Username:  loginResp.Username,
		Role:      loginResp.Role,
	})
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=32"`
	Password string `json:"password" binding:"required,min=6"`
	Email    string `json:"email" binding:"omitempty,email"`
}

// Register 注册
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// 调用 User Service 创建用户
	resp, err := h.userClient.CreateUser(c.Request.Context(), &userpb.CreateUserRequest{
		Username:       req.Username,
		Password:       req.Password,
		Email:          req.Email,
		InitialBalance: "10000", // 默认初始余额 10000
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("register failed", logger.Err(err), logger.S("username", req.Username))
		response.InternalServerError(c, "registration failed: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"message":  "registration successful",
		"user_id":  resp.User.GetId(),
		"username": resp.User.GetUsername(),
	})
}

// HealthHandler 健康检查 Handler
type HealthHandler struct{}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Health 健康检查
func (h *HealthHandler) Health(c *gin.Context) {
	response.Success(c, gin.H{
		"status":    "ok",
		"timestamp": time.Now().Unix(),
	})
}

// Ready 就绪检查
func (h *HealthHandler) Ready(c *gin.Context) {
	response.Success(c, gin.H{
		"status":    "ready",
		"timestamp": time.Now().Unix(),
	})
}

// OrderHandler 订单 Handler
type OrderHandler struct {
	clients *client.Clients
}

func NewOrderHandler(clients *client.Clients) *OrderHandler {
	return &OrderHandler{clients: clients}
}

// CreateOrderRequest 创建订单请求
type CreateOrderRequest struct {
	Symbol         string  `json:"symbol" binding:"required"`
	Side           string  `json:"side" binding:"required,oneof=buy sell"`
	OrderType      string  `json:"order_type" binding:"omitempty,oneof=limit market ioc fok"`
	Price          float64 `json:"price" binding:"required,gt=0"`
	Quantity       float64 `json:"quantity" binding:"required,gt=0"`
	IdempotencyKey string  `json:"idempotency_key" binding:"omitempty"`
}

// CreateOrder 创建订单
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.Unauthorized(c, "user not found")
		return
	}

	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	logger.WithContext(c.Request.Context()).Info("create order",
		logger.I64("user_id", userID),
		logger.S("symbol", req.Symbol),
		logger.S("side", req.Side),
		logger.F("price", req.Price),
		logger.F("quantity", req.Quantity),
	)

	// 先冻结用户余额
	amount := req.Price * req.Quantity
	freezeReq := &userpb.FreezeAmountRequest{
		UserId:  userID,
		Amount:  formatDecimal(amount),
		OrderId: "", // 稍后更新
	}

	freezeResp, err := h.clients.User.FreezeAmount(c.Request.Context(), freezeReq)
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to freeze amount", logger.Err(err))
		response.InternalServerError(c, "failed to freeze amount")
		return
	}

	if !freezeResp.Success {
		response.BadRequest(c, "insufficient balance")
		return
	}

	// 提交订单到撮合引擎
	orderSide := matchingpb.OrderSide_ORDER_SIDE_BUY
	if req.Side == "sell" {
		orderSide = matchingpb.OrderSide_ORDER_SIDE_SELL
	}

	orderType := matchingpb.OrderType_ORDER_TYPE_LIMIT
	switch req.OrderType {
	case "market":
		orderType = matchingpb.OrderType_ORDER_TYPE_MARKET
	case "ioc":
		orderType = matchingpb.OrderType_ORDER_TYPE_IOC
	case "fok":
		orderType = matchingpb.OrderType_ORDER_TYPE_FOK
	}

	matchingReq := &matchingpb.SubmitOrderRequest{
		UserId:    userID,
		Symbol:    req.Symbol,
		Side:      orderSide,
		OrderType: orderType,
		Price:     formatDecimal(req.Price),
		Quantity:  formatDecimal(req.Quantity),
	}

	matchingResp, err := h.clients.Matching.SubmitOrder(c.Request.Context(), matchingReq)
	if err != nil {
		// 撮合失败，解冻金额
		h.clients.User.UnfreezeAmount(c.Request.Context(), &userpb.UnfreezeAmountRequest{
			UserId:  userID,
			Amount:  formatDecimal(amount),
			OrderId: "",
		})
		logger.WithContext(c.Request.Context()).Error("failed to submit order", logger.Err(err))
		response.InternalServerError(c, "failed to submit order")
		return
	}

	// 更新订单状态
	if matchingResp.Success {
		// 完全成交
		h.clients.Order.UpdateOrderStatus(c.Request.Context(), &orderpb.UpdateOrderStatusRequest{
			OrderId:        matchingResp.Trades[0].GetBuyOrderId(),
			Status:         orderpb.OrderStatus_ORDER_STATUS_FILLED,
			FilledQuantity: formatDecimal(req.Quantity),
		})
	} else if matchingResp.RemainingQuantity != "" && matchingResp.RemainingQuantity != "0" {
		// 部分成交
		filledQty := req.Quantity - parseDecimal(matchingResp.RemainingQuantity)
		h.clients.Order.UpdateOrderStatus(c.Request.Context(), &orderpb.UpdateOrderStatusRequest{
			OrderId:        matchingResp.Trades[0].GetBuyOrderId(),
			Status:         orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED,
			FilledQuantity: formatDecimal(filledQty),
		})
	}

	response.Success(c, gin.H{
		"success":           matchingResp.Success,
		"filled_quantity":   matchingResp.RemainingQuantity,
		"trades":            len(matchingResp.Trades),
	})
}

// CancelOrderRequest 取消订单请求
type CancelOrderRequest struct {
	OrderID string `json:"order_id" binding:"required"`
}

// CancelOrder 取消订单
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.Unauthorized(c, "user not found")
		return
	}

	var req CancelOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}

	logger.WithContext(c.Request.Context()).Info("cancel order",
		logger.I64("user_id", userID),
		logger.S("order_id", req.OrderID),
	)

	// 获取订单信息以获取冻结金额
	orderResp, err := h.clients.Order.GetOrder(c.Request.Context(), &orderpb.GetOrderRequest{
		OrderId: req.OrderID,
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to get order", logger.Err(err))
		response.InternalServerError(c, "failed to get order")
		return
	}

	order := orderResp.GetOrder()
	if order == nil {
		response.NotFound(c, "order not found")
		return
	}

	// 解冻金额
	h.clients.User.UnfreezeAmount(c.Request.Context(), &userpb.UnfreezeAmountRequest{
		UserId:  userID,
		Amount:  order.GetRemainingQuantity(),
		OrderId: req.OrderID,
	})

	// 调用订单服务取消订单
	cancelResp, err := h.clients.Order.CancelOrder(c.Request.Context(), &orderpb.CancelOrderRequest{
		OrderId: req.OrderID,
		UserId:  userID,
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to cancel order", logger.Err(err))
		response.InternalServerError(c, "failed to cancel order")
		return
	}

	response.Success(c, gin.H{
		"order_id": req.OrderID,
		"success":  cancelResp.Success,
		"status":   "cancelled",
	})
}

// GetOrder 获取订单
func (h *OrderHandler) GetOrder(c *gin.Context) {
	orderID := c.Param("order_id")

	orderResp, err := h.clients.Order.GetOrder(c.Request.Context(), &orderpb.GetOrderRequest{
		OrderId: orderID,
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to get order", logger.Err(err))
		response.InternalServerError(c, "failed to get order")
		return
	}

	order := orderResp.GetOrder()
	if order == nil {
		response.NotFound(c, "order not found")
		return
	}

	response.Success(c, gin.H{
		"order_id":           order.GetOrderId(),
		"user_id":            order.GetUserId(),
		"symbol":            order.GetSymbol(),
		"side":              order.GetSide().String(),
		"order_type":        order.GetOrderType().String(),
		"price":             order.GetPrice(),
		"quantity":          order.GetQuantity(),
		"filled_quantity":   order.GetFilledQuantity(),
		"remaining_quantity": order.GetRemainingQuantity(),
		"status":            order.GetStatus().String(),
		"created_at":        order.GetCreatedAt(),
		"updated_at":        order.GetUpdatedAt(),
	})
}

// ListOrders 订单列表
func (h *OrderHandler) ListOrders(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.Unauthorized(c, "user not found")
		return
	}

	page := int32(1)
	pageSize := int32(10)

	listResp, err := h.clients.Order.ListOrders(c.Request.Context(), &orderpb.ListOrdersRequest{
		UserId:   userID,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to list orders", logger.Err(err))
		response.InternalServerError(c, "failed to list orders")
		return
	}

	orders := make([]gin.H, 0, len(listResp.GetOrders()))
	for _, order := range listResp.GetOrders() {
		orders = append(orders, gin.H{
			"order_id":         order.GetOrderId(),
			"symbol":           order.GetSymbol(),
			"side":             order.GetSide().String(),
			"price":            order.GetPrice(),
			"quantity":         order.GetQuantity(),
			"filled_quantity":  order.GetFilledQuantity(),
			"status":           order.GetStatus().String(),
			"created_at":       order.GetCreatedAt(),
		})
	}

	response.SuccessWithPage(c, orders, listResp.GetTotal(), int(page), int(pageSize))
}

// BalanceHandler 余额 Handler
type BalanceHandler struct {
	clients *client.Clients
}

func NewBalanceHandler(clients *client.Clients) *BalanceHandler {
	return &BalanceHandler{clients: clients}
}

// GetBalance 获取余额
func (h *BalanceHandler) GetBalance(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		response.Unauthorized(c, "user not found")
		return
	}

	balanceResp, err := h.clients.User.GetBalance(c.Request.Context(), &userpb.GetBalanceRequest{
		UserId: userID,
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to get balance", logger.Err(err))
		response.InternalServerError(c, "failed to get balance")
		return
	}

	response.Success(c, gin.H{
		"user_id":            balanceResp.GetUserId(),
		"available_balance":  balanceResp.GetAvailableBalance(),
		"frozen_balance":    balanceResp.GetFrozenBalance(),
		"total_balance":     balanceResp.GetTotalBalance(),
	})
}

// OrderBookHandler 订单簿 Handler
type OrderBookHandler struct {
	clients *client.Clients
}

func NewOrderBookHandler(clients *client.Clients) *OrderBookHandler {
	return &OrderBookHandler{clients: clients}
}

// GetOrderBook 获取订单簿
func (h *OrderBookHandler) GetOrderBook(c *gin.Context) {
	symbol := c.Param("symbol")
	if symbol == "" {
		symbol = c.Query("symbol")
	}
	if symbol == "" {
		symbol = "BTC/USDT"
	}

	depth := int32(10)
	if d := c.Query("depth"); d != "" {
		// parse depth
	}

	orderbookResp, err := h.clients.Matching.GetOrderBook(c.Request.Context(), &matchingpb.GetOrderBookRequest{
		Symbol: symbol,
		Depth:  depth,
	})
	if err != nil {
		logger.WithContext(c.Request.Context()).Error("failed to get order book", logger.Err(err))
		response.InternalServerError(c, "failed to get order book")
		return
	}

	orderbook := orderbookResp.GetOrderBook()
	if orderbook == nil {
		response.NotFound(c, "order book not found")
		return
	}

	bids := make([]gin.H, 0, len(orderbook.GetBids()))
	for _, bid := range orderbook.GetBids() {
		bids = append(bids, gin.H{
			"price":    bid.GetPrice(),
			"quantity": bid.GetQuantity(),
			"orders":   bid.GetOrderCount(),
		})
	}

	asks := make([]gin.H, 0, len(orderbook.GetAsks()))
	for _, ask := range orderbook.GetAsks() {
		asks = append(asks, gin.H{
			"price":    ask.GetPrice(),
			"quantity": ask.GetQuantity(),
			"orders":   ask.GetOrderCount(),
		})
	}

	var timestamp int64
	if orderbook.GetTimestamp() != nil {
		timestamp = orderbook.GetTimestamp().AsTime().Unix()
	}

	response.Success(c, gin.H{
		"symbol":    symbol,
		"bids":      bids,
		"asks":      asks,
		"timestamp": timestamp,
	})
}

// 辅助函数
func formatDecimal(v float64) string {
	return fmt.Sprintf("%.8f", v)
}

func parseDecimal(s string) float64 {
	var v float64
	fmt.Sscanf(s, "%f", &v)
	return v
}
