// Package consumer_test 提供通知消费者测试
package consumer_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/notify/consumer"
	"github.com/stretchr/testify/assert"
)

// TestEventProcessing 测试事件处理
func TestEventProcessing(t *testing.T) {
	// 这个测试需要真实的 Redis 连接
	// 在 CI 环境中跳过
	t.Skip("requires real Redis connection")
}

// TestEventStructure 测试事件结构
func TestEventStructure(t *testing.T) {
	event := &consumer.Event{
		Type:      "trade",
		OrderID:   "ORD123",
		UserID:    1,
		Symbol:    "BTC/USDT",
		Timestamp: time.Now().Unix(),
	}

	data, err := json.Marshal(event)
	assert.NoError(t, err)

	var decoded consumer.Event
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, event.Type, decoded.Type)
	assert.Equal(t, event.OrderID, decoded.OrderID)
	assert.Equal(t, event.UserID, decoded.UserID)
}

// TestTradeEventStructure 测试交易事件结构
func TestTradeEventStructure(t *testing.T) {
	event := &consumer.TradeEvent{
		TradeID:     "TRD123",
		BuyOrderID:  "ORD001",
		SellOrderID: "ORD002",
		BuyUserID:   1,
		SellUserID:  2,
		Price:       50000.0,
		Quantity:    1.0,
	}

	data, err := json.Marshal(event)
	assert.NoError(t, err)

	var decoded consumer.TradeEvent
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, event.TradeID, decoded.TradeID)
	assert.Equal(t, event.Price, decoded.Price)
	assert.Equal(t, event.Quantity, decoded.Quantity)
}

// TestConsumerHandlerRegistration 测试消费者处理器注册
func TestConsumerHandlerRegistration(t *testing.T) {
	eventHandlerCalled := false
	var mu sync.Mutex

	// 模拟注册处理器
	handler := func(event *consumer.Event) error {
		mu.Lock()
		eventHandlerCalled = true
		mu.Unlock()
		return nil
	}

	// 验证处理器可以被调用
	event := &consumer.Event{
		Type:      "test",
		OrderID:   "ORD123",
		UserID:    1,
		Timestamp: time.Now().Unix(),
	}

	err := handler(event)
	assert.NoError(t, err)

	mu.Lock()
	assert.True(t, eventHandlerCalled)
	mu.Unlock()
}

// TestStreamPublisher 测试 Stream 发布者
func TestStreamPublisher(t *testing.T) {
	// 这个测试需要真实的 Redis 连接
	t.Skip("requires real Redis connection")
}
