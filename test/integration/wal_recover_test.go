// Package integration provides integration tests for WAL recovery
package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForNotifications waits for async notifications to be processed
func waitForNotifications() {
	time.Sleep(100 * time.Millisecond)
}

func TestWALRecovery_BasicRecovery(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create matcher and submit orders
	cfg := engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)

	ctx := context.Background()

	// Submit orders that create trades
	// Add sell order first
	_, err := m.SubmitOrder(ctx, "ORD001", 1, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(10.0))
	require.NoError(t, err)

	// Add buy order that matches
	result, err := m.SubmitOrder(ctx, "ORD002", 2, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50100), decimal.NewFromFloat(5.0))
	require.NoError(t, err)
	require.Len(t, result.Trades, 1)

	// Verify trades were written to WAL
	w, err := walManager.GetWAL(symbol)
	require.NoError(t, err)
	lsn := w.LastLSN()
	t.Logf("WAL LSN after trades: %d", lsn)
	assert.Greater(t, lsn, uint64(0))

	m.Shutdown()
	walManager.Close()

	// Phase 2: Create new matcher and recover
	cfg2 := engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	}
	m2 := engine.NewMatcher(cfg2)
	require.NotNil(t, m2)

	walManager2 := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m2.SetWALManager(walManager2)

	err = m2.Recover(engine.RecoveryConfig{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
	})
	require.NoError(t, err)

	// Verify order book state is recovered
	bids, asks := m2.GetOrderBook(symbol, 10)
	// Should have remaining sell order
	assert.Len(t, asks, 1)
	assert.Equal(t, "ORD001", asks[0].OrderID)

	// Should have no bids (fully matched)
	assert.Len(t, bids, 0)

	m2.Shutdown()
	walManager2.Close()
}

func TestWALRecovery_WithSnapshot(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "ETH/USDT"

	// Phase 1: Create initial state
	cfg := engine.Config{
		WALDir:              walDir,
		SnapshotDir:         snapshotDir,
		MaxTradesPerSnapshot: 5, // Trigger snapshot every 5 trades
		WALSyncMode:         wal.SyncNone,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)
	m.SetSnapshotDir(snapshotDir)

	ctx := context.Background()

	// Create multiple trades
	for i := 0; i < 3; i++ {
		// Add sell order
		_, err := m.SubmitOrder(ctx, "SELL"+string(rune('A'+i)), 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(3000), decimal.NewFromFloat(1.0))
		require.NoError(t, err)

		// Add buy order
		_, err = m.SubmitOrder(ctx, "BUY"+string(rune('A'+i)), 2, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(3100), decimal.NewFromFloat(1.0))
		require.NoError(t, err)
	}

	// Manually trigger snapshot
	err := m.TakeSnapshot(symbol, snapshotDir)
	require.NoError(t, err)

	m.Shutdown()
	walManager.Close()

	// Phase 2: Recover with snapshot
	cfg2 := engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	}
	m2 := engine.NewMatcher(cfg2)
	require.NotNil(t, m2)

	walManager2 := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m2.SetWALManager(walManager2)

	err = m2.Recover(engine.RecoveryConfig{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
	})
	require.NoError(t, err)

	// Verify state is recovered (all orders should be matched)
	bids, asks := m2.GetOrderBook(symbol, 10)
	assert.Len(t, bids, 0)
	assert.Len(t, asks, 0)

	m2.Shutdown()
	walManager2.Close()
}

func TestWALRecovery_PartialWALAfterSnapshot(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create initial state with snapshot
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)
		m.SetSnapshotDir(snapshotDir)

		ctx := context.Background()

		// Create a few trades
		for i := 0; i < 2; i++ {
			_, _ = m.SubmitOrder(ctx, "SELL"+string(rune('A'+i)), 1, symbol,
				model.OrderSideSell, model.OrderTypeLimit,
				decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
			_, _ = m.SubmitOrder(ctx, "BUY"+string(rune('A'+i)), 2, symbol,
				model.OrderSideBuy, model.OrderTypeLimit,
				decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))
		}

		// Take snapshot
		_ = m.TakeSnapshot(symbol, snapshotDir)

		// Create more trades after snapshot (matching orders)
		_, _ = m.SubmitOrder(ctx, "SELL_NEW", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
		_, _ = m.SubmitOrder(ctx, "BUY_NEW", 2, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: Recover - should use snapshot + replay WAL entries after snapshot
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// All orders should be matched
		bids, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, bids, 0)
		assert.Len(t, asks, 0)

		m.Shutdown()
		walManager.Close()
	}
}

