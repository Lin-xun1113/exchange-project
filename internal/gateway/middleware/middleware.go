// Package middleware provides the gateway's Gin middleware.
package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/response"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// RequestID attaches the request ID to HTTP responses and downstream contexts.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = c.GetHeader("X-Trace-ID")
		}
		if requestID == "" {
			requestID = uuid.NewString()
		}

		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Request = c.Request.WithContext(logger.WithRequestID(c.Request.Context(), requestID))
		c.Next()
	}
}

func Metrics(m *metrics.Gateway) gin.HandlerFunc {
	inFlight := m.InFlight()
	return func(c *gin.Context) {
		start := time.Now()
		inFlight.Inc()
		defer func() {
			inFlight.Dec()
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			method := c.Request.Method
			switch method {
			case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
				http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
			default:
				method = "OTHER"
			}
			m.ObserveHTTP(method, route, c.Writer.Status(), time.Since(start))
		}()
		c.Next()
	}
}

func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ctx := c.Request.Context()
		// The OpenTelemetry middleware has started the HTTP span at this point.
		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			span.SetAttributes(attribute.String("request.id", c.GetString("request_id")))
		}

		c.Next()

		logger.WithContext(ctx).Info("access log",
			logger.S("method", c.Request.Method),
			logger.S("path", c.Request.URL.Path),
			logger.I("status", c.Writer.Status()),
			logger.I64("latency_ms", time.Since(start).Milliseconds()),
			logger.S("client_ip", c.ClientIP()),
			logger.S("user_agent", c.Request.UserAgent()),
		)
	}
}

func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.WithContext(c.Request.Context()).Error("panic recovered",
					zap.Any("panic", err), zap.Stack("stack"),
					logger.S("method", c.Request.Method),
					logger.S("path", c.Request.URL.Path),
				)
				c.Abort()
				if !c.Writer.Written() {
					response.InternalServerError(c, "internal server error")
				}
			}
		}()
		c.Next()
	}
}

func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID, Idempotency-Key")
		c.Header("Access-Control-Expose-Headers", "Content-Length, Content-Type, X-Request-ID, Retry-After")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
