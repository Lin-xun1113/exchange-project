// Package consumer 提供通知消费者实现
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"go.uber.org/zap"
)

// Event 事件结构
type Event struct {
	Type      string          `json:"type"`
	OrderID   string          `json:"order_id"`
	UserID    int64           `json:"user_id"`
	Symbol    string          `json:"symbol"`
	Data      json.RawMessage `json:"data"`
	Timestamp int64           `json:"timestamp"`
}

// TradeEvent 交易事件
type TradeEvent struct {
	TradeID     string  `json:"trade_id"`
	BuyOrderID  string  `json:"buy_order_id"`
	SellOrderID string  `json:"sell_order_id"`
	BuyUserID   int64   `json:"buy_user_id"`
	SellUserID  int64   `json:"sell_user_id"`
	Price       float64 `json:"price"`
	Quantity    float64 `json:"quantity"`
}

// Consumer 消费者
type Consumer struct {
	redis     *redis.Client
	group     string
	stream    string
	consumer  string
	handlers  map[string]Handler
	ctx       context.Context
	cancel    context.CancelFunc
}

// Handler 事件处理函数
type Handler func(event *Event) error

// NewConsumer 创建消费者
func NewConsumer(redisClient *redis.Client, stream, group, consumer string) *Consumer {
	ctx, cancel := context.WithCancel(context.Background())

	return &Consumer{
		redis:    redisClient,
		stream:   stream,
		group:    group,
		consumer: consumer,
		handlers: make(map[string]Handler),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// RegisterHandler 注册事件处理器
func (c *Consumer) RegisterHandler(eventType string, handler Handler) {
	c.handlers[eventType] = handler
	logger.Info("event handler registered",
		logger.S("type", eventType),
		logger.S("group", c.group),
	)
}

// Start 开始消费
func (c *Consumer) Start() error {
	// 创建消费者组
	err := c.redis.XGroupCreateMkStream(c.ctx, c.stream, c.group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("failed to create consumer group: %w", err)
	}

	// 启动消费协程
	go c.consume()

	logger.Info("consumer started",
		logger.S("stream", c.stream),
		logger.S("group", c.group),
		logger.S("consumer", c.consumer),
	)

	return nil
}

// consume 消费循环
func (c *Consumer) consume() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.readAndProcess()
		}
	}
}

// readAndProcess 读取并处理消息
func (c *Consumer) readAndProcess() {
	// 读取新消息
	streams, err := c.redis.XReadGroup(c.ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.consumer,
		Streams:  []string{c.stream, ">"},
		Count:    10,
		Block:    time.Second,
	}).Result()

	if err != nil {
		if err != redis.Nil {
			logger.Error("failed to read messages", zap.Error(err))
		}
		return
	}

	for _, stream := range streams {
		for _, msg := range stream.Messages {
			if err := c.processMessage(&msg); err != nil {
				logger.Error("failed to process message",
					logger.S("message_id", msg.ID),
					logger.Err(err),
				)
			} else {
				// 确认消息
				c.redis.XAck(c.ctx, c.stream, c.group, msg.ID)
			}
		}
	}
}

// processMessage 处理单条消息
func (c *Consumer) processMessage(msg *redis.XMessage) error {
	// 解析事件
	eventType := msg.Values["type"].(string)
	eventData := msg.Values["data"].(string)

	var event Event
	if err := json.Unmarshal([]byte(eventData), &event); err != nil {
		return fmt.Errorf("failed to unmarshal event: %w", err)
	}

	// 调用处理器
	handler, ok := c.handlers[eventType]
	if !ok {
		logger.Warn("no handler for event type",
			logger.S("type", eventType),
			logger.S("message_id", msg.ID),
		)
		return nil
	}

	if err := handler(&event); err != nil {
		return fmt.Errorf("handler error: %w", err)
	}

	logger.Debug("event processed",
		logger.S("type", eventType),
		logger.S("order_id", event.OrderID),
		logger.S("message_id", msg.ID),
	)

	return nil
}

// Stop 停止消费
func (c *Consumer) Stop() {
	c.cancel()
	logger.Info("consumer stopped",
		logger.S("stream", c.stream),
		logger.S("group", c.group),
	)
}

// StreamPublisher Stream 发布者
type StreamPublisher struct {
	redis  *redis.Client
	stream string
}

// NewStreamPublisher 创建 Stream 发布者
func NewStreamPublisher(redisClient *redis.Client, stream string) *StreamPublisher {
	return &StreamPublisher{
		redis:  redisClient,
		stream: stream,
	}
}

// Publish 发布事件
func (p *StreamPublisher) Publish(ctx context.Context, eventType string, event *Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	id, err := p.redis.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]interface{}{
			"type":      eventType,
			"data":      string(data),
			"timestamp": time.Now().Unix(),
		},
	}).Result()

	if err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	logger.Debug("event published",
		logger.S("type", eventType),
		logger.S("order_id", event.OrderID),
		logger.S("message_id", id),
	)

	return nil
}