func TestWALRecovery_CancelOrderRecovery(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create orders and cancel one
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		ctx := context.Background()

		// Submit orders
		_, _ = m.SubmitOrder(ctx, "ORD001", 1, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
		_, _ = m.SubmitOrder(ctx, "ORD002", 2, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

		// Cancel one order
		success := m.CancelOrder(symbol, "ORD001")
		assert.True(t, success)

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: Recover
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// Only ORD002 should be in the book
		bids, _ := m.GetOrderBook(symbol, 10)
		assert.Len(t, bids, 1)
		assert.Equal(t, "ORD002", bids[0].OrderID)

		// ORD001 should not exist
		order := m.GetOrder(symbol, "ORD001")
		assert.Nil(t, order)

		m.Shutdown()
		walManager.Close()
	}
}

func TestWALRecovery_ConcurrentOperations(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"
	const numGoroutines = 10

	// Phase 1: Concurrent submissions
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)
		m.SetSnapshotDir(snapshotDir)

		ctx := context.Background()
		var wg sync.WaitGroup

		// Add initial liquidity
		_, _ = m.SubmitOrder(ctx, "LIQUIDITY", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(100.0))

		// Concurrent buy orders
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, _ = m.SubmitOrder(ctx, "BUY_"+string(rune('A'+idx)), int64(idx), symbol,
					model.OrderSideBuy, model.OrderTypeLimit,
					decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))
			}(i)
		}

		wg.Wait()

		// Take snapshot
		_ = m.TakeSnapshot(symbol, snapshotDir)

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: Recover
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// Check recovered state
		bids, asks := m.GetOrderBook(symbol, 100)

		// All buy orders should have been filled
		assert.Len(t, bids, 0)

		// Remaining sell order
		assert.Len(t, asks, 1)
		assert.Equal(t, "LIQUIDITY", asks[0].OrderID)

		m.Shutdown()
		walManager.Close()
	}
}

func TestWALRecovery_NoSnapshotNoWAL(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Create matcher with no prior state
	m := engine.NewMatcher(engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	})

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)

	// Recovery on empty directories should not error
	err := m.Recover(engine.RecoveryConfig{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
	})
	require.NoError(t, err)

	// Order book should be empty
	bids, asks := m.GetOrderBook("BTC/USDT", 10)
	assert.Len(t, bids, 0)
	assert.Len(t, asks, 0)

	m.Shutdown()
	walManager.Close()
}

