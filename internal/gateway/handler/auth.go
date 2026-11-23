// Package handler implements the gateway's HTTP-to-gRPC translation.
package handler

import (
	"regexp"

	"github.com/gin-gonic/gin"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/gateway/client"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/response"
	"github.com/linxun2025/exchange-project/pkg/symbol"
)

type Handler struct {
	cl      *client.Clients
	jwt     middleware.JWTConfig
	allowed func(symbol.Pair) bool
}

func New(cl *client.Clients, jwt middleware.JWTConfig, symbols []string) *Handler {
	return &Handler{cl: cl, jwt: jwt, allowed: symbol.Allowed(symbols)}
}

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func idempotencyKey(c *gin.Context) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if !idempotencyKeyPattern.MatchString(key) {
		response.BadRequest(c, "missing or invalid Idempotency-Key header")
		return "", false
	}
	return key, true
}

func (h *Handler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	result, err := h.cl.User.Login(c.Request.Context(), &userpb.LoginRequest{Username: req.Username, Password: req.Password})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	token, expiresAt, err := middleware.GenerateToken(result.GetUserId(), result.GetUsername(), result.GetRole(), h.jwt.Secret, h.jwt.ExpireTime)
	if err != nil {
		response.InternalServerError(c, "failed to generate token")
		return
	}
	response.Success(c, gin.H{
		"token":      token,
		"expires_at": expiresAt,
		"user_id":    result.GetUserId(),
		"username":   result.GetUsername(),
		"role":       result.GetRole(),
	})
}

func (h *Handler) Register(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required,min=3,max=32"`
		Password string `json:"password" binding:"required,min=6"`
		Email    string `json:"email" binding:"omitempty,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request")
		return
	}
	result, err := h.cl.User.CreateUser(c.Request.Context(), &userpb.CreateUserRequest{
		Username: req.Username, Password: req.Password, Email: req.Email,
	})
	if err != nil {
		writeRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"user_id": result.GetUser().GetId(), "username": result.GetUser().GetUsername()})
}
