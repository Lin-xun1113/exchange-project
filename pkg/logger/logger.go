// Package logger 提供结构化日志功能，基于 zap
package logger

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type contextKey string

const (
	RequestIDKey contextKey = "request_id"
	TraceIDKey   contextKey = "trace_id"
	UserIDKey    contextKey = "user_id"
)

// Logger 全局日志实例
var log *zap.Logger

// Init 初始化日志
func Init(environment string) error {
	var cfg zap.Config
	if environment == "production" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
	}
	cfg.EncoderConfig.StacktraceKey = ""

	var err error
	log, err = cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return err
	}

	return nil
}

// Sync 刷新日志缓冲
func Sync() {
	if log != nil {
		log.Sync()
	}
}

// GetLogger 获取日志实例
func GetLogger() *zap.Logger {
	if log == nil {
		log, _ = zap.NewProduction()
	}
	return log
}

// WithContext 返回带上下文的日志
func WithContext(ctx context.Context) *zap.Logger {
	l := GetLogger()

	requestID := ctx.Value(RequestIDKey)
	if requestID != nil {
		l = l.With(zap.String("request_id", requestID.(string)))
	}

	traceID := ctx.Value(TraceIDKey)
	if traceID != nil {
		l = l.With(zap.String("trace_id", traceID.(string)))
	}

	userID := ctx.Value(UserIDKey)
	if userID != nil {
		l = l.With(zap.Int64("user_id", userID.(int64)))
	}

	return l
}

// NewContextWithRequestID 创建带 request_id 的 context
func NewContextWithRequestID(ctx context.Context) context.Context {
	requestID := uuid.New().String()
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// GetRequestID 从 context 获取 request_id
func GetRequestID(ctx context.Context) string {
	if requestID := ctx.Value(RequestIDKey); requestID != nil {
		return requestID.(string)
	}
	return ""
}

// NewContextWithTraceID 创建带 trace_id 的 context
func NewContextWithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, TraceIDKey, traceID)
}

// GetTraceID 从 context 获取 trace_id
func GetTraceID(ctx context.Context) string {
	if traceID := ctx.Value(TraceIDKey); traceID != nil {
		return traceID.(string)
	}
	return ""
}

// NewContextWithUserID 创建带 user_id 的 context
func NewContextWithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// GetUserID 从 context 获取 user_id
func GetUserID(ctx context.Context) int64 {
	if userID := ctx.Value(UserIDKey); userID != nil {
		return userID.(int64)
	}
	return 0
}

// Debug 调试日志
func Debug(msg string, fields ...zap.Field) {
	GetLogger().Debug(msg, fields...)
}

// Info 信息日志
func Info(msg string, fields ...zap.Field) {
	GetLogger().Info(msg, fields...)
}

// Warn 警告日志
func Warn(msg string, fields ...zap.Field) {
	GetLogger().Warn(msg, fields...)
}

// Error 错误日志
func Error(msg string, fields ...zap.Field) {
	GetLogger().Error(msg, fields...)
}

// Fatal 致命错误日志
func Fatal(msg string, fields ...zap.Field) {
	GetLogger().Fatal(msg, fields...)
	os.Exit(1)
}

// S 快捷创建 string 字段
func S(key string, val string) zap.Field {
	return zap.String(key, val)
}

// I 快捷创建 int 字段
func I(key string, val int) zap.Field {
	return zap.Int(key, val)
}

// I64 快捷创建 int64 字段
func I64(key string, val int64) zap.Field {
	return zap.Int64(key, val)
}

// F 快捷创建 float64 字段
func F(key string, val float64) zap.Field {
	return zap.Float64(key, val)
}

// B 快捷创建 bool 字段
func B(key string, val bool) zap.Field {
	return zap.Bool(key, val)
}

// Any 快捷创建任意类型字段
func Any(key string, val interface{}) zap.Field {
	return zap.Any(key, val)
}

// Err 快捷创建 error 字段
func Err(err error) zap.Field {
	return zap.Error(err)
}
