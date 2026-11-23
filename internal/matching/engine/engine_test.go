package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/linxun2025/exchange-project/internal/matching/snapshot"
	"github.com/linxun2025/exchange-project/internal/matching/wal"
	"github.com/stretchr/testify/require"
)

const market = "BTC/USDT"

func testConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	return Config{
		WALDir: filepath.Join(root, "wal"), SnapshotDir: filepath.Join(root, "snapshot"),
		Symbols: []string{market}, Sync: wal.SyncNone, SnapshotInterval: time.Hour,
	}
}

func openMatcher(t *testing.T, cfg Config) *Matcher {
	t.Helper()
	m, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, m.Close(context.Background())) })
	return m
}

func submit(t *testing.T, m *Matcher, pair string, o book.Order) *SubmitResult {
	t.Helper()
	r, err := m.Submit(context.Background(), pair, o)
	require.NoError(t, err)
	return r
}

func order(id string, side book.Side, kind book.Kind, price, qty int64) book.Order {
	return book.Order{ID: id, UserID: 1, Side: side, Kind: kind, Price: price, Qty: qty}
}

func TestRecover_FullBook(t *testing.T) {
	ctx := t.Context()
	cfg := testConfig(t)
	m := openMatcher(t, cfg)
	var makers, takers []book.Order
	for i := range 105 {
		side, price := book.Buy, int64(50-i/2)
		if i >= 35 {
			side, price = book.Sell, int64(90+i/2)
		}
		makers = append(makers, order(fmt.Sprintf("maker-%03d", 105-i), side, book.Limit, price, 4))
	}
	for i := range 10 {
		// Consume a full FIFO maker and part of the next one in most commands.
		takers = append(takers, order(fmt.Sprintf("taker-%d", i), book.Buy, book.IOC, 200, 5))
	}
	for _, o := range makers {
		require.True(t, submit(t, m, market, o).Resting)
	}
	require.NoError(t, m.Snapshot(ctx, market))
	lsn, body, err := snapshot.Latest(symbolDir(cfg.SnapshotDir, market))
	require.NoError(t, err)
	require.EqualValues(t, 105, lsn)
	var captured snapshotBody
	require.NoError(t, json.Unmarshal(body, &captured))
	require.Len(t, captured.Orders, 105)

	live := make([]*SubmitResult, len(takers))
	for i, o := range takers {
		live[i] = submit(t, m, market, o)
		require.EqualValues(t, 5, live[i].Filled)
		for _, trade := range live[i].Trades {
			require.False(t, trade.At.IsZero())
		}
	}
	require.NoError(t, m.Close(ctx))
	before := m.actors[market].book.Resting() // Inspect complete state after the actor has exited.
	require.Len(t, before, 93)
	lsn, _, err = snapshot.Latest(symbolDir(cfg.SnapshotDir, market))
	require.NoError(t, err)
	require.EqualValues(t, 115, lsn, "shutdown must write the final snapshot")

	recovered := openMatcher(t, cfg)
	for i, o := range takers {
		require.Equal(t, live[i], submit(t, recovered, market, o))
	}
	depth, err := recovered.Depth(ctx, market, 200)
	require.NoError(t, err)
	require.EqualValues(t, 115, depth.LSN)
	require.NoError(t, recovered.Close(ctx))
	require.Equal(t, before, recovered.actors[market].book.Resting())

	t.Run("replay same persisted inputs including timestamp", func(t *testing.T) {
		replayCfg := testConfig(t)
		src := symbolDir(cfg.WALDir, market)
		dst := symbolDir(replayCfg.WALDir, market)
		require.NoError(t, os.MkdirAll(dst, 0700))
		files, err := os.ReadDir(src)
		require.NoError(t, err)
		require.NotEmpty(t, files)
		for _, file := range files {
			data, err := os.ReadFile(filepath.Join(src, file.Name()))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dst, file.Name()), data, 0600))
		}
		m := openMatcher(t, replayCfg)
		for i, o := range takers {
			require.Equal(t, live[i], submit(t, m, market, o))
		}
		require.NoError(t, m.Close(t.Context()))
		require.Equal(t, before, m.actors[market].book.Resting())
	})
	t.Run("fall back to the full snapshot and replay subsequent inputs", func(t *testing.T) {
		corruptSnapshot(t, symbolDir(cfg.SnapshotDir, market), 115)
		m := openMatcher(t, cfg)
		for i, o := range takers {
			require.Equal(t, live[i], submit(t, m, market, o))
		}
		require.NoError(t, m.Close(t.Context()))
		require.Equal(t, before, m.actors[market].book.Resting())
	})
	t.Run("fresh commands produce the same trade IDs", func(t *testing.T) {
		m := openMatcher(t, testConfig(t))
		for _, o := range makers {
			submit(t, m, market, o)
		}
		for i, o := range takers {
			r := submit(t, m, market, o)
			require.Len(t, r.Trades, len(live[i].Trades))
			for j, trade := range r.Trades {
				require.Equal(t, live[i].Trades[j].ID, trade.ID)
			}
		}
	})
}

