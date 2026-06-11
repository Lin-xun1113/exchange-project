// Package middleware 提供 JWT 认证中间件
package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/response"
)

const (
	AuthorizationHeader = "Authorization"
	BearerPrefix       = "Bearer "
	UserIDKey          = "user_id"
	UsernameKey        = "username"
	RoleKey            = "role"
)

// Claims JWT Claims
type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// JWTConfig JWT 配置
type JWTConfig struct {
	Secret     string
	ExpireTime time.Duration
}

// JWT JWT 认证中间件
func JWT(cfg JWTConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader(AuthorizationHeader)
		if authHeader == "" {
			response.Unauthorized(c, "missing authorization header")
			c.Abort()
			return
		}

		if !strings.HasPrefix(authHeader, BearerPrefix) {
			response.Unauthorized(c, "invalid authorization header format")
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, BearerPrefix)

		claims, err := ParseToken(tokenString, cfg.Secret)
		if err != nil {
			if err == jwt.ErrTokenExpired {
				response.Unauthorized(c, "token expired")
			} else {
				response.Unauthorized(c, "invalid token")
			}
			c.Abort()
			return
		}

		c.Set(UserIDKey, claims.UserID)
		c.Set(UsernameKey, claims.Username)
		c.Set(RoleKey, claims.Role)

		c.Next()
	}
}

// ParseToken 解析 Token
func ParseToken(tokenString, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.ErrTokenInvalid
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.ErrTokenInvalid
	}

	return claims, nil
}

// GenerateToken 生成 Token
func GenerateToken(userID int64, username, role string, secret string, expireTime time.Duration) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expireTime)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "exchange-project",
			Subject:   username,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GetUserID 获取用户ID
func GetUserID(c *gin.Context) int64 {
	if userID, exists := c.Get(UserIDKey); exists {
		return userID.(int64)
	}
	return 0
}

// GetUsername 获取用户名
func GetUsername(c *gin.Context) string {
	if username, exists := c.Get(UsernameKey); exists {
		return username.(string)
	}
	return ""
}

// GetRole 获取用户角色
func GetRole(c *gin.Context) string {
	if role, exists := c.Get(RoleKey); exists {
		return role.(string)
	}
	return ""
}

// RefreshToken 刷新 Token
func RefreshToken(tokenString, secret string, expireTime time.Duration) (string, error) {
	claims, err := ParseToken(tokenString, secret)
	if err != nil {
		return "", err
	}

	return GenerateToken(claims.UserID, claims.Username, claims.Role, secret, expireTime)
}

// logger wrapper for debug logging
var _ = logger.Info
