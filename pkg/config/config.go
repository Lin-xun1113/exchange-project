// Package config 提供配置加载功能
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
)

// Config 全局配置
type Config struct {
	App      AppConfig      `yaml:"app"`
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	JWT      JWTConfig      `yaml:"jwt"`
	Logging  LoggingConfig  `yaml:"logging"`
	Service  ServiceConfig  `yaml:"service"`
	GRPC     GRPCConfig     `yaml:"grpc"`
}

// GRPCConfig gRPC 服务配置
type GRPCConfig struct {
	UserGRPCAddr     string `yaml:"user_grpc_addr"`
	OrderGRPCAddr    string `yaml:"order_grpc_addr"`
	MatchingGRPCAddr string `yaml:"matching_grpc_addr"`
}

// AppConfig 应用配置
type AppConfig struct {
	Name        string `yaml:"name"`
	Environment string `yaml:"environment"`
	Version     string `yaml:"version"`
}

// ServerConfig 服务配置
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	Database     string `yaml:"database"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
	ConnMaxLife  int    `yaml:"conn_max_life"` // 分钟
}

// DSN 返回 MySQL DSN 连接字符串
func (d *DatabaseConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		d.Username, d.Password, d.Host, d.Port, d.Database)
}

// DSNWithoutDB 返回不带数据库名的 DSN
func (d *DatabaseConfig) DSNWithoutDB() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/?charset=utf8mb4&parseTime=True&loc=Local",
		d.Username, d.Password, d.Host, d.Port)
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

// Addr 返回 Redis 地址
func (r *RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

// JWTConfig JWT 配置
type JWTConfig struct {
	Secret     string `yaml:"secret"`
	ExpireTime int    `yaml:"expire_time"` // 小时
}

// GetExpireDuration 返回过期时间 duration
func (j *JWTConfig) GetExpireDuration() time.Duration {
	return time.Duration(j.ExpireTime) * time.Hour
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level      string `yaml:"level"`
	Directory  string `yaml:"directory"`
	MaxSize    int    `yaml:"max_size"`    // MB
	MaxBackups int    `yaml:"max_backups"` // 保留文件数
	MaxAge     int    `yaml:"max_age"`     // 天
}

// ServiceConfig 服务发现配置
type ServiceConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	Addr    string `yaml:"addr"`
}

// Load 加载配置
func Load(configPath string) (*Config, error) {
	// 优先从环境变量覆盖
	cfg := &Config{}
	
	// 读取环境变量并覆盖
	cfg.App.Name = getEnv("APP_NAME", "exchange-project")
	cfg.App.Environment = getEnv("APP_ENVIRONMENT", "development")
	cfg.App.Version = getEnv("APP_VERSION", "v1.0.0")

	cfg.Database.Host = getEnv("DB_HOST", "localhost")
	cfg.Database.Port = getEnvInt("DB_PORT", 3306)
	cfg.Database.Username = getEnv("DB_USERNAME", "root")
	cfg.Database.Password = getEnv("DB_PASSWORD", "password")
	cfg.Database.Database = getEnv("DB_DATABASE", "exchange")
	cfg.Database.MaxOpenConns = getEnvInt("DB_MAX_OPEN_CONNS", 100)
	cfg.Database.MaxIdleConns = getEnvInt("DB_MAX_IDLE_CONNS", 10)
	cfg.Database.ConnMaxLife = getEnvInt("DB_CONN_MAX_LIFE", 60)

	cfg.Redis.Host = getEnv("REDIS_HOST", "localhost")
	cfg.Redis.Port = getEnvInt("REDIS_PORT", 6379)
	cfg.Redis.Password = getEnv("REDIS_PASSWORD", "")
	cfg.Redis.DB = getEnvInt("REDIS_DB", 0)
	cfg.Redis.PoolSize = getEnvInt("REDIS_POOL_SIZE", 100)

	cfg.JWT.Secret = getEnv("JWT_SECRET", "")
	if cfg.JWT.Secret == "" {
		cfg.JWT.Secret = generateSecureSecret(32)
		logger.Warn("JWT_SECRET not set, generated a random secret at startup. "+
			"This secret will be different on each restart. Set JWT_SECRET environment variable for production.")
	} else if cfg.JWT.Secret == "your-secret-key-change-in-production" ||
		strings.HasPrefix(cfg.JWT.Secret, "your-") {
		logger.Warn("JWT_SECRET appears to be a placeholder value. " +
			"Use a strong, random secret in production.")
	}
	cfg.JWT.ExpireTime = getEnvInt("JWT_EXPIRE_TIME", 24)

	cfg.Logging.Level = getEnv("LOG_LEVEL", "info")
	cfg.Logging.Directory = getEnv("LOG_DIRECTORY", "./logs")
	cfg.Logging.MaxSize = getEnvInt("LOG_MAX_SIZE", 100)
	cfg.Logging.MaxBackups = getEnvInt("LOG_MAX_BACKUPS", 7)
	cfg.Logging.MaxAge = getEnvInt("LOG_MAX_AGE", 30)

	// 服务端口
	cfg.Server.Host = getEnv("SERVER_HOST", "0.0.0.0")
	cfg.Server.Port = getEnvInt("SERVER_PORT", 8080)

	// gRPC 服务地址
	cfg.GRPC.UserGRPCAddr = getEnv("USER_GRPC_ADDR", "localhost:50051")
	cfg.GRPC.OrderGRPCAddr = getEnv("ORDER_GRPC_ADDR", "localhost:50052")
	cfg.GRPC.MatchingGRPCAddr = getEnv("MATCHING_GRPC_ADDR", "localhost:50053")

	logger.Info("configuration loaded",
		logger.S("app_name", cfg.App.Name),
		logger.S("environment", cfg.App.Environment),
		logger.S("db_host", cfg.Database.Host),
		logger.S("redis_host", cfg.Redis.Host),
	)

	return cfg, nil
}

// LoadDefault 加载默认配置（用于测试）
func LoadDefault() *Config {
	return &Config{
		App: AppConfig{
			Name:        "exchange-project",
			Environment: "development",
			Version:     "v1.0.0",
		},
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		Database: DatabaseConfig{
			Host:         "localhost",
			Port:         3306,
			Username:     "root",
			Password:     "password",
			Database:     "exchange",
			MaxOpenConns: 100,
			MaxIdleConns: 10,
			ConnMaxLife:  60,
		},
		Redis: RedisConfig{
			Host:     "localhost",
			Port:     6379,
			Password: "",
			DB:       0,
			PoolSize: 100,
		},
		JWT: JWTConfig{
			Secret:     "dev-secret-key",
			ExpireTime: 24,
		},
		Logging: LoggingConfig{
			Level:     "info",
			Directory: "./logs",
			MaxSize:   100,
			MaxBackups: 7,
			MaxAge:    30,
		},
	}
}

// 辅助函数
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultVal
}

// Address 返回服务地址
func (s *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// LogConfig 返回 zap 日志配置
func (l *LoggingConfig) LogConfig() zap.Config {
	if strings.ToLower(l.Level) == "debug" {
		return zap.NewDevelopmentConfig()
	}
	return zap.NewProductionConfig()
}

// generateSecureSecret generates a cryptographically secure random secret
func generateSecureSecret(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to a less secure method if crypto/rand fails
		for i := range bytes {
			bytes[i] = byte(time.Now().UnixNano() % 256)
			time.Sleep(time.Nanosecond)
		}
	}
	return hex.EncodeToString(bytes)[:length*2]
}