func TestRecover_FallbackKeepsWALAfterOlderValidSnapshot(t *testing.T) {
	ctx := t.Context()
	cfg := testConfig(t)
	dir := symbolDir(cfg.WALDir, market)
	snapDir := symbolDir(cfg.SnapshotDir, market)
	m := openMatcher(t, cfg)
	submit(t, m, market, order("bid1", book.Buy, book.Limit, 100, 4))
	require.NoError(t, m.Snapshot(ctx, market))
	require.NoError(t, m.Close(ctx))
	require.FileExists(t, segment(dir, 1), "first snapshot must retain the untruncated WAL")
	// Reproduce the segment left after rotating before the next append. WAL
	// unit tests exercise rolling at the size threshold itself.
	require.NoError(t, os.WriteFile(segment(dir, 2), []byte("EXWAL002"), 0600))

	m = openMatcher(t, cfg)
	submit(t, m, market, order("bid2", book.Buy, book.Limit, 100, 3))
	require.NoError(t, m.Snapshot(ctx, market))
	require.NoError(t, m.Close(ctx))
	require.NoFileExists(t, segment(dir, 1), "two valid snapshots should trigger real WAL compaction")
	require.FileExists(t, segment(dir, 2))
	corruptSnapshot(t, snapDir, 2)
	require.NoError(t, os.WriteFile(segment(dir, 3), []byte("EXWAL002"), 0600))

	m = openMatcher(t, cfg)
	r := submit(t, m, market, order("sell", book.Sell, book.IOC, 100, 5))
	require.EqualValues(t, 3, r.LSN)
	require.Len(t, r.Trades, 2)
	require.Equal(t, "bid1", r.Trades[0].BuyOrderID)
	require.Equal(t, "bid2", r.Trades[1].BuyOrderID)
	require.NoError(t, m.Snapshot(ctx, market))
	require.NoError(t, m.Close(ctx))
	require.FileExists(t, segment(dir, 2), "WAL needed after the older valid snapshot must survive compaction")
	require.FileExists(t, filepath.Join(snapDir, "00000000000000000001.snap"))
	require.NoFileExists(t, filepath.Join(snapDir, "00000000000000000002.snap"))
	corruptSnapshot(t, snapDir, 3)

	m = openMatcher(t, cfg)
	require.Equal(t, r, submit(t, m, market, order("sell", book.Sell, book.IOC, 100, 5)))
	depth, err := m.Depth(ctx, market, 5)
	require.NoError(t, err)
	require.Equal(t, Depth{Bids: []book.Level{{Price: 100, Qty: 2, Orders: 1}}, Asks: []book.Level{}, LSN: 3}, depth)
	require.NoError(t, m.Close(ctx))
	// With the latest snapshot still unusable, a hole in the replay prefix
	// must fail startup even though the older snapshot has a nonzero LSN.
	corruptSnapshot(t, snapDir, 3)
	require.NoError(t, os.Remove(segment(dir, 2)))
	_, err = New(cfg)
	require.ErrorIs(t, err, wal.ErrCorrupt)
}

func TestMatcher_SubmitAndCancelReplayWithoutAdvancingWAL(t *testing.T) {
	ctx := t.Context()
	cfg := testConfig(t)
	m := openMatcher(t, cfg)
	maker := order("maker", book.Sell, book.Limit, 10, 4)
	taker := order("taker", book.Buy, book.Limit, 11, 2)
	require.True(t, submit(t, m, market, maker).Resting)
	first := submit(t, m, market, taker)
	require.Len(t, first.Trades, 1)
	second := submit(t, m, market, taker)
	require.Equal(t, first, second)
	second.Trades[0].ID = "changed by caller"
	require.Equal(t, first, submit(t, m, market, taker))
	c, err := m.Cancel(ctx, market, maker.ID)
	require.NoError(t, err)
	require.Equal(t, CancelResult{Found: true, Remaining: 2}, c)
	for _, id := range []string{maker.ID, "missing", taker.ID} {
		got, err := m.Cancel(ctx, market, id)
		require.NoError(t, err)
		require.Equal(t, id == maker.ID, got.Found)
	}
	d, err := m.Depth(ctx, market, 10)
	require.NoError(t, err)
	require.EqualValues(t, 3, d.LSN)
	require.NoError(t, m.Close(ctx))

	m = openMatcher(t, cfg)
	require.Equal(t, first, submit(t, m, market, taker))
	got, err := m.Cancel(ctx, market, maker.ID)
	require.NoError(t, err)
	require.Equal(t, c, got)
	d, err = m.Depth(ctx, market, 10)
	require.NoError(t, err)
	require.EqualValues(t, 3, d.LSN)
}

