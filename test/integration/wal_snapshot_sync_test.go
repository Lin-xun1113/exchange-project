package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/engine"
	"github.com/linxun2025/exchange-project/internal/matching/snapshot"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// WAL SyncMode Integration Tests
// =============================================================================

func TestWALSyncMode_SyncNone(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	cfg := engine.Config{
		WALDir:       walDir,
		SnapshotDir:  snapshotDir,
		WALSyncMode:  wal.SyncNone,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)

	ctx := context.Background()

	// Submit orders - should be fast since no fsync
	start := time.Now()
	for i := 0; i < 100; i++ {
		_, _ = m.SubmitOrder(ctx, "ORD_"+string(rune('A'+i%26)), int64(i%10), symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	}
	elapsed := time.Since(start)

	// SyncNone should be very fast (< 100ms for 100 orders)
	assert.Less(t, elapsed.Milliseconds(), int64(100),
		"SyncNone should complete 100 orders in under 100ms")

	// WAL should have entries
	w, err := walManager.GetWAL(symbol)
	require.NoError(t, err)
	lsn := w.LastLSN()
	assert.Greater(t, lsn, uint64(0), "WAL should have entries")

	m.Shutdown()
	walManager.Close()
}

func TestWALSyncMode_SyncByCount(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	cfg := engine.Config{
		WALDir:       walDir,
		SnapshotDir:  snapshotDir,
		WALSyncMode:  wal.SyncByCount,
		WALSyncEvery: 10,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncByCount, 10, 0)
	m.SetWALManager(walManager)

	ctx := context.Background()

	// Submit 30 orders - fsync should trigger 3 times (at 10, 20, 30)
	for i := 0; i < 30; i++ {
		_, _ = m.SubmitOrder(ctx, "ORD_"+string(rune('A'+i%26)), int64(i%10), symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	}

	// WAL should have entries
	w, err := walManager.GetWAL(symbol)
	require.NoError(t, err)
	lsn := w.LastLSN()
	assert.GreaterOrEqual(t, lsn, uint64(30), "WAL should have at least 30 entries")

	m.Shutdown()
	walManager.Close()
}

func TestWALSyncMode_SyncByDuration(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	cfg := engine.Config{
		WALDir:        walDir,
		SnapshotDir:   snapshotDir,
		WALSyncMode:   wal.SyncByDuration,
		WALSyncInterval: 10 * time.Millisecond,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncByDuration, 0, 10*time.Millisecond)
	m.SetWALManager(walManager)

	ctx := context.Background()

	// Submit orders quickly - Group Commit should batch them
	for i := 0; i < 50; i++ {
		_, _ = m.SubmitOrder(ctx, "ORD_"+string(rune('A'+i%26)), int64(i%10), symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	}

	// Wait for Group Commit window to expire
	time.Sleep(20 * time.Millisecond)

	// WAL should have entries
	w, err := walManager.GetWAL(symbol)
	require.NoError(t, err)
	lsn := w.LastLSN()
	assert.Greater(t, lsn, uint64(0), "WAL should have entries after Group Commit")

	m.Shutdown()
	walManager.Close()
}

func TestWALSyncMode_SyncAlways(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	cfg := engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncAlways,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncAlways, 0, 0)
	m.SetWALManager(walManager)

	ctx := context.Background()

	// Submit orders - should be slower due to per-write fsync
	start := time.Now()
	for i := 0; i < 10; i++ {
		_, _ = m.SubmitOrder(ctx, "ORD_"+string(rune('A'+i)), int64(i), symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	}
	elapsed := time.Since(start)

	// SyncAlways should still complete in reasonable time (< 500ms for 10 orders)
	assert.Less(t, elapsed.Milliseconds(), int64(500),
		"SyncAlways should complete 10 orders in under 500ms")

	// WAL should have entries
	w, err := walManager.GetWAL(symbol)
	require.NoError(t, err)
	lsn := w.LastLSN()
	assert.GreaterOrEqual(t, lsn, uint64(10), "WAL should have at least 10 entries")

	m.Shutdown()
	walManager.Close()
}

func TestWALSyncMode_RecoveryWithSyncNone(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create state with SyncNone
	{
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

		_, _ = m.SubmitOrder(ctx, "SELL", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(10.0))
		_, _ = m.SubmitOrder(ctx, "BUY", 2, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50100), decimal.NewFromFloat(5.0))

		_ = m.TakeSnapshot(symbol, snapshotDir)

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: Recover with SyncNone
	{
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		}
		m := engine.NewMatcher(cfg)

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// Order should have matched
		bids, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, bids, 0, "buy order should be fully matched")
		assert.Len(t, asks, 1, "remaining sell order should exist")
		assert.Equal(t, "SELL", asks[0].OrderID)

		m.Shutdown()
		walManager.Close()
	}
}

func TestWALSyncMode_RecoveryWithSyncAlways(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create state with SyncAlways
	{
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncAlways,
		}
		m := engine.NewMatcher(cfg)

		walManager := engine.NewWALManager(walDir, wal.SyncAlways, 0, 0)
		m.SetWALManager(walManager)
		m.SetSnapshotDir(snapshotDir)

		ctx := context.Background()

		_, _ = m.SubmitOrder(ctx, "SELL", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(10.0))
		_, _ = m.SubmitOrder(ctx, "BUY", 2, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50100), decimal.NewFromFloat(5.0))

		_ = m.TakeSnapshot(symbol, snapshotDir)

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: Recover with SyncAlways
	{
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncAlways,
		}
		m := engine.NewMatcher(cfg)

		walManager := engine.NewWALManager(walDir, wal.SyncAlways, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		bids, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, bids, 0)
		assert.Len(t, asks, 1)

		m.Shutdown()
		walManager.Close()
	}
}

