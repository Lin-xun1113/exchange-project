// Package middleware 提供 Gin 中间件
package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/response"
)

// RequestID 请求ID中间件
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = c.GetHeader("X-Trace-ID")
		}

		if requestID == "" {
			requestID = generateRequestID()
		}

		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// AccessLog 访问日志中间件（带指标记录）
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// 增加正在处理的请求数
		m := metrics.GetMetrics()
		m.IncHTTPRequestsInFlight()

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		// 减少正在处理的请求数
		m.DecHTTPRequestsInFlight()

		// 记录指标
		m.RecordHTTPRequest(c.Request.Method, path, status, latency)

		logger.Info("access log",
			logger.S("method", c.Request.Method),
			logger.S("path", path),
			logger.S("query", query),
			logger.I("status", status),
			logger.I64("latency_ms", latency.Milliseconds()),
			logger.S("client_ip", c.ClientIP()),
			logger.S("user_agent", c.Request.UserAgent()),
			logger.S("request_id", c.GetString("request_id")),
		)
	}
}

// Recovery 恢复中间件，防止 panic（带指标记录）
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		defer func() {
			if err := recover(); err != nil {
				logger.Error("panic recovered",
					logger.Err(err.(error)),
					logger.S("path", c.Request.URL.Path),
					logger.S("method", c.Request.Method),
					logger.S("request_id", c.GetString("request_id")),
				)

				// 记录错误指标
				m := metrics.GetMetrics()
				m.RecordHTTPRequest(c.Request.Method, c.Request.URL.Path, 500, time.Since(start))

				response.InternalServerError(c, "internal server error")
				c.Abort()
			}
		}()

		c.Next()
	}
}

// CORS 跨域中间件
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID, X-Idempotency-Key")
		c.Header("Access-Control-Expose-Headers", "Content-Length, Content-Type, X-Request-ID")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// RateLimit 简单限流中间件（基于 IP）
// 实际生产中应使用 Redis 实现分布式限流
func RateLimit(requests int, window time.Duration) gin.HandlerFunc {
	// 这里实现一个简单的内存限流，生产环境应使用 Redis
	type clientInfo struct {
		count     int
		resetTime time.Time
	}

	clients := make(map[string]*clientInfo)

	return func(c *gin.Context) {
		ip := c.ClientIP()

		now := time.Now()

		info, exists := clients[ip]
		if !exists || now.After(info.resetTime) {
			clients[ip] = &clientInfo{
				count:     1,
				resetTime: now.Add(window),
			}
			c.Next()
			return
		}

		info.count++
		if info.count > requests {
			logger.Warn("rate limit exceeded",
				logger.S("client_ip", ip),
				logger.S("path", c.Request.URL.Path),
			)
			response.TooManyRequests(c, "rate limit exceeded")
			c.Abort()
			return
		}

		c.Next()
	}
}

// generateRequestID 生成请求ID
func generateRequestID() string {
	return time.Now().Format("20060102150405.000000")
}
