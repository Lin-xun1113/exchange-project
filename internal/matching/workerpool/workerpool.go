// Package workerpool 提供 Worker Pool 实现
package workerpool

import (
	"context"
	"sync"

	"github.com/linxun2025/exchange-project/pkg/logger"
)

// Task 任务函数类型
type Task func() error

// Config Worker Pool 配置
type Config struct {
	Workers   int    // Worker 数量
	QueueSize int    // 任务队列大小
	Name      string // Pool 名称
}

// WorkerPool Worker Pool 实现
type WorkerPool struct {
	name      string
	workers   int
	taskQueue chan Task
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.RWMutex
	running   bool
	metrics   *Metrics
}

// Metrics 性能指标
type Metrics struct {
	mu              sync.RWMutex
	SubmittedTasks  int64
	CompletedTasks  int64
	FailedTasks    int64
	RunningTasks   int64
	QueuedTasks    int64
}

// New 创建新的 Worker Pool
func New(cfg Config) *WorkerPool {
	if cfg.Workers <= 0 {
		cfg.Workers = 10
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1000
	}
	if cfg.Name == "" {
		cfg.Name = "workerpool"
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &WorkerPool{
		name:      cfg.Name,
		workers:   cfg.Workers,
		taskQueue: make(chan Task, cfg.QueueSize),
		ctx:       ctx,
		cancel:    cancel,
		metrics:   &Metrics{},
	}

	pool.start()

	return pool
}

// start 启动 Worker Pool
func (wp *WorkerPool) start() {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if wp.running {
		return
	}
	wp.running = true

	// 启动 workers
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}

	logger.Info("worker pool started",
		logger.S("name", wp.name),
		logger.I("workers", wp.workers),
		logger.I("queue_size", wp.workers*2),
	)
}

// worker Worker 协程
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	logger.Debug("worker started", logger.S("name", wp.name), logger.I("worker_id", id))

	for {
		select {
		case <-wp.ctx.Done():
			logger.Debug("worker stopped", logger.S("name", wp.name), logger.I("worker_id", id))
			return
		case task, ok := <-wp.taskQueue:
			if !ok {
				logger.Debug("worker queue closed", logger.S("name", wp.name), logger.I("worker_id", id))
				return
			}

			// 执行任务
			wp.metrics.mu.Lock()
			wp.metrics.RunningTasks++
			wp.metrics.mu.Unlock()

			err := task()

			wp.metrics.mu.Lock()
			wp.metrics.RunningTasks--
			if err != nil {
				wp.metrics.FailedTasks++
				logger.Error("task execution failed",
					logger.S("name", wp.name),
					logger.I("worker_id", id),
					logger.Err(err),
				)
			} else {
				wp.metrics.CompletedTasks++
			}
			wp.metrics.mu.Unlock()
		}
	}
}

// Submit 提交任务
func (wp *WorkerPool) Submit(task Task) error {
	select {
	case <-wp.ctx.Done():
		return context.Canceled
	case wp.taskQueue <- task:
		wp.metrics.mu.Lock()
		wp.metrics.SubmittedTasks++
		wp.metrics.QueuedTasks++
		wp.metrics.mu.Unlock()
		return nil
	default:
		// 队列已满
		wp.metrics.mu.Lock()
		wp.metrics.QueuedTasks--
		wp.metrics.mu.Unlock()

		logger.Warn("task queue is full",
			logger.S("name", wp.name),
			logger.I("queue_size", len(wp.taskQueue)),
		)
		return ErrQueueFull
	}
}

// SubmitWithContext 提交带上下文的任务
func (wp *WorkerPool) SubmitWithContext(ctx context.Context, task Task) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case wp.taskQueue <- task:
		wp.metrics.mu.Lock()
		wp.metrics.SubmittedTasks++
		wp.metrics.QueuedTasks++
		wp.metrics.mu.Unlock()
		return nil
	default:
		wp.metrics.mu.Lock()
		wp.metrics.QueuedTasks--
		wp.metrics.mu.Unlock()

		logger.Warn("task queue is full",
			logger.S("name", wp.name),
			logger.I("queue_size", len(wp.taskQueue)),
		)
		return ErrQueueFull
	}
}

// Shutdown 优雅关闭，等待所有任务完成
func (wp *WorkerPool) Shutdown() {
	logger.Info("worker pool shutting down", logger.S("name", wp.name))

	wp.mu.Lock()
	if !wp.running {
		wp.mu.Unlock()
		return
	}
	wp.running = false
	wp.mu.Unlock()

	// 取消上下文
	wp.cancel()

	// 关闭任务队列
	close(wp.taskQueue)

	// 等待所有 worker 结束
	wp.wg.Wait()

	logger.Info("worker pool stopped", logger.S("name", wp.name))
}

// ShutdownNow 立即关闭
func (wp *WorkerPool) ShutdownNow() {
	logger.Info("worker pool shutting down immediately", logger.S("name", wp.name))

	wp.mu.Lock()
	if !wp.running {
		wp.mu.Unlock()
		return
	}
	wp.running = false
	wp.mu.Unlock()

	// 取消上下文
	wp.cancel()

	// 关闭任务队列
	close(wp.taskQueue)

	logger.Info("worker pool stopped immediately", logger.S("name", wp.name))
}

// GetMetrics 返回性能指标
func (wp *WorkerPool) GetMetrics() Metrics {
	wp.metrics.mu.RLock()
	defer wp.metrics.mu.RUnlock()
	return Metrics{
		SubmittedTasks: wp.metrics.SubmittedTasks,
		CompletedTasks: wp.metrics.CompletedTasks,
		FailedTasks:   wp.metrics.FailedTasks,
		RunningTasks:  wp.metrics.RunningTasks,
		QueuedTasks:   int64(len(wp.taskQueue)),
	}
}

// IsRunning 检查是否运行中
func (wp *WorkerPool) IsRunning() bool {
	wp.mu.RLock()
	defer wp.mu.RUnlock()
	return wp.running
}

// QueueLength 返回队列长度
func (wp *WorkerPool) QueueLength() int {
	return len(wp.taskQueue)
}

// RunningWorkers 返回运行中的 worker 数量
func (wp *WorkerPool) RunningWorkers() int {
	wp.mu.RLock()
	defer wp.mu.RUnlock()
	if wp.running {
		return wp.workers
	}
	return 0
}

// 错误定义
var ErrQueueFull = &WorkerPoolError{Message: "task queue is full"}

// WorkerPoolError Worker Pool 错误
type WorkerPoolError struct {
	Message string
}

func (e *WorkerPoolError) Error() string {
	return e.Message
}
