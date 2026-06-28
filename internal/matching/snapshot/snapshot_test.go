package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshot_Take_Empty(t *testing.T) {
	symbol := "BTC/USDT"
	ob := book.NewOrderBook(symbol)
	const walLSN uint64 = 100

	snap := Take(symbol, ob, walLSN)

	require.NotNil(t, snap)
	assert.Equal(t, symbol, snap.Symbol)
	assert.Equal(t, walLSN, snap.EndingLSN)
	assert.Len(t, snap.Bids, 0)
	assert.Len(t, snap.Asks, 0)
}

func TestSnapshot_Take_WithOrders(t *testing.T) {
	symbol := "ETH/USDT"
	ob := book.NewOrderBook(symbol)
	const walLSN uint64 = 42

	// Add bid orders
	ob.AddOrderForRecovery(book.NewOrderInBook(
		1, "BID001", 100, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(3000), decimal.NewFromFloat(1.0),
	))
	ob.AddOrderForRecovery(book.NewOrderInBook(
		2, "BID002", 101, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(2990), decimal.NewFromFloat(2.0),
	))

	// Add ask orders
	ob.AddOrderForRecovery(book.NewOrderInBook(
		3, "ASK001", 200, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(3100), decimal.NewFromFloat(1.5),
	))
	ob.AddOrderForRecovery(book.NewOrderInBook(
		4, "ASK002", 201, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(3110), decimal.NewFromFloat(0.5),
	))

	snap := Take(symbol, ob, walLSN)

	require.NotNil(t, snap)
	assert.Equal(t, symbol, snap.Symbol)
	assert.Equal(t, walLSN, snap.EndingLSN)
	assert.Len(t, snap.Bids, 2)
	assert.Len(t, snap.Asks, 2)

	// Verify bid orders
	assert.Equal(t, "BID001", snap.Bids[0].OrderID)
	assert.Equal(t, "BID002", snap.Bids[1].OrderID)

	// Verify ask orders
	assert.Equal(t, "ASK001", snap.Asks[0].OrderID)
	assert.Equal(t, "ASK002", snap.Asks[1].OrderID)
}

func TestSnapshot_Take_OrderStateFields(t *testing.T) {
	symbol := "BTC/USDT"
	ob := book.NewOrderBook(symbol)

	order := book.NewOrderInBook(
		1, "ORD001", 100, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
	)
	order.FilledQuantity = decimal.NewFromFloat(0.5)
	order.RemainingQty = decimal.NewFromFloat(0.5)
	order.Status = model.OrderStatusPartialFilled
	order.CreatedAt = 1234567890

	ob.AddOrderForRecovery(order)

	snap := Take(symbol, ob, 10)

	require.Len(t, snap.Bids, 1)
	state := snap.Bids[0]

	assert.Equal(t, int64(1), state.ID)
	assert.Equal(t, "ORD001", state.OrderID)
	assert.Equal(t, int64(100), state.UserID)
	assert.Equal(t, symbol, state.Symbol)
	assert.Equal(t, model.OrderSideBuy, state.Side)
	assert.Equal(t, model.OrderTypeLimit, state.OrderType)
	assert.Equal(t, "50000", state.Price)
	assert.Equal(t, "1", state.Quantity)
	assert.Equal(t, "0.5", state.FilledQuantity)
	assert.Equal(t, "0.5", state.RemainingQty)
	assert.Equal(t, model.OrderStatusPartialFilled, state.Status)
	assert.Equal(t, int64(1234567890), state.CreatedAt)
}

func TestSnapshot_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	// Create snapshot with orders
	ob := book.NewOrderBook(symbol)
	ob.AddOrderForRecovery(book.NewOrderInBook(
		1, "BID001", 100, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
	))
	ob.AddOrderForRecovery(book.NewOrderInBook(
		2, "ASK001", 200, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(51000), decimal.NewFromFloat(2.0),
	))

	snap := Take(symbol, ob, 50)

	// Save snapshot
	err := Save(snap, dir)
	require.NoError(t, err)

	// Verify symlink was created
	safeSymbol := "BTC_USDT"
	linkPath := filepath.Join(dir, safeSymbol+".latest")
	_, err = os.Lstat(linkPath)
	require.NoError(t, err)

	// Load snapshot
	loaded, err := Load(symbol, dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, symbol, loaded.Symbol)
	assert.Equal(t, uint64(50), loaded.EndingLSN)
	assert.Len(t, loaded.Bids, 1)
	assert.Len(t, loaded.Asks, 1)
	assert.Equal(t, "BID001", loaded.Bids[0].OrderID)
	assert.Equal(t, "ASK001", loaded.Asks[0].OrderID)
}

