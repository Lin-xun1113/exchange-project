package wal

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWAL_NewWAL(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	require.NotNil(t, w)

	// Verify file was created
	safeSymbol := SanitizeSymbol(symbol)
	expectedPath := filepath.Join(dir, safeSymbol, safeSymbol+".wal")
	_, err = os.Stat(expectedPath)
	require.NoError(t, err)

	// Verify initial LSN is 0
	assert.Equal(t, uint64(0), w.LastLSN())

	w.Close()
}

func TestWAL_Append_Command(t *testing.T) {
	dir := t.TempDir()
	symbol := "ETH/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	entry := &Entry{
		Type: EntryTypeCommand,
		Payload: CommandPayload{
			OrderID:   "ORD001",
			UserID:    123,
			Side:      "buy",
			OrderType: "limit",
			Price:     "50000.00",
			Quantity:  "1.5",
		},
	}

	lsn, err := w.Append(entry)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), lsn)
	assert.Equal(t, uint64(1), w.LastLSN())
}

func TestWAL_Append_Cancel(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	entry := &Entry{
		Type: EntryTypeCancel,
		Payload: CancelPayload{
			OrderID: "ORD001",
		},
	}

	lsn, err := w.Append(entry)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), lsn)
}

func TestWAL_Append_Trade(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	entry := &Entry{
		Type: EntryTypeTrade,
		Payload: TradePayload{
			TradeID:     "TRD001",
			BuyOrderID:  "ORD001",
			SellOrderID: "ORD002",
			BuyUserID:   123,
			SellUserID:  456,
			Price:       "50000.00",
			Quantity:    "1.5",
			Symbol:      "BTC/USDT",
			OrderID:     "ORD001",
		},
	}

	lsn, err := w.Append(entry)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), lsn)
}

func TestWAL_Append_Multiple(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	// Append multiple entries
	for i := 0; i < 5; i++ {
		entry := &Entry{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD" + string(rune('A'+i)),
				UserID:    int64(i),
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		}
		lsn, err := w.Append(entry)
		require.NoError(t, err)
		assert.Equal(t, uint64(i+1), lsn)
	}

	assert.Equal(t, uint64(5), w.LastLSN())
}

func TestWAL_LastLSN_Empty(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	// New WAL should have LSN 0
	assert.Equal(t, uint64(0), w.LastLSN())
}

func TestWAL_LastLSN_AfterAppend(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	entry := &Entry{
		Type: EntryTypeCommand,
		Payload: CommandPayload{
			OrderID:   "ORD001",
			UserID:    123,
			Side:      "buy",
			OrderType: "limit",
			Price:     "50000.00",
			Quantity:  "1.0",
		},
	}

	_, err = w.Append(entry)
	require.NoError(t, err)

	// Verify LastLSN
	assert.Equal(t, uint64(1), w.LastLSN())
}

func TestWAL_LastLSN_Persists(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	// Create and append
	{
		w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
		require.NoError(t, err)

		entry := &Entry{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD001",
				UserID:    123,
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		}
		_, err = w.Append(entry)
		require.NoError(t, err)
		_, err = w.Append(entry)
		require.NoError(t, err)

		w.Close()
	}

	// Reopen and check LSN
	{
		w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
		require.NoError(t, err)
		defer w.Close()

		assert.Equal(t, uint64(2), w.LastLSN())
	}
}