func TestWALRecovery_ManualSnapshotAndRecovery(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create orders, take snapshot, add more orders
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)
		m.SetSnapshotDir(snapshotDir)

		ctx := context.Background()

		// Create some orders that will match
		_, _ = m.SubmitOrder(ctx, "ORD001", 1, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
		_, _ = m.SubmitOrder(ctx, "ORD002", 2, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

		// Allow async notifications to process
		waitForNotifications()

		// Take snapshot (both orders matched, book should be empty)
		err := m.TakeSnapshot(symbol, snapshotDir)
		require.NoError(t, err)

		// Add more matching orders after snapshot
		_, _ = m.SubmitOrder(ctx, "ORD003", 3, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
		_, _ = m.SubmitOrder(ctx, "ORD004", 4, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

		// Allow async notifications to process
		waitForNotifications()

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: Recover
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// All orders should be matched (snapshot was empty, WAL replay matched ORD003+ORD004)
		bids, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, bids, 0, "all orders should be matched after recovery")
		assert.Len(t, asks, 0, "all orders should be matched after recovery")

		m.Shutdown()
		walManager.Close()
	}
}

func TestSnapshotAndWAL_EndToEnd(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Simulate a trading session with snapshots
	session := func(name string, orders []struct {
		id       string
		userID   int64
		side     model.OrderSide
		price    float64
		qty      float64
	}) {
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		}
		m := engine.NewMatcher(cfg)

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)
		m.SetSnapshotDir(snapshotDir)

		ctx := context.Background()

		for _, o := range orders {
			_, _ = m.SubmitOrder(ctx, o.id, o.userID, symbol,
				o.side, model.OrderTypeLimit,
				decimal.NewFromFloat(o.price), decimal.NewFromFloat(o.qty))
		}

		// Allow async notifications to process
		waitForNotifications()

		// Take snapshot
		_ = m.TakeSnapshot(symbol, snapshotDir)

		m.Shutdown()
		walManager.Close()
	}

	// Session 1: Create initial state (orders match with each other)
	session("session1", []struct {
		id       string
		userID   int64
		side     model.OrderSide
		price    float64
		qty      float64
	}{
		{"S1_ORD1", 1, model.OrderSideSell, 50000, 5.0},
		{"S1_ORD2", 2, model.OrderSideBuy, 50000, 5.0}, // Matches S1_ORD1
	})

	// Session 2: Add more orders that match
	session("session2", []struct {
		id       string
		userID   int64
		side     model.OrderSide
		price    float64
		qty      float64
	}{
		{"S2_ORD1", 3, model.OrderSideSell, 50100, 3.0},
		{"S2_ORD2", 4, model.OrderSideBuy, 50100, 3.0}, // Matches S2_ORD1
	})

	// Session 3: Add more orders that match
	session("session3", []struct {
		id       string
		userID   int64
		side     model.OrderSide
		price    float64
		qty      float64
	}{
		{"S3_ORD1", 5, model.OrderSideSell, 50200, 2.0},
		{"S3_ORD2", 6, model.OrderSideBuy, 50200, 2.0}, // Matches S3_ORD1
	})

	// Final recovery
	m := engine.NewMatcher(engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	})

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)

	err := m.Recover(engine.RecoveryConfig{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
	})
	require.NoError(t, err)

	// All orders should be matched
	bids, asks := m.GetOrderBook(symbol, 10)
	assert.Len(t, bids, 0)
	assert.Len(t, asks, 0)

	m.Shutdown()
	walManager.Close()
}

func TestWALRecovery_VerifyWALEntries(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create entries
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)
		m.SetSnapshotDir(snapshotDir)

		ctx := context.Background()

		// Submit some orders
		_, _ = m.SubmitOrder(ctx, "ORD001", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
		_, _ = m.SubmitOrder(ctx, "ORD002", 2, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))

		// Allow async notifications to process
		waitForNotifications()

		// Take snapshot
		_ = m.TakeSnapshot(symbol, snapshotDir)

		// Add more orders
		_, _ = m.SubmitOrder(ctx, "ORD003", 3, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
		_, _ = m.SubmitOrder(ctx, "ORD004", 4, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))

		// Allow async notifications to process
		waitForNotifications()

		m.Shutdown()
		walManager.Close()
	}

	// Verify WAL file exists and has entries (WAL is in subdirectory)
	safeSymbol := strings.ReplaceAll(symbol, "/", "_")
	walPath := filepath.Join(walDir, safeSymbol, safeSymbol+".wal")
	_, err := os.Stat(walPath)
	require.NoError(t, err)

	// Phase 2: Recover
	{
		m := engine.NewMatcher(engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		})

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// All matched
		bids, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, bids, 0)
		assert.Len(t, asks, 0)

		m.Shutdown()
		walManager.Close()
	}
}

func TestWALRecovery_CleanShutdown(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	cfg := engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	}
	m := engine.NewMatcher(cfg)

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)
	m.SetSnapshotDir(snapshotDir)

	ctx := context.Background()

	// Create some state
	_, _ = m.SubmitOrder(ctx, "ORD001", 1, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	_, _ = m.SubmitOrder(ctx, "ORD002", 2, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))

	// Take snapshot before shutdown
	_ = m.TakeSnapshot(symbol, snapshotDir)

	// Clean shutdown
	m.Shutdown()
	walManager.Close()

	// Verify WAL is closed properly by trying to open it again
	w, err := wal.NewWAL(symbol, walDir, wal.SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	// WAL should have entries
	assert.Greater(t, w.LastLSN(), uint64(0))
}