func TestSnapshot_Save_Multiple(t *testing.T) {
	dir := t.TempDir()
	symbol := "ETH/USDT"

	// Create first snapshot
	{
		ob := book.NewOrderBook(symbol)
		ob.AddOrderForRecovery(book.NewOrderInBook(
			1, "ORD001", 100, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(3000), decimal.NewFromFloat(1.0),
		))
		snap := Take(symbol, ob, 10)
		err := Save(snap, dir)
		require.NoError(t, err)
	}

	// Create second snapshot (same symbol, different LSN)
	{
		ob := book.NewOrderBook(symbol)
		ob.AddOrderForRecovery(book.NewOrderInBook(
			1, "ORD002", 101, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(3000), decimal.NewFromFloat(2.0),
		))
		ob.AddOrderForRecovery(book.NewOrderInBook(
			2, "ORD003", 102, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(3100), decimal.NewFromFloat(1.0),
		))
		snap := Take(symbol, ob, 20)
		err := Save(snap, dir)
		require.NoError(t, err)
	}

	// Load latest - should be the second snapshot
	loaded, err := Load(symbol, dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, uint64(20), loaded.EndingLSN)
	assert.Len(t, loaded.Bids, 1)
	assert.Len(t, loaded.Asks, 1)
	assert.Equal(t, "ORD002", loaded.Bids[0].OrderID)
	assert.Equal(t, "ORD003", loaded.Asks[0].OrderID)
}

func TestSnapshot_Save_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory that doesn't exist
	snapshotDir := filepath.Join(dir, "snapshots")
	symbol := "BTC/USDT"

	// Create snapshot
	ob := book.NewOrderBook(symbol)
	snap := Take(symbol, ob, 5)

	// Save should create the directory
	err := Save(snap, snapshotDir)
	require.NoError(t, err)

	// Verify directory was created
	_, err = os.Stat(snapshotDir)
	require.NoError(t, err)

	// Load should work
	loaded, err := Load(symbol, snapshotDir)
	require.NoError(t, err)
	require.NotNil(t, loaded)
}

func TestSnapshot_Load_NotFound(t *testing.T) {
	dir := t.TempDir()
	symbol := "NONEXISTENT"

	_, err := Load(symbol, dir)
	assert.Error(t, err)
}

func TestSnapshot_Load_BrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	safeSymbol := "BTC_USDT"

	// Create a broken symlink
	linkPath := filepath.Join(dir, safeSymbol+".latest")
	err := os.Symlink("/nonexistent/path", linkPath)
	require.NoError(t, err)

	_, err = Load("BTC/USDT", dir)
	assert.Error(t, err)
}

func TestSnapshot_Restore_Empty(t *testing.T) {
	symbol := "BTC/USDT"
	ob := book.NewOrderBook(symbol)

	snap := Take(symbol, ob, 10)

	// Restore to empty book
	newBook := book.NewOrderBook(symbol)
	err := Restore(snap, newBook)
	require.NoError(t, err)

	bids, asks := newBook.GetDepth(10)
	assert.Len(t, bids, 0)
	assert.Len(t, asks, 0)
}

func TestSnapshot_Restore_WithOrders(t *testing.T) {
	symbol := "ETH/USDT"
	dir := t.TempDir()

	// Create snapshot with orders
	{
		ob := book.NewOrderBook(symbol)
		ob.AddOrderForRecovery(book.NewOrderInBook(
			1, "BID001", 100, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(3000), decimal.NewFromFloat(1.0),
		))
		ob.AddOrderForRecovery(book.NewOrderInBook(
			2, "BID002", 101, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(2990), decimal.NewFromFloat(2.0),
		))
		ob.AddOrderForRecovery(book.NewOrderInBook(
			3, "ASK001", 200, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(3100), decimal.NewFromFloat(1.5),
		))

		snap := Take(symbol, ob, 25)

		// Save snapshot
		err := Save(snap, dir)
		require.NoError(t, err)
	}

	// Load and restore (use same directory)
	snap, err := Load(symbol, dir)
	require.NoError(t, err)

	newBook := book.NewOrderBook(symbol)
	err = Restore(snap, newBook)
	require.NoError(t, err)

	// Verify orders were restored
	bids, asks := newBook.GetDepth(10)
	assert.Len(t, bids, 2)
	assert.Len(t, asks, 1)

	// Verify best bid
	bestBid := newBook.GetBestBid()
	assert.Equal(t, "3000", bestBid.String())

	// Verify best ask
	bestAsk := newBook.GetBestAsk()
	assert.Equal(t, "3100", bestAsk.String())
}