func TestMatcher_OverflowReturnsDurableFullRemainder(t *testing.T) {
	ctx := t.Context()
	cfg := testConfig(t)
	m := openMatcher(t, cfg)
	submit(t, m, market, order("large", book.Buy, book.Limit, 10, math.MaxInt64))
	overflow := order("overflow", book.Buy, book.Limit, 10, 1)
	r := submit(t, m, market, overflow)
	require.Equal(t, &SubmitResult{OrderID: "overflow", Remaining: 1, LSN: 2}, r)
	d, err := m.Depth(ctx, market, 5)
	require.NoError(t, err)
	require.Equal(t, []book.Level{{Price: 10, Qty: math.MaxInt64, Orders: 1}}, d.Bids)

	_, err = m.Cancel(ctx, market, "large")
	require.NoError(t, err)
	require.Equal(t, r, submit(t, m, market, overflow), "capacity became available after the first result")
	require.NoError(t, m.Close(ctx))
	// Force replay of the non-mutating command from WAL as well as a retry.
	require.NoError(t, os.RemoveAll(symbolDir(cfg.SnapshotDir, market)))
	m = openMatcher(t, cfg)
	require.Equal(t, r, submit(t, m, market, overflow))
	d, err = m.Depth(ctx, market, 5)
	require.NoError(t, err)
	require.Empty(t, d.Bids)
	require.EqualValues(t, 3, d.LSN)
}

func TestMatcher_TooManyFillsLeavesBookUnchangedAndReplays(t *testing.T) {
	ctx := t.Context()
	cfg := testConfig(t)
	m := openMatcher(t, cfg)
	const fillLimit = 512
	// The last two makers share a worse level, exercising a partial level
	// with sufficient volume but too many fills to fit the response budget.
	for i := range fillLimit + 2 {
		submit(t, m, market, order(fmt.Sprintf("bid-%03d", i), book.Buy, book.Limit, 11-int64(i/fillLimit), 1))
	}
	before, err := m.Depth(ctx, market, 2)
	require.NoError(t, err)
	require.Equal(t, []book.Level{{Price: 11, Qty: fillLimit, Orders: fillLimit}, {Price: 10, Qty: 2, Orders: 2}}, before.Bids)

	var inputs []book.Order
	var results []*SubmitResult
	for _, tc := range []struct {
		id     string
		kind   book.Kind
		price  int64
		reject book.Reject
	}{
		{"limit", book.Limit, 10, book.RejectNone},
		{"ioc", book.IOC, 10, book.RejectNone},
		{"fok", book.FOK, 10, book.RejectFOKUnfilled},
		{"market", book.Market, 0, book.RejectNoLiquidity},
	} {
		o := order(tc.id, book.Sell, tc.kind, tc.price, fillLimit+1)
		r := submit(t, m, market, o)
		require.Equal(t, &SubmitResult{OrderID: o.ID, Remaining: o.Qty, Reject: tc.reject, LSN: before.LSN + uint64(len(inputs)) + 1}, r)
		inputs = append(inputs, o)
		results = append(results, r)
	}
	after, err := m.Depth(ctx, market, 2)
	require.NoError(t, err)
	require.Equal(t, before.Bids, after.Bids)
	require.Empty(t, after.Asks)

	sweep := order("allowed-boundary", book.Sell, book.IOC, 10, fillLimit)
	filled := submit(t, m, market, sweep)
	require.Len(t, filled.Trades, fillLimit)
	require.EqualValues(t, fillLimit, filled.Filled)
	for i, o := range inputs {
		require.Equal(t, results[i], submit(t, m, market, o), "retry after liquidity changed")
	}
	require.NoError(t, m.Close(ctx))
	require.NoError(t, os.RemoveAll(symbolDir(cfg.SnapshotDir, market)))
	m = openMatcher(t, cfg)
	for i, o := range inputs {
		require.Equal(t, results[i], submit(t, m, market, o))
	}
	require.Equal(t, filled, submit(t, m, market, sweep))
	after, err = m.Depth(ctx, market, 2)
	require.NoError(t, err)
	require.Equal(t, []book.Level{{Price: 10, Qty: 2, Orders: 2}}, after.Bids)
	require.Equal(t, filled.LSN, after.LSN)
}

