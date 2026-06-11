// Package workerpool_test 提供 Worker Pool 测试
package workerpool_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/workerpool"
	"github.com/stretchr/testify/assert"
)

// TestWorkerPool_Basic 测试 Worker Pool 基本功能
func TestWorkerPool_Basic(t *testing.T) {
	wp := workerpool.New(workerpool.Config{
		Workers:   3,
		QueueSize: 10,
		Name:      "test",
	})
	defer wp.Shutdown()

	var count int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		err := wp.Submit(func() error {
			atomic.AddInt32(&count, 1)
			wg.Done()
			return nil
		})
		assert.NoError(t, err)
	}

	wg.Wait()
	assert.Equal(t, int32(5), count)
}

// TestWorkerPool_ContextCancellation 测试上下文取消
func TestWorkerPool_ContextCancellation(t *testing.T) {
	wp := workerpool.New(workerpool.Config{
		Workers:   2,
		QueueSize: 5,
		Name:      "test",
	})
	defer wp.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())

	// 取消上下文后再提交，任务应该被拒绝
	cancel()
	err := wp.SubmitWithContext(ctx, func() error {
		return nil
	})
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

// TestWorkerPool_Shutdown 测试优雅关闭
func TestWorkerPool_Shutdown(t *testing.T) {
	wp := workerpool.New(workerpool.Config{
		Workers:   2,
		QueueSize: 5,
		Name:      "test",
	})

	var count int32

	// 提交任务
	for i := 0; i < 3; i++ {
		wp.Submit(func() error {
			atomic.AddInt32(&count, 1)
			time.Sleep(50 * time.Millisecond)
			return nil
		})
	}

	// 等待任务执行
	time.Sleep(200 * time.Millisecond)

	// 关闭
	wp.Shutdown()

	assert.True(t, wp.QueueLength() == 0 || count == 3)
}

// TestWorkerPool_QueueFull 测试队列满的情况
func TestWorkerPool_QueueFull(t *testing.T) {
	wp := workerpool.New(workerpool.Config{
		Workers:   1,
		QueueSize: 2,
		Name:      "test",
	})
	defer wp.Shutdown()

	// 使用慢任务填满队列
	slowTask := func() error {
		time.Sleep(1 * time.Second)
		return nil
	}

	// 填满队列
	wp.Submit(slowTask)
	wp.Submit(slowTask)

	// 队列已满，新任务应该被拒绝
	err := wp.Submit(slowTask)
	assert.Error(t, err)
}

// TestWorkerPool_Metrics 测试性能指标
func TestWorkerPool_Metrics(t *testing.T) {
	wp := workerpool.New(workerpool.Config{
		Workers:   2,
		QueueSize: 10,
		Name:      "test",
	})
	defer wp.Shutdown()

	for i := 0; i < 5; i++ {
		wp.Submit(func() error {
			return nil
		})
	}

	time.Sleep(100 * time.Millisecond)

	metrics := wp.GetMetrics()
	assert.GreaterOrEqual(t, metrics.SubmittedTasks, int64(5))
	assert.GreaterOrEqual(t, metrics.CompletedTasks, int64(5))
}

// TestWorkerPool_ConcurrentSubmit 测试并发提交
func TestWorkerPool_ConcurrentSubmit(t *testing.T) {
	wp := workerpool.New(workerpool.Config{
		Workers:   5,
		QueueSize: 100,
		Name:      "test",
	})
	defer wp.Shutdown()

	var count int32
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wp.Submit(func() error {
				atomic.AddInt32(&count, 1)
				return nil
			})
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(100), count)
}