func TestSnapshot_Restore_OrderFields(t *testing.T) {
	symbol := "BTC/USDT"

	// Create snapshot with detailed order
	ob := book.NewOrderBook(symbol)
	order := book.NewOrderInBook(
		1, "RESTORE001", 999, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
	)
	order.FilledQuantity = decimal.NewFromFloat(0.3)
	order.RemainingQty = decimal.NewFromFloat(0.7)
	order.Status = model.OrderStatusPartialFilled
	order.CreatedAt = 9876543210
	ob.AddOrderForRecovery(order)

	snap := Take(symbol, ob, 100)
	dir := t.TempDir()
	err := Save(snap, dir)
	require.NoError(t, err)

	// Load
	loaded, err := Load(symbol, dir)
	require.NoError(t, err)

	// Restore
	newBook := book.NewOrderBook(symbol)
	err = Restore(loaded, newBook)
	require.NoError(t, err)

	// Get restored order
	restored := newBook.GetOrderByID("RESTORE001")
	require.NotNil(t, restored)

	// Verify all fields
	assert.Equal(t, "RESTORE001", restored.OrderID)
	assert.Equal(t, int64(999), restored.UserID)
	assert.Equal(t, symbol, restored.Symbol)
	assert.Equal(t, model.OrderSideBuy, restored.Side)
	assert.Equal(t, model.OrderTypeLimit, restored.OrderType)
	assert.Equal(t, "50000", restored.Price.String())
	assert.Equal(t, "1", restored.Quantity.String())
	assert.Equal(t, "0.3", restored.FilledQuantity.String())
	assert.Equal(t, "0.7", restored.RemainingQty.String())
	assert.Equal(t, model.OrderStatusPartialFilled, restored.Status)
	assert.Equal(t, int64(9876543210), restored.CreatedAt)
}

func TestSnapshot_Restore_Partial(t *testing.T) {
	// Create snapshot with some orders that might fail to restore
	symbol := "BTC/USDT"
	ob := book.NewOrderBook(symbol)

	// Add valid orders
	ob.AddOrderForRecovery(book.NewOrderInBook(
		1, "VALID001", 100, symbol,
		model.OrderSideBuy, model.OrderTypeLimit,
		decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
	))
	ob.AddOrderForRecovery(book.NewOrderInBook(
		2, "VALID002", 101, symbol,
		model.OrderSideSell, model.OrderTypeLimit,
		decimal.NewFromFloat(51000), decimal.NewFromFloat(2.0),
	))

	snap := Take(symbol, ob, 50)
	dir := t.TempDir()
	err := Save(snap, dir)
	require.NoError(t, err)

	// Load
	loaded, err := Load(symbol, dir)
	require.NoError(t, err)

	// Restore to empty book
	newBook := book.NewOrderBook(symbol)
	err = Restore(loaded, newBook)
	require.NoError(t, err)

	// Both orders should be restored
	bids, asks := newBook.GetDepth(10)
	assert.Len(t, bids, 1)
	assert.Len(t, asks, 1)
}

func TestSnapshot_FullRecoveryCycle(t *testing.T) {
	symbol := "BTC/USDT"
	snapshotDir := t.TempDir()

	// Phase 1: Create state and snapshot
	{
		ob := book.NewOrderBook(symbol)

		// Add some orders
		ob.AddOrderForRecovery(book.NewOrderInBook(
			1, "PHASE1_BID1", 100, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
		))
		ob.AddOrderForRecovery(book.NewOrderInBook(
			2, "PHASE1_BID2", 101, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(49900), decimal.NewFromFloat(0.5),
		))
		ob.AddOrderForRecovery(book.NewOrderInBook(
			3, "PHASE1_ASK1", 200, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(51000), decimal.NewFromFloat(2.0),
		))

		// Take snapshot
		snap := Take(symbol, ob, 100)
		err := Save(snap, snapshotDir)
		require.NoError(t, err)
	}

	// Phase 2: Load and restore
	{
		snap, err := Load(symbol, snapshotDir)
		require.NoError(t, err)
		require.NotNil(t, snap)

		assert.Equal(t, symbol, snap.Symbol)
		assert.Equal(t, uint64(100), snap.EndingLSN)
		assert.Len(t, snap.Bids, 2)
		assert.Len(t, snap.Asks, 1)

		// Restore to new book
		newBook := book.NewOrderBook(symbol)
		err = Restore(snap, newBook)
		require.NoError(t, err)

		// Verify restored state
		bids, asks := newBook.GetDepth(10)
		assert.Len(t, bids, 2)
		assert.Len(t, asks, 1)

		// Verify best prices
		bestBid := newBook.GetBestBid()
		bestAsk := newBook.GetBestAsk()
		assert.Equal(t, "50000", bestBid.String())
		assert.Equal(t, "51000", bestAsk.String())
	}
}