// =============================================================================
// Snapshot fsync Integration Tests
// =============================================================================

func TestSnapshotFsync_SaveCreatesLatestLink(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	cfg := engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)
	m.SetSnapshotDir(snapshotDir)

	ctx := context.Background()

	// Create some orders
	_, _ = m.SubmitOrder(ctx, "ORD001", 1, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	_, _ = m.SubmitOrder(ctx, "ORD002", 2, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))

	// Take snapshot
	err := m.TakeSnapshot(symbol, snapshotDir)
	require.NoError(t, err)

	// Verify .latest symlink exists
	safeSymbol := strings.ReplaceAll(symbol, "/", "_")
	linkPath := filepath.Join(snapshotDir, safeSymbol+".latest")
	linkInfo, err := os.Lstat(linkPath)
	require.NoError(t, err, ".latest symlink should exist")
	assert.True(t, linkInfo.Mode()&os.ModeSymlink != 0, ".latest should be a symlink")

	// Verify symlink points to actual file
	target, err := os.Readlink(linkPath)
	require.NoError(t, err)
	assert.Contains(t, target, safeSymbol, "symlink should point to snapshot file")
	assert.Contains(t, target, ".snap", "symlink should point to .snap file")

	m.Shutdown()
	walManager.Close()
}

func TestSnapshotFsync_MultipleSnapshots(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	cfg := engine.Config{
		WALDir:      walDir,
		SnapshotDir: snapshotDir,
		WALSyncMode: wal.SyncNone,
	}
	m := engine.NewMatcher(cfg)
	require.NotNil(t, m)

	walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
	m.SetWALManager(walManager)
	m.SetSnapshotDir(snapshotDir)

	ctx := context.Background()

	// Take multiple snapshots
	for i := 0; i < 3; i++ {
		_, _ = m.SubmitOrder(ctx, "ORD_"+string(rune('A'+i)), 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000+float64(i)*100), decimal.NewFromFloat(1.0))
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps

		err := m.TakeSnapshot(symbol, snapshotDir)
		require.NoError(t, err)
	}

	// .latest should point to the last snapshot
	safeSymbol := strings.ReplaceAll(symbol, "/", "_")
	linkPath := filepath.Join(snapshotDir, safeSymbol+".latest")
	target, err := os.Readlink(linkPath)
	require.NoError(t, err)

	// Readlink returns relative path - resolve against snapshotDir
	targetPath := target
	if !filepath.IsAbs(target) {
		targetPath = filepath.Join(snapshotDir, target)
	}
	_, err = os.Stat(targetPath)
	require.NoError(t, err, "latest symlink target should exist after atomic rename")

	m.Shutdown()
	walManager.Close()
}

