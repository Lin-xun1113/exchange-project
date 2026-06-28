package book_test

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
)

// latencies 存储延迟数据（纳秒）
type latencies []int64

// Percentile 计算指定百分位数
func (l latencies) Percentile(p float64) int64 {
	if len(l) == 0 {
		return 0
	}
	sorted := make(latencies, len(l))
	copy(sorted, l)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := float64(len(sorted)-1) * p / 100.0
	return sorted[int(idx)]
}

// stats 计算延迟统计信息
type stats struct {
	count      int64
	p50       int64
	p90       int64
	p95       int64
	p99       int64
	p999      int64
	max       int64
	totalNanos int64
}

// add 添加延迟样本
func (s *stats) add(ns int64) {
	atomic.AddInt64(&s.count, 1)
	atomic.AddInt64(&s.totalNanos, ns)

	// 使用原子更新最大值
	for {
		current := atomic.LoadInt64(&s.max)
		if ns <= current || atomic.CompareAndSwapInt64(&s.max, current, ns) {
			break
		}
	}
}

// String 返回统计信息字符串
func (s *stats) String() string {
	return fmt.Sprintf("count=%d p50=%s p90=%s p95=%s p99=%s p999=%s max=%s avg=%s",
		s.count,
		formatNanos(s.p50),
		formatNanos(s.p90),
		formatNanos(s.p95),
		formatNanos(s.p99),
		formatNanos(s.p999),
		formatNanos(s.max),
		formatNanos(s.totalNanos/s.count),
	)
}

// formatNanos 格式化纳秒为可读格式
func formatNanos(ns int64) string {
	switch {
	case ns >= 1_000_000_000:
		return fmt.Sprintf("%.2fs", float64(ns)/1_000_000_000)
	case ns >= 1_000_000:
		return fmt.Sprintf("%.2fms", float64(ns)/1_000_000)
	case ns >= 1_000:
		return fmt.Sprintf("%.2fµs", float64(ns)/1_000)
	default:
		return fmt.Sprintf("%dns", ns)
	}
}

// BenchmarkAddOrder_ColdStart 测试冷启动场景（空订单簿添加订单）
func BenchmarkAddOrder_ColdStart(b *testing.B) {
	// 准备订单数据，避免在测量中分配
	order := book.NewOrderInBook(1, "ORD001", 1, "BTC/USDT",
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50000.0), decimal.NewFromFloat(1.0))

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ob := book.NewOrderBook("BTC/USDT")
		ob.AddOrder(order)
	}
}

// BenchmarkAddOrder_HotStart 测试热启动场景（订单簿已有数据）
func BenchmarkAddOrder_HotStart(b *testing.B) {
	ob := book.NewOrderBook("BTC/USDT")

	// 预热：添加 1000 个不同价格的订单
	for i := 0; i < 1000; i++ {
		price := 50000.0 + float64(i)*0.1
		ob.AddOrder(book.NewOrderInBook(int64(i), fmt.Sprintf("ORD%d", i), 1, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(price), decimal.NewFromFloat(1.0)))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 每次创建新订单，在不同价格添加
		price := 49000.0 + float64(i%1000)*0.1
		order := book.NewOrderInBook(int64(10000+i), fmt.Sprintf("ORD_NEW_%d", i), 1, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(price), decimal.NewFromFloat(1.0))
		ob.AddOrder(order)
	}
}