func TestWAL_Replay_Empty(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	var replayed []*Entry
	err = w.Replay(0, func(entry *Entry) error {
		replayed = append(replayed, entry)
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 0)
}

func TestWAL_Replay_FromLSN(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append 5 entries
	for i := 0; i < 5; i++ {
		entry := &Entry{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD" + string(rune('A'+i)),
				UserID:    int64(i),
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		}
		_, err = w.Append(entry)
		require.NoError(t, err)
	}
	w.Close()

	// Reopen and replay from LSN 2
	w2, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w2.Close()

	var replayed []*Entry
	err = w2.Replay(2, func(entry *Entry) error {
		replayed = append(replayed, entry)
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 3) // Should get LSN 3, 4, 5
	assert.Equal(t, uint64(3), replayed[0].LSN)
	assert.Equal(t, uint64(4), replayed[1].LSN)
	assert.Equal(t, uint64(5), replayed[2].LSN)
}

func TestWAL_Replay_All(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append mixed entries
	entries := []*Entry{
		{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD001",
				UserID:    1,
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		},
		{
			Type: EntryTypeCancel,
			Payload: CancelPayload{
				OrderID: "ORD002",
			},
		},
		{
			Type: EntryTypeTrade,
			Payload: TradePayload{
				TradeID:     "TRD001",
				BuyOrderID:  "ORD001",
				SellOrderID: "ORD003",
				BuyUserID:   1,
				SellUserID:  3,
				Price:       "50000.00",
				Quantity:    "0.5",
				Symbol:      "BTC/USDT",
				OrderID:     "ORD001",
			},
		},
	}

	for _, e := range entries {
		_, err = w.Append(e)
		require.NoError(t, err)
	}
	w.Close()

	// Reopen and replay all
	w2, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w2.Close()

	var replayed []*Entry
	err = w2.Replay(0, func(entry *Entry) error {
		replayed = append(replayed, entry)
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 3)

	// Verify types
	assert.Equal(t, EntryTypeCommand, replayed[0].Type)
	assert.Equal(t, EntryTypeCancel, replayed[1].Type)
	assert.Equal(t, EntryTypeTrade, replayed[2].Type)

	// Verify payloads
	cmdPayload, ok := replayed[0].Payload.(CommandPayload)
	require.True(t, ok)
	assert.Equal(t, "ORD001", cmdPayload.OrderID)

	cancelPayload, ok := replayed[1].Payload.(CancelPayload)
	require.True(t, ok)
	assert.Equal(t, "ORD002", cancelPayload.OrderID)

	tradePayload, ok := replayed[2].Payload.(TradePayload)
	require.True(t, ok)
	assert.Equal(t, "TRD001", tradePayload.TradeID)
}

func TestWAL_Replay_Error(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	entry := &Entry{
		Type: EntryTypeCommand,
		Payload: CommandPayload{
			OrderID:   "ORD001",
			UserID:    1,
			Side:      "buy",
			OrderType: "limit",
			Price:     "50000.00",
			Quantity:  "1.0",
		},
	}
	_, err = w.Append(entry)
	require.NoError(t, err)
	w.Close()

	w2, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w2.Close()

	// Return error from handler
	err = w2.Replay(0, func(entry *Entry) error {
		return assert.AnError
	})
	assert.Error(t, err)
}

func TestWAL_Truncate_Basic(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append 5 entries
	for i := 0; i < 5; i++ {
		entry := &Entry{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD" + string(rune('A'+i)),
				UserID:    int64(i),
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		}
		_, err = w.Append(entry)
		require.NoError(t, err)
	}
	assert.Equal(t, uint64(5), w.LastLSN())
	w.Close()

	// Reopen and truncate to LSN 3
	w2, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w2.Close()

	err = w2.Truncate(3)
	require.NoError(t, err)

	// Last LSN should be 5 (the last remaining entry after truncating entries 1-3)
	assert.Equal(t, uint64(5), w2.LastLSN())

	// Replay should only get entries 4 and 5
	var replayed []*Entry
	err = w2.Replay(0, func(entry *Entry) error {
		replayed = append(replayed, entry)
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 2)
	assert.Equal(t, uint64(4), replayed[0].LSN)
	assert.Equal(t, uint64(5), replayed[1].LSN)
}

func TestWAL_Truncate_All(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append 3 entries
	for i := 0; i < 3; i++ {
		entry := &Entry{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD" + string(rune('A'+i)),
				UserID:    int64(i),
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		}
		_, err = w.Append(entry)
		require.NoError(t, err)
	}
	w.Close()

	// Reopen and truncate all
	w2, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w2.Close()

	err = w2.Truncate(3)
	require.NoError(t, err)

	// Last LSN should be 0
	assert.Equal(t, uint64(0), w2.LastLSN())

	// Replay should get nothing
	var replayed []*Entry
	err = w2.Replay(0, func(entry *Entry) error {
		replayed = append(replayed, entry)
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 0)
}

func TestWAL_Truncate_None(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append 3 entries
	for i := 0; i < 3; i++ {
		entry := &Entry{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD" + string(rune('A'+i)),
				UserID:    int64(i),
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		}
		_, err = w.Append(entry)
		require.NoError(t, err)
	}
	w.Close()

	// Reopen and truncate to LSN 0 (should truncate nothing)
	w2, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w2.Close()

	err = w2.Truncate(0)
	require.NoError(t, err)

	// All entries should remain
	assert.Equal(t, uint64(3), w2.LastLSN())
}

func TestWAL_Truncate_PreservesNewerEntries(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append mixed entries
	entries := []struct {
		typ     EntryType
		payload interface{}
	}{
		{EntryTypeCommand, CommandPayload{OrderID: "ORD001", UserID: 1, Side: "buy", OrderType: "limit", Price: "50000", Quantity: "1.0"}},
		{EntryTypeCancel, CancelPayload{OrderID: "ORD001"}},
		{EntryTypeCommand, CommandPayload{OrderID: "ORD002", UserID: 2, Side: "sell", OrderType: "limit", Price: "51000", Quantity: "2.0"}},
		{EntryTypeTrade, TradePayload{TradeID: "TRD001", BuyOrderID: "ORD001", SellOrderID: "ORD002", Symbol: "BTC/USDT"}},
		{EntryTypeCommand, CommandPayload{OrderID: "ORD003", UserID: 3, Side: "buy", OrderType: "limit", Price: "50000", Quantity: "0.5"}},
	}

	for _, e := range entries {
		entry := &Entry{Type: e.typ, Payload: e.payload}
		_, err = w.Append(entry)
		require.NoError(t, err)
	}
	w.Close()

	// Reopen and truncate to LSN 2
	w2, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w2.Close()

	err = w2.Truncate(2)
	require.NoError(t, err)

	// Should have LSN 5
	assert.Equal(t, uint64(5), w2.LastLSN())

	// Replay should get entries 3, 4, 5
	var replayed []*Entry
	err = w2.Replay(0, func(entry *Entry) error {
		replayed = append(replayed, entry)
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 3)

	// Verify the remaining entries
	assert.Equal(t, uint64(3), replayed[0].LSN)
	assert.Equal(t, EntryTypeCommand, replayed[0].Type)

	assert.Equal(t, uint64(4), replayed[1].LSN)
	assert.Equal(t, EntryTypeTrade, replayed[1].Type)

	assert.Equal(t, uint64(5), replayed[2].LSN)
	assert.Equal(t, EntryTypeCommand, replayed[2].Type)
}

func TestWAL_Close(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append an entry
	entry := &Entry{
		Type: EntryTypeCommand,
		Payload: CommandPayload{
			OrderID:   "ORD001",
			UserID:    1,
			Side:      "buy",
			OrderType: "limit",
			Price:     "50000.00",
			Quantity:  "1.0",
		},
	}
	_, err = w.Append(entry)
	require.NoError(t, err)

	// Close should succeed
	err = w.Close()
	require.NoError(t, err)

	// Closing again should be safe (no error)
	err = w.Close()
	require.NoError(t, err)

	// Append after close should fail
	_, err = w.Append(entry)
	assert.Error(t, err)
}

func TestWAL_Append_Closed(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	w.Close()

	entry := &Entry{
		Type: EntryTypeCommand,
		Payload: CommandPayload{
			OrderID:   "ORD001",
			UserID:    1,
			Side:      "buy",
			OrderType: "limit",
			Price:     "50000.00",
			Quantity:  "1.0",
		},
	}

	_, err = w.Append(entry)
	assert.Error(t, err)
}

func TestSanitizeSymbol(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"BTC/USDT", "BTC_USDT"},
		{"ETH/USDT", "ETH_USDT"},
		{"BTC:USD", "BTC_USD"},
		{"SOL/USDC", "SOL_USDC"},
		{"NORMAL", "NORMAL"},
		{"multiple/slashes/here", "multiple_slashes_here"},
		{"with:colons:here", "with_colons_here"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := SanitizeSymbol(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWAL_SnapshotLSN(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)

	// Append 3 entries
	for i := 0; i < 3; i++ {
		entry := &Entry{
			Type: EntryTypeCommand,
			Payload: CommandPayload{
				OrderID:   "ORD" + string(rune('A'+i)),
				UserID:    int64(i),
				Side:      "buy",
				OrderType: "limit",
				Price:     "50000.00",
				Quantity:  "1.0",
			},
		}
		_, err = w.Append(entry)
		require.NoError(t, err)
	}

	// SnapshotLSN should return current LSN
	snapshotLSN := w.SnapshotLSN()
	assert.Equal(t, uint64(3), snapshotLSN)

	w.Close()
}

func TestWAL_Sync(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	entry := &Entry{
		Type: EntryTypeCommand,
		Payload: CommandPayload{
			OrderID:   "ORD001",
			UserID:    1,
			Side:      "buy",
			OrderType: "limit",
			Price:     "50000.00",
			Quantity:  "1.0",
		},
	}
	_, err = w.Append(entry)
	require.NoError(t, err)

	// Sync should succeed
	err = w.Sync()
	require.NoError(t, err)
}

func TestWAL_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	symbol := "BTC/USDT"

	w, err := NewWAL(symbol, dir, SyncNone, 0, 0)
	require.NoError(t, err)
	defer w.Close()

	const numGoroutines = 10
	const entriesPerGoroutine = 10

	// Concurrent appends
	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < entriesPerGoroutine; j++ {
				entry := &Entry{
					Type: EntryTypeCommand,
					Payload: CommandPayload{
						OrderID:   "ORD" + string(rune('A'+idx)) + string(rune('0'+j)),
						UserID:    int64(idx*100+j),
						Side:      "buy",
						OrderType: "limit",
						Price:     "50000.00",
						Quantity:  "1.0",
					},
				}
				_, _ = w.Append(entry)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Final LSN should be numGoroutines * entriesPerGoroutine
	expectedFinal := uint64(numGoroutines * entriesPerGoroutine)
	assert.Equal(t, expectedFinal, w.LastLSN())
}