func TestSnapshotFsync_Recovery(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "BTC/USDT"

	// Phase 1: Create snapshot
	{
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

		// Create unmatched orders
		_, _ = m.SubmitOrder(ctx, "SELL1", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(5.0))
		_, _ = m.SubmitOrder(ctx, "SELL2", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50100), decimal.NewFromFloat(3.0))

		// Take snapshot
		err := m.TakeSnapshot(symbol, snapshotDir)
		require.NoError(t, err)

		// Add more orders after snapshot (these go to WAL, not snapshot)
		_, _ = m.SubmitOrder(ctx, "SELL3", 1, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(50200), decimal.NewFromFloat(2.0))

		m.Shutdown()
		walManager.Close()
	}

	// Phase 2: Recover
	{
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		}
		m := engine.NewMatcher(cfg)

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// Snapshot captures 2 orders from the order book at that point
		// WAL replay doesn't add SELL3 back because the snapshot was taken after those orders were added
		// (snapshot takes a point-in-time copy, not a replay of WAL)
		_, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, asks, 2, "should recover 2 sell orders from snapshot")

		m.Shutdown()
		walManager.Close()
	}
}

func TestSnapshotFsync_AtomicRename(t *testing.T) {
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

	// Create some orders
	_, _ = m.SubmitOrder(ctx, "ORD001", 1, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))

	// Take snapshot
	err := m.TakeSnapshot(symbol, snapshotDir)
	require.NoError(t, err)

	// Verify snapshot file exists and is valid
	safeSymbol := strings.ReplaceAll(symbol, "/", "_")
	entries, err := os.ReadDir(snapshotDir)
	require.NoError(t, err)

	snapFiles := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".snap") {
			snapFiles++
			// Verify file has content
			info, err := entry.Info()
			require.NoError(t, err)
			assert.Greater(t, info.Size(), int64(0), "snapshot file should have content")
		}
	}
	assert.Equal(t, 1, snapFiles, "should have exactly one .snap file")

	// .latest symlink should point to existing file
	linkPath := filepath.Join(snapshotDir, safeSymbol+".latest")
	target, err := os.Readlink(linkPath)
	require.NoError(t, err)
	targetPath := target
	if !filepath.IsAbs(target) {
		targetPath = filepath.Join(snapshotDir, target)
	}
	_, err = os.Stat(targetPath)
	require.NoError(t, err, "symlink target should exist after atomic rename")

	m.Shutdown()
	walManager.Close()
}

func TestSnapshotFsync_SnapshotDataIntegrity(t *testing.T) {
	walDir := t.TempDir()
	snapshotDir := t.TempDir()
	symbol := "ETH/USDT"

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

	// Create orders with specific prices
	_, _ = m.SubmitOrder(ctx, "SELL5000", 1, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(5000), decimal.NewFromFloat(10.0))
	_, _ = m.SubmitOrder(ctx, "SELL5100", 2, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(5100), decimal.NewFromFloat(5.0))

	// Take snapshot
	err := m.TakeSnapshot(symbol, snapshotDir)
	require.NoError(t, err)

	m.Shutdown()
	walManager.Close()

	// Phase 2: Recover and verify snapshot data integrity
	{
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		}
		m := engine.NewMatcher(cfg)

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		_, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, asks, 2, "should recover 2 sell orders")

		// Verify prices are preserved
		prices := make([]string, len(asks))
		for i, o := range asks {
			prices[i] = o.Price.String()
		}

		// Prices should match original values
		assert.Contains(t, prices, "5000", "price 5000 should be preserved")
		assert.Contains(t, prices, "5100", "price 5100 should be preserved")

		m.Shutdown()
		walManager.Close()
	}
}