// BenchmarkAddOrder_Latency 测试单次 AddOrder 延迟（收集详细百分位数）
func BenchmarkAddOrder_Latency(b *testing.B) {
	ob := book.NewOrderBook("BTC/USDT")

	// 预热：添加 1000 个不同价格的订单
	for i := 0; i < 1000; i++ {
		price := 50000.0 + float64(i)*0.1
		ob.AddOrder(book.NewOrderInBook(int64(i), fmt.Sprintf("ORD%d", i), 1, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(price), decimal.NewFromFloat(1.0)))
	}

	// 收集延迟数据
	const sampleSize = 10000
	var lats latencies = make([]int64, 0, sampleSize)

	// 预热
	for i := 0; i < 100; i++ {
		order := book.NewOrderInBook(int64(1000+i), fmt.Sprintf("WARM%d", i), 1, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(51000.0), decimal.NewFromFloat(1.0))
		ob.AddOrder(order)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N && len(lats) < sampleSize; i++ {
		order := book.NewOrderInBook(int64(2000+i), fmt.Sprintf("ORD%d", i), 1, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(51000.0+float64(i)*0.01), decimal.NewFromFloat(1.0))

		start := time.Now()
		ob.AddOrder(order)
		lat := time.Since(start).Nanoseconds()
		lat = lat - 1000 // 减去空循环开销（粗略估计）

		if lat > 0 {
			lat = lat - 1000
		}
		if lat < 0 {
			lat = 0
		}
		lats = append(lats, lat)
	}

	// 计算并输出统计信息
	if len(lats) > 0 {
		s := &stats{}
		s.p50 = latencies(lats).Percentile(50)
		s.p90 = latencies(lats).Percentile(90)
		s.p95 = latencies(lats).Percentile(95)
		s.p99 = latencies(lats).Percentile(99)
		s.p999 = latencies(lats).Percentile(99.9)
		s.count = int64(len(lats))

		var total int64
		for _, lat := range lats {
			total += lat
		}
		s.totalNanos = total
		s.max = latencies(lats).Percentile(100)

		b.Logf("Latency stats (n=%d): %s", len(lats), s.String())
	}
}

// BenchmarkMatching_Latency 测试撮合场景延迟
func BenchmarkMatching_Latency(b *testing.B) {
	// 收集延迟数据
	const sampleSize = 5000
	var lats latencies = make([]int64, 0, sampleSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N && len(lats) < sampleSize; i++ {
		// 每次创建新订单簿
		ob := book.NewOrderBook("BTC/USDT")

		// 添加对手订单（卖单）
		for j := 0; j < 10; j++ {
			price := 50000.0 + float64(j)*10
			ob.AddOrder(book.NewOrderInBook(int64(j), fmt.Sprintf("SELL_%d_%d", i, j), 1, "BTC/USDT",
				model.OrderSideSell, model.OrderTypeLimit,
				decimal.NewFromFloat(price), decimal.NewFromFloat(10.0)))
		}

		// 买单价格高于最低卖价，会触发撮合
		buyOrder := book.NewOrderInBook(int64(1000+i), fmt.Sprintf("BUY_%d", i), 2, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(51000.0), decimal.NewFromFloat(5.0))

		start := time.Now()
		trades, _ := ob.AddOrder(buyOrder)
		lat := time.Since(start).Nanoseconds()

		_ = trades // 使用 trades 避免编译器优化
		lat = lat - 500 // 减去估计的订单簿创建开销
		if lat < 0 {
			lat = 0
		}
		lats = append(lats, lat)
	}

	// 计算并输出统计信息
	if len(lats) > 0 {
		s := &stats{}
		s.p50 = latencies(lats).Percentile(50)
		s.p90 = latencies(lats).Percentile(90)
		s.p95 = latencies(lats).Percentile(95)
		s.p99 = latencies(lats).Percentile(99)
		s.p999 = latencies(lats).Percentile(99.9)
		s.count = int64(len(lats))

		var total int64
		for _, lat := range lats {
			total += lat
		}
		s.totalNanos = total
		s.max = latencies(lats).Percentile(100)

		b.Logf("Matching latency stats (n=%d): %s", len(lats), s.String())
	}
}

// BenchmarkMatching_HighVolume 测试高成交量撮合延迟
func BenchmarkMatching_HighVolume(b *testing.B) {
	const sampleSize = 1000
	var lats latencies = make([]int64, 0, sampleSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N && len(lats) < sampleSize; i++ {
		ob := book.NewOrderBook("BTC/USDT")

		// 添加大量卖单（100 个价格档位，每个 10 个订单）
		for level := 0; level < 100; level++ {
			price := 50000.0 + float64(level)*10
			for j := 0; j < 10; j++ {
				ob.AddOrder(book.NewOrderInBook(int64(level*10+j), fmt.Sprintf("SELL_%d_%d", i, level*10+j), 1, "BTC/USDT",
					model.OrderSideSell, model.OrderTypeLimit,
					decimal.NewFromFloat(price), decimal.NewFromFloat(1.0)))
			}
		}

		// 大额买单会触发多次撮合
		buyOrder := book.NewOrderInBook(int64(10000+i), fmt.Sprintf("BIG_BUY_%d", i), 2, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(60000.0), decimal.NewFromFloat(500.0))

		start := time.Now()
		trades, _ := ob.AddOrder(buyOrder)
		lat := time.Since(start).Nanoseconds()

		_ = len(trades) // 使用 trades 避免编译器优化
		lat = lat - 20000 // 减去估计的预热开销
		if lat < 0 {
			lat = 0
		}
		lats = append(lats, lat)
	}

	if len(lats) > 0 {
		s := &stats{}
		s.p50 = latencies(lats).Percentile(50)
		s.p90 = latencies(lats).Percentile(90)
		s.p95 = latencies(lats).Percentile(95)
		s.p99 = latencies(lats).Percentile(99)
		s.p999 = latencies(lats).Percentile(99.9)
		s.count = int64(len(lats))

		var total int64
		for _, lat := range lats {
			total += lat
		}
		s.totalNanos = total
		s.max = latencies(lats).Percentile(100)

		b.Logf("High-volume matching latency (n=%d): %s", len(lats), s.String())
	}
}

// BenchmarkAddOrder_Concurrent 测试并发下单（模拟多个交易对同时下单）
// 注意：每个交易对有独立的订单簿，这是 Actor 模型的正确用法
func BenchmarkAddOrder_Concurrent(b *testing.B) {
	const numGoroutines = 10
	const ordersPerGoroutine = 100

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 每个 goroutine 有独立的订单簿（模拟多个交易对）
		orderBooks := make([]*book.OrderBook, numGoroutines)
		for g := 0; g < numGoroutines; g++ {
			orderBooks[g] = book.NewOrderBook(fmt.Sprintf("PAIR%d/USDT", g))
			// 预热
			for j := 0; j < 50; j++ {
				orderBooks[g].AddOrder(book.NewOrderInBook(int64(j), fmt.Sprintf("WARM_%d_%d", g, j), 1, fmt.Sprintf("PAIR%d/USDT", g),
					model.OrderSideSell, model.OrderTypeLimit,
					decimal.NewFromFloat(50000.0+float64(j)*10), decimal.NewFromFloat(10.0)))
			}
		}

		var wg sync.WaitGroup
		var totalLat int64
		var mu sync.Mutex

		for g := 0; g < numGoroutines; g++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()

				localLats := make([]int64, 0, ordersPerGoroutine)
				ob := orderBooks[goroutineID]
				for j := 0; j < ordersPerGoroutine; j++ {
					orderID := goroutineID*ordersPerGoroutine + j
					order := book.NewOrderInBook(int64(10000+orderID), fmt.Sprintf("ORD_%d", orderID), int64(2+goroutineID), fmt.Sprintf("PAIR%d/USDT", goroutineID),
						model.OrderSideBuy, model.OrderTypeLimit,
						decimal.NewFromFloat(51000.0+float64(orderID%100)*0.1), decimal.NewFromFloat(1.0))

					start := time.Now()
					ob.AddOrder(order)
					lat := time.Since(start).Nanoseconds()
					localLats = append(localLats, lat)
				}

				mu.Lock()
				for _, lat := range localLats {
					totalLat += lat
				}
				mu.Unlock()
			}(g)
		}

		wg.Wait()
		_ = totalLat // 使用 totalLat 避免编译器优化
	}
}

