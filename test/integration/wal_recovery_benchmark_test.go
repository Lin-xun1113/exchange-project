package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
)

// BenchmarkWALRecovery_Micro 测试微规模（20条订单）的恢复性能
func BenchmarkWALRecovery_Micro(b *testing.B) {
	benchmarkRecovery(b, "BTC/USDT", 20, true)
}

// BenchmarkWALRecovery_Small 测试小规模（50条订单）的恢复性能
func BenchmarkWALRecovery_Small(b *testing.B) {
	benchmarkRecovery(b, "BTC/USDT", 50, true)
}

// BenchmarkWALRecovery_NoSnapshot 测试无快照时的纯 WAL 重放性能
func BenchmarkWALRecovery_NoSnapshot(b *testing.B) {
	benchmarkRecovery(b, "ETH/USDT", 30, false)
}

// benchmarkRecovery 是恢复性能测试的通用辅助函数
func benchmarkRecovery(b *testing.B, symbol string, orderCount int, withSnapshot bool) {
	walDir := b.TempDir()
	snapshotDir := b.TempDir()

	// 禁用自动快照，使用 ManualSnapshotInterval
	cfg := engine.Config{
		WALDir:              walDir,
		SnapshotDir:         snapshotDir,
		MaxTradesPerSnapshot: 10000, // 禁用自动触发
		WALSyncMode:         wal.SyncNone,
	}

	// Phase 1: 创建订单
	{
		m := engine.NewMatcher(cfg)
		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)
		m.SetSnapshotDir(snapshotDir)

		ctx := context.Background()

		for i := 0; i < orderCount; i++ {
			side := model.OrderSideBuy
			if i%2 == 0 {
				side = model.OrderSideSell
			}
			_, _ = m.SubmitOrder(ctx, fmt.Sprintf("ORD_%d", i), int64(i%10), symbol,
				side, model.OrderTypeLimit,
				decimal.NewFromFloat(50000.0+float64(i%50)*10), decimal.NewFromFloat(1.0))
		}

		time.Sleep(100 * time.Millisecond)

		if withSnapshot {
			_ = m.TakeSnapshot(symbol, snapshotDir)
		}

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: 测量恢复时间
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		}
		m := engine.NewMatcher(cfg)
		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		start := time.Now()
		_ = m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		elapsed := time.Since(start)

		b.ReportMetric(elapsed.Seconds()*1000, "recovery_time_ms")

		m.Shutdown()
		walManager.Close()
	}

	snapshotStr := "with snapshot"
	if !withSnapshot {
		snapshotStr = "no snapshot"
	}
	b.Logf("%s: %d orders, %s", symbol, orderCount, snapshotStr)
}
