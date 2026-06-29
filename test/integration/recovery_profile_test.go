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

// BenchmarkRecoveryProfile 专门测试恢复性能并分析瓶颈
func BenchmarkRecoveryProfile(b *testing.B) {
	symbol := "BTC/USDT"
	orderCount := 50

	walDir := b.TempDir()
	snapshotDir := b.TempDir()

	cfg := engine.Config{
		WALDir:              walDir,
		SnapshotDir:         snapshotDir,
		MaxTradesPerSnapshot: 10000,
		WALSyncMode:         wal.SyncNone,
	}

	// Phase 1: 创建订单
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
	_ = m.TakeSnapshot(symbol, snapshotDir)
	m.Shutdown()
	walManager.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			ActorTimeout: 30 * time.Second,
			WALSyncMode: wal.SyncNone,
		}
		start := time.Now()
		m := engine.NewMatcher(cfg)
		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		recoverStart := time.Now()
		_ = m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		recoverElapsed := time.Since(recoverStart)

		totalElapsed := time.Since(start)
		b.ReportMetric(recoverElapsed.Seconds()*1000, "recover_only_ms")
		b.ReportMetric(totalElapsed.Seconds()*1000, "total_time_ms")

		m.Shutdown()
		walManager.Close()
	}
}