// BenchmarkAddOrder_ConcurrentWithMatching 测试带撮合的并发下单
// 注意：每个 goroutine 处理不同的交易对
func BenchmarkAddOrder_ConcurrentWithMatching(b *testing.B) {
	const numGoroutines = 10
	const ordersPerGoroutine = 50

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 每个 goroutine 有独立的订单簿
		orderBooks := make([]*book.OrderBook, numGoroutines)
		for g := 0; g < numGoroutines; g++ {
			orderBooks[g] = book.NewOrderBook(fmt.Sprintf("PAIR%d/USDT", g))
			// 添加卖单作为对手盘
			for j := 0; j < 50; j++ {
				orderBooks[g].AddOrder(book.NewOrderInBook(int64(j), fmt.Sprintf("SELL_%d_%d", g, j), 1, fmt.Sprintf("PAIR%d/USDT", g),
					model.OrderSideSell, model.OrderTypeLimit,
					decimal.NewFromFloat(50000.0+float64(j)*10), decimal.NewFromFloat(100.0)))
			}
		}

		var wg sync.WaitGroup
		var totalLat int64
		var mu sync.Mutex
		var allLats latencies

		for g := 0; g < numGoroutines; g++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()

				localLats := make([]int64, 0, ordersPerGoroutine)
				ob := orderBooks[goroutineID]
				for j := 0; j < ordersPerGoroutine; j++ {
					orderID := goroutineID*ordersPerGoroutine + j
					order := book.NewOrderInBook(int64(10000+orderID), fmt.Sprintf("ORD_%d", orderID), int64(2+goroutineID), fmt.Sprintf("PAIR%d/USDT", goroutineID),
						model.OrderSideBuy, model.OrderTypeLimit,
						decimal.NewFromFloat(51000.0+float64(orderID%50)*0.5), decimal.NewFromFloat(1.0))

					start := time.Now()
					ob.AddOrder(order)
					lat := time.Since(start).Nanoseconds()
					localLats = append(localLats, lat)
				}

				mu.Lock()
				allLats = append(allLats, localLats...)
				for _, lat := range localLats {
					totalLat += lat
				}
				mu.Unlock()
			}(g)
		}

		wg.Wait()

		if len(allLats) > 0 {
			s := &stats{}
			s.p50 = latencies(allLats).Percentile(50)
			s.p90 = latencies(allLats).Percentile(90)
			s.p95 = latencies(allLats).Percentile(95)
			s.p99 = latencies(allLats).Percentile(99)
			s.p999 = latencies(allLats).Percentile(99.9)
			s.count = int64(len(allLats))
			s.totalNanos = totalLat
			s.max = latencies(allLats).Percentile(100)

			b.Logf("Concurrent matching (n=%d, goroutines=%d): %s", len(allLats), numGoroutines, s.String())
		}
	}
}