func TestMatcher_ResultCachePreservesLiveAndEvictsOldestFinal(t *testing.T) {
	cfg := testConfig(t)
	cfg.ResultCache = 2
	m := openMatcher(t, cfg)
	live := order("live", book.Buy, book.Limit, 10, 2)
	miss := func(id string) book.Order { return order(id, book.Buy, book.FOK, 10, 1) }
	first := submit(t, m, market, live)
	require.Equal(t, book.RejectFOKUnfilled, submit(t, m, market, miss("old")).Reject)
	recent := submit(t, m, market, miss("recent"))
	require.NoError(t, m.Close(t.Context()))
	m = openMatcher(t, cfg)
	latest := submit(t, m, market, miss("latest"))
	require.Equal(t, latest, submit(t, m, market, miss("latest")))
	require.Equal(t, recent, submit(t, m, market, miss("recent")))
	require.Equal(t, first, submit(t, m, market, live))
	require.EqualValues(t, 5, submit(t, m, market, miss("old")).LSN)
}

func TestMatcher_RejectsDuplicateMarketsBeforeOpeningWAL(t *testing.T) {
	cfg := testConfig(t)
	cfg.Symbols = []string{"btc-usdt", "BTC/USDT"}
	_, err := New(cfg)
	require.ErrorContains(t, err, "duplicate symbol")
	require.NoDirExists(t, cfg.WALDir)
}

func TestMatcher_RejectsUnconfiguredWALDirectory(t *testing.T) {
	cfg := testConfig(t)
	require.NoError(t, os.MkdirAll(symbolDir(cfg.WALDir, "ETH/USDT"), 0700))
	_, err := New(cfg)
	require.ErrorContains(t, err, "wal data for unconfigured symbol")
}

func TestMatcher_DistinctPairsHaveDistinctTradeIDs(t *testing.T) {
	cfg := testConfig(t)
	cfg.Symbols = []string{"AB/CDE", "ABC/DE"}
	m := openMatcher(t, cfg)
	var ids []string
	for _, symbol := range cfg.Symbols {
		submit(t, m, symbol, order("maker", book.Sell, book.Limit, 10, 1))
		r := submit(t, m, symbol, order("taker", book.Buy, book.IOC, 10, 1))
		require.Len(t, r.Trades, 1)
		ids = append(ids, r.Trades[0].ID)
	}
	require.Equal(t, []string{"AB/CDE-2-0", "ABC/DE-2-0"}, ids)
}

func TestMatcher_AutomaticSnapshots(t *testing.T) {
	for _, trigger := range []string{"record count", "timer"} {
		t.Run(trigger, func(t *testing.T) {
			cfg := testConfig(t)
			if trigger == "record count" {
				cfg.SnapshotEvery = 2
			} else {
				cfg.SnapshotInterval = 20 * time.Millisecond
			}
			m := openMatcher(t, cfg)
			dir := symbolDir(cfg.SnapshotDir, market)
			submit(t, m, market, order("one", book.Sell, book.Limit, 10, 1))
			target := uint64(1)
			if trigger == "record count" {
				require.NoDirExists(t, dir)
				submit(t, m, market, order("two", book.Sell, book.Limit, 10, 1))
				target = 2
			}
			require.Eventually(t, func() bool {
				lsn, _, err := snapshot.Latest(dir)
				return err == nil && lsn == target
			}, 5*time.Second, 5*time.Millisecond)
		})
	}
}

func TestMatcher_SnapshotFailurePreservesWALAndCloses(t *testing.T) {
	ctx := t.Context()
	cfg := testConfig(t)
	m, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	submit(t, m, market, order("maker", book.Sell, book.Limit, 10, 2))
	require.NoError(t, os.MkdirAll(cfg.SnapshotDir, 0700))
	dir := symbolDir(cfg.SnapshotDir, market)
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0600))
	require.Error(t, m.Snapshot(ctx, market))
	taker := order("taker", book.Buy, book.IOC, 10, 1)
	first := submit(t, m, market, taker)
	require.Len(t, first.Trades, 1)
	require.Error(t, m.Close(ctx))

	require.NoError(t, os.Remove(dir))
	recovered := openMatcher(t, cfg)
	require.Equal(t, first, submit(t, recovered, market, taker))
	d, err := recovered.Depth(ctx, market, 10)
	require.NoError(t, err)
	require.Equal(t, []book.Level{{Price: 10, Qty: 1, Orders: 1}}, d.Asks)
	require.EqualValues(t, 2, d.LSN)
}