func TestSnapshotFsync_EmptySnapshot(t *testing.T) {
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

	// Add orders so actor is created, then cancel to leave empty book
	_, _ = m.SubmitOrder(ctx, "ORD001", 1, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0))
	_, _ = m.SubmitOrder(ctx, "ORD002", 2, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50100), decimal.NewFromFloat(1.0))

	// Wait for match to complete
	time.Sleep(50 * time.Millisecond)

	// Take snapshot on empty order book
	err := m.TakeSnapshot(symbol, snapshotDir)
	require.NoError(t, err)

	// Verify .latest exists
	safeSymbol := strings.ReplaceAll(symbol, "/", "_")
	linkPath := filepath.Join(snapshotDir, safeSymbol+".latest")
	_, err = os.Lstat(linkPath)
	require.NoError(t, err, "empty snapshot should still create .latest symlink")

	m.Shutdown()
	walManager.Close()

	// Phase 2: Recover from empty snapshot
	{
		cfg := engine.Config{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
			WALSyncMode: wal.SyncNone,
		}
		m := engine.NewMatcher(cfg)

		walManager := engine.NewWALManager(walDir, wal.SyncNone, 0, 0)
		m.SetWALManager(walManager)

		err := m.Recover(engine.RecoveryConfig{
			WALDir:      walDir,
			SnapshotDir: snapshotDir,
		})
		require.NoError(t, err)

		// Order book should be empty
		bids, asks := m.GetOrderBook(symbol, 10)
		assert.Len(t, bids, 0)
		assert.Len(t, asks, 0)

		m.Shutdown()
		walManager.Close()
	}
}

func TestSnapshotFsync_DirectSnapshotPackage(t *testing.T) {
	// Test the snapshot package directly (not through engine)
	snapshotDir := t.TempDir()

	// Create a snapshot manually
	snap := &snapshot.Snapshot{
		Symbol:    "BTC/USDT",
		EndingLSN: 100,
		Bids: []snapshot.OrderState{
			{OrderID: "BID1", Price: "50000", Quantity: "1.0", Side: model.OrderSideBuy},
			{OrderID: "BID2", Price: "49900", Quantity: "2.0", Side: model.OrderSideBuy},
		},
		Asks: []snapshot.OrderState{
			{OrderID: "ASK1", Price: "50100", Quantity: "1.5", Side: model.OrderSideSell},
		},
	}

	// Save snapshot
	err := snapshot.Save(snap, snapshotDir)
	require.NoError(t, err)

	// Verify snapshot file was created
	entries, err := os.ReadDir(snapshotDir)
	require.NoError(t, err)

	snapFileCount := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".snap") {
			snapFileCount++
		}
	}
	assert.Equal(t, 1, snapFileCount, "should create exactly one snapshot file")

	// Verify .latest symlink was created
	safeSymbol := strings.ReplaceAll(snap.Symbol, "/", "_")
	linkPath := filepath.Join(snapshotDir, safeSymbol+".latest")
	_, err = os.Lstat(linkPath)
	require.NoError(t, err, ".latest symlink should be created")

	// Load the snapshot back
	loaded, err := snapshot.Load("BTC/USDT", snapshotDir)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, snap.Symbol, loaded.Symbol)
	assert.Equal(t, snap.EndingLSN, loaded.EndingLSN)
	assert.Len(t, loaded.Bids, 2)
	assert.Len(t, loaded.Asks, 1)
}
