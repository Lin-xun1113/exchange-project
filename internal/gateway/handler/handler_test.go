// Package handler_test provides Handler tests
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/gateway/handler"
	"github.com/linxun2025/exchange-project/internal/gateway/middleware"
	"github.com/linxun2025/exchange-project/pkg/response"
	"github.com/stretchr/testify/assert"
)

// mockUserClient is a mock implementation of userpb.UserServiceClient
type mockUserClient struct {
	loginResp  *userpb.LoginResponse
	loginErr   error
	createResp *userpb.CreateUserResponse
	createErr  error
}

func (m *mockUserClient) Login(_ context.Context, _ *userpb.LoginRequest) (*userpb.LoginResponse, error) {
	if m.loginErr != nil {
		return nil, m.loginErr
	}
	return m.loginResp, nil
}

func (m *mockUserClient) GetUser(_ context.Context, _ *userpb.GetUserRequest) (*userpb.GetUserResponse, error) {
	return nil, nil
}

func (m *mockUserClient) CreateUser(_ context.Context, _ *userpb.CreateUserRequest) (*userpb.CreateUserResponse, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.createResp, nil
}

func (m *mockUserClient) GetBalance(_ context.Context, _ *userpb.GetBalanceRequest) (*userpb.GetBalanceResponse, error) {
	return nil, nil
}

func (m *mockUserClient) FreezeAmount(_ context.Context, _ *userpb.FreezeAmountRequest) (*userpb.FreezeAmountResponse, error) {
	return nil, nil
}

func (m *mockUserClient) UnfreezeAmount(_ context.Context, _ *userpb.UnfreezeAmountRequest) (*userpb.UnfreezeAmountResponse, error) {
	return nil, nil
}

func (m *mockUserClient) DeductAmount(_ context.Context, _ *userpb.DeductAmountRequest) (*userpb.DeductAmountResponse, error) {
	return nil, nil
}

func (m *mockUserClient) AddAmount(_ context.Context, _ *userpb.AddAmountRequest) (*userpb.AddAmountResponse, error) {
	return nil, nil
}

func init() {
	gin.SetMode(gin.TestMode)
}

// TestHealthHandler tests health check
func TestHealthHandler(t *testing.T) {
	h := handler.NewHealthHandler()

	r := gin.New()
	r.GET("/healthz", h.Health)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp response.Response
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, 0, resp.Code)
}

// TestAuthHandler_Login tests login
func TestAuthHandler_Login(t *testing.T) {
	mockClient := &mockUserClient{
		loginResp: &userpb.LoginResponse{
			UserId:   1,
			Username: "testuser",
			Role:     "trader",
		},
	}
	authHandler := handler.NewAuthHandler(mockClient, "test-secret", 24*time.Hour)

	r := gin.New()
	r.POST("/login", authHandler.Login)

	body := map[string]string{
		"username": "testuser",
		"password": "password123",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp response.Response
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, 0, resp.Code)

	// Verify the returned token
	data, ok := resp.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, data["token"])
}

// TestAuthHandler_Login_InvalidRequest tests login with invalid request
func TestAuthHandler_Login_InvalidRequest(t *testing.T) {
	mockClient := &mockUserClient{}
	authHandler := handler.NewAuthHandler(mockClient, "test-secret", 24*time.Hour)

	r := gin.New()
	r.POST("/login", authHandler.Login)

	// Empty request body
	req := httptest.NewRequest("POST", "/login", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestAuthHandler_Login_AuthFailure tests login with authentication failure
func TestAuthHandler_Login_AuthFailure(t *testing.T) {
	mockClient := &mockUserClient{
		loginErr: fmt.Errorf("invalid credentials"),
	}
	authHandler := handler.NewAuthHandler(mockClient, "test-secret", 24*time.Hour)

	r := gin.New()
	r.POST("/login", authHandler.Login)

	body := map[string]string{
		"username": "testuser",
		"password": "wrongpassword",
	}
	jsonBody, _ := json.Marshal(body)

	req := httptest.NewRequest("POST", "/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestJWTMiddleware 测试 JWT 中间件
func TestJWTMiddleware(t *testing.T) {
	secret := "test-secret"
	expireTime := 24 * time.Hour

	// 生成一个有效的 token
	token, err := middleware.GenerateToken(1, "testuser", "trader", secret, expireTime)
	assert.NoError(t, err)

	r := gin.New()
	r.Use(middleware.JWT(middleware.JWTConfig{
		Secret:     secret,
		ExpireTime: expireTime,
	}))
	r.GET("/protected", func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		username := middleware.GetUsername(c)
		role := middleware.GetRole(c)

		c.JSON(http.StatusOK, gin.H{
			"user_id":  userID,
			"username": username,
			"role":     role,
		})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, float64(1), resp["user_id"])
	assert.Equal(t, "testuser", resp["username"])
	assert.Equal(t, "trader", resp["role"])
}

// TestJWTMiddleware_InvalidToken 测试无效 Token
func TestJWTMiddleware_InvalidToken(t *testing.T) {
	secret := "test-secret"

	r := gin.New()
	r.Use(middleware.JWT(middleware.JWTConfig{
		Secret:     secret,
		ExpireTime: 24 * time.Hour,
	}))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestJWTMiddleware_MissingToken 测试缺失 Token
func TestJWTMiddleware_MissingToken(t *testing.T) {
	secret := "test-secret"

	r := gin.New()
	r.Use(middleware.JWT(middleware.JWTConfig{
		Secret:     secret,
		ExpireTime: 24 * time.Hour,
	}))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