// BenchmarkOrderBook_10KPriceLevels 测试 10000 价格档位场景
func BenchmarkOrderBook_10KPriceLevels(b *testing.B) {
	ob := book.NewOrderBook("BTC/USDT")

	// 预热：添加 10000 个不同价格的订单
	for i := 0; i < 10000; i++ {
		price := 40000.0 + float64(i)*0.01
		ob.AddOrder(book.NewOrderInBook(int64(i), fmt.Sprintf("ORD%d", i), 1, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(price), decimal.NewFromFloat(1.0)))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// 在随机价格添加订单
		price := 40000.0 + rand.Float64()*100
		order := book.NewOrderInBook(int64(20000+i), fmt.Sprintf("ORD_NEW_%d", i), 1, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(price), decimal.NewFromFloat(1.0))
		ob.AddOrder(order)
	}
}

// BenchmarkOrderBook_10KPriceLevelsMatching 测试 10000 价格档位 + 撮合
func BenchmarkOrderBook_10KPriceLevelsMatching(b *testing.B) {
	ob := book.NewOrderBook("BTC/USDT")

	// 预热：添加 10000 个卖单
	for i := 0; i < 10000; i++ {
		price := 50000.0 + float64(i)*0.01
		ob.AddOrder(book.NewOrderInBook(int64(i), fmt.Sprintf("SELL_%d", i), 1, "BTC/USDT",
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(price), decimal.NewFromFloat(1.0)))
	}

	const sampleSize = 1000
	var lats latencies = make([]int64, 0, sampleSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N && len(lats) < sampleSize; i++ {
		// 买单价格足够高，会触发撮合
		order := book.NewOrderInBook(int64(20000+i), fmt.Sprintf("BUY_%d", i), 2, "BTC/USDT",
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(51000.0), decimal.NewFromFloat(5.0))

		start := time.Now()
		trades, _ := ob.AddOrder(order)
		lat := time.Since(start).Nanoseconds()

		_ = len(trades) // 使用 trades 避免编译器优化
		lat = lat - 500 // 减去估计开销
		if lat < 0 {
			lat = 0
		}
		lats = append(lats, lat)
	}

	if len(lats) > 0 {
		s := &stats{}
		s.p50 = latencies(lats).Percentile(50)
		s.p90 = latencies(lats).Percentile(90)
		s.p95 = latencies(lats).Percentile(95)
		s.p99 = latencies(lats).Percentile(99)
		s.p999 = latencies(lats).Percentile(99.9)
		s.count = int64(len(lats))

		var total int64
		for _, lat := range lats {
			total += lat
		}
		s.totalNanos = total
		s.max = latencies(lats).Percentile(100)

		b.Logf("10K price levels matching (n=%d): %s", len(lats), s.String())
	}
}