func TestMatcher_CloseDrainsAcceptedCommandsAndFinalSnapshot(t *testing.T) {
	cfg := testConfig(t)
	m, err := recoverMatcher(cfg)
	require.NoError(t, err)
	a := m.actors[market]
	ordered := []command{
		{op: opSubmit, order: order("maker", book.Sell, book.Limit, 10, 3), reply: make(chan response, 1)},
		{op: opSubmit, order: order("taker", book.Buy, book.Limit, 10, 1), reply: make(chan response, 1)},
		{op: opSnapshot, reply: make(chan response, 1)},
		{op: opCancel, order: book.Order{ID: "maker"}, reply: make(chan response, 1)},
		{op: opDepth, depth: 10, reply: make(chan response, 1)},
	}
	for _, cmd := range ordered {
		require.NoError(t, m.enqueue(t.Context(), a, cmd))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, m.Close(ctx), context.Canceled)
	_, err = m.Depth(t.Context(), market, 10)
	require.ErrorIs(t, err, ErrClosed)
	m.start()
	ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, m.Close(ctx))
	require.NoError(t, m.Close(ctx))
	for _, cmd := range ordered {
		select {
		case r := <-cmd.reply:
			require.NoError(t, r.err)
		case <-ctx.Done():
			t.Fatalf("shutdown lost reply for operation %d", cmd.op)
		}
	}
	lsn, _, err := snapshot.Latest(symbolDir(cfg.SnapshotDir, market))
	require.NoError(t, err)
	require.EqualValues(t, 3, lsn)
	m = openMatcher(t, cfg)
	d, err := m.Depth(ctx, market, 10)
	require.NoError(t, err)
	require.Empty(t, d.Asks)
	require.EqualValues(t, 3, d.LSN)
}

func TestMatcher_FailStopsOnWALRotationWriteError(t *testing.T) {
	dir := os.Getenv("MATCHING_FAIL_STOP_WAL_TEST")
	if dir != "" {
		cfg := Config{WALDir: filepath.Join(dir, "wal"), SnapshotDir: filepath.Join(dir, "snap"), Symbols: []string{market}}
		path := symbolDir(cfg.WALDir, market)
		w, err := wal.Open(path, wal.SyncFsync)
		require.NoError(t, err)
		// Full 1 MiB payloads fill a real 64 MiB segment in 64 writes.
		for i := range 64 {
			data, err := json.Marshal(submitRec{O: fmt.Sprintf("maker-%d", i), U: 1, S: book.Sell, K: book.Limit, P: 11, Q: 1, T: 1})
			require.NoError(t, err)
			data = append(data, bytes.Repeat([]byte{' '}, 1<<20-len(data))...)
			_, err = w.Append(wal.RecordSubmit, data)
			require.NoError(t, err)
		}
		require.NoError(t, w.Close())
		m := openMatcher(t, cfg)
		require.NoError(t, os.Rename(path, filepath.Join(dir, "moved")))
		// Prevent the rolling writer from creating its next segment.
		require.NoError(t, os.WriteFile(path, []byte("not a directory"), 0600))
		_, err = m.Submit(t.Context(), market, order("taker", book.Buy, book.Limit, 11, 1))
		require.ErrorIs(t, err, ErrClosed)
		// Receiving ErrClosed must be followed by process termination.
		<-time.After(5 * time.Second)
		t.Fatal("actor continued running after WAL I/O failure")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMatcher_FailStopsOnWALRotationWriteError$")
	cmd.Env = append(os.Environ(), "MATCHING_FAIL_STOP_WAL_TEST="+t.TempDir())
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "%s", out)
	require.Equal(t, 1, exit.ExitCode(), "%s", out)
	require.Contains(t, string(out), `"level":"fatal"`, "%s", out)
	require.Contains(t, string(out), "wal append failed", "%s", out)
}

func corruptSnapshot(t *testing.T, dir string, lsn uint64) {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("%020d.snap", lsn))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data[8] ^= 1
	require.NoError(t, os.WriteFile(path, data, 0600))
}

func segment(dir string, first uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d.wal", first))
}