func TestSnapshot_Take_Symbol(t *testing.T) {
	symbols := []string{"BTC/USDT", "ETH/USDC", "SOL/USDT", "NORMAL"}

	for _, symbol := range symbols {
		t.Run(symbol, func(t *testing.T) {
			ob := book.NewOrderBook(symbol)
			ob.AddOrderForRecovery(book.NewOrderInBook(
				1, "ORD001", 100, symbol,
				model.OrderSideBuy, model.OrderTypeLimit,
				decimal.NewFromFloat(100), decimal.NewFromFloat(1.0),
			))

			snap := Take(symbol, ob, 5)

			assert.Equal(t, symbol, snap.Symbol)
			assert.Len(t, snap.Bids, 1)
		})
	}
}

func TestSnapshot_OrderState_AllOrderTypes(t *testing.T) {
	symbol := "BTC/USDT"

	// Test with different order types
	orderTypes := []model.OrderType{
		model.OrderTypeLimit,
		model.OrderTypeMarket,
		model.OrderTypeIOC,
		model.OrderTypeFOK,
	}

	for _, ot := range orderTypes {
		t.Run(string(ot), func(t *testing.T) {
			ob := book.NewOrderBook(symbol)
			ob.AddOrderForRecovery(book.NewOrderInBook(
				1, "ORD_"+string(ot), 100, symbol,
				model.OrderSideBuy, ot,
				decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
			))

			snap := Take(symbol, ob, 1)

			require.Len(t, snap.Bids, 1)
			assert.Equal(t, ot, snap.Bids[0].OrderType)
		})
	}
}

func TestSnapshot_OrderState_AllSides(t *testing.T) {
	symbol := "BTC/USDT"

	// Test buy side
	{
		ob := book.NewOrderBook(symbol)
		ob.AddOrderForRecovery(book.NewOrderInBook(
			1, "BUY001", 100, symbol,
			model.OrderSideBuy, model.OrderTypeLimit,
			decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
		))

		snap := Take(symbol, ob, 1)

		require.Len(t, snap.Bids, 1)
		assert.Equal(t, model.OrderSideBuy, snap.Bids[0].Side)
		assert.Len(t, snap.Asks, 0)
	}

	// Test sell side
	{
		ob := book.NewOrderBook(symbol)
		ob.AddOrderForRecovery(book.NewOrderInBook(
			1, "SELL001", 100, symbol,
			model.OrderSideSell, model.OrderTypeLimit,
			decimal.NewFromFloat(51000), decimal.NewFromFloat(1.0),
		))

		snap := Take(symbol, ob, 1)

		require.Len(t, snap.Asks, 1)
		assert.Equal(t, model.OrderSideSell, snap.Asks[0].Side)
		assert.Len(t, snap.Bids, 0)
	}
}

func TestSnapshot_OrderState_AllStatuses(t *testing.T) {
	symbol := "BTC/USDT"

	statuses := []model.OrderStatus{
		model.OrderStatusPending,
		model.OrderStatusPartialFilled,
		model.OrderStatusFilled,
		model.OrderStatusCancelled,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			ob := book.NewOrderBook(symbol)
			order := book.NewOrderInBook(
				1, "ORD_"+string(status), 100, symbol,
				model.OrderSideBuy, model.OrderTypeLimit,
				decimal.NewFromFloat(50000), decimal.NewFromFloat(1.0),
			)
			order.Status = status
			ob.AddOrderForRecovery(order)

			snap := Take(symbol, ob, 1)

			require.Len(t, snap.Bids, 1)
			assert.Equal(t, status, snap.Bids[0].Status)
		})
	}
}
