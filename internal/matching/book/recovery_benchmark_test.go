package book

import (
	"fmt"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
)

// BenchmarkBatchVsSingleRecovery compares O(n log n) batch vs O(n²) single insertion
func BenchmarkBatchVsSingleRecovery(b *testing.B) {
	testCases := []int{100, 500, 1000, 5000, 10000, 100000}

	for _, n := range testCases {
		// Prepare test orders
		orders := make([]*OrderInBook, n)
		for i := 0; i < n; i++ {
			price := decimal.NewFromFloat(100.0 + float64(i%100)*0.01)
			qty := decimal.NewFromFloat(1.0)
			side := model.OrderSideBuy
			if i%2 == 1 {
				side = model.OrderSideSell
			}
			orders[i] = NewOrderInBook(
				int64(i),
				fmt.Sprintf("ORDER-%d", i),
				int64(i%10),
				"BTC/USDT",
				side,
				model.OrderTypeLimit,
				price,
				qty,
			)
		}

		b.Run(fmt.Sprintf("SingleInsertion_O(n²)_%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ob := NewOrderBook("BTC/USDT")
				for _, o := range orders {
					ob.AddOrderForRecovery(o)
				}
			}
		})

		b.Run(fmt.Sprintf("BatchInsertion_O(nlogn)_%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ob := NewOrderBook("BTC/USDT")
				ob.BatchAddOrdersForRecovery(orders)
			}
		})
	}
}

// BenchmarkRecoveryRealistic simulates realistic snapshot recovery
func BenchmarkRecoveryRealistic(b *testing.B) {
	// Simulate realistic order distribution: 60% buy, 40% sell
	// Price range: 100-110 for buys (lower), 100-110 for sells (higher)
	// Multiple orders at same price level

	orderCounts := []int{100, 500, 1000}

	for _, n := range orderCounts {
		orders := make([]*OrderInBook, n)
		for i := 0; i < n; i++ {
			side := model.OrderSideBuy
			if i%10 >= 6 {
				side = model.OrderSideSell
			}
			priceVal := 100.0
			if side == model.OrderSideBuy {
				priceVal = 100.0 + float64(i%20)*0.1 // 100-102
			} else {
				priceVal = 100.5 + float64(i%20)*0.1 // 100.5-102.5
			}
			price := decimal.NewFromFloat(priceVal)
			orders[i] = NewOrderInBook(
				int64(i),
				fmt.Sprintf("ORDER-%d", i),
				int64(i%10),
				"BTC/USDT",
				side,
				model.OrderTypeLimit,
				price,
				decimal.NewFromFloat(1.0),
			)
		}

		b.Run(fmt.Sprintf("Orders_%d", n), func(b *testing.B) {
			b.ResetTimer()
			b.ReportAllocs()
			start := time.Now()
			for i := 0; i < b.N; i++ {
				ob := NewOrderBook("BTC/USDT")
				ob.BatchAddOrdersForRecovery(orders)
			}
			elapsed := time.Since(start)
			b.ReportMetric(float64(n*b.N)/elapsed.Seconds(), "orders/sec")
		})
	}
}
