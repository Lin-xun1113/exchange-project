package book_test

import (
	"math"
	"testing"

	"github.com/linxun2025/exchange-project/internal/matching/book"
	"github.com/stretchr/testify/require"
)

func order(id string, user int64, side book.Side, kind book.Kind, price, qty int64) book.Order {
	return book.Order{ID: id, UserID: user, Side: side, Kind: kind, Price: price, Qty: qty}
}

func TestBook_PriceTimePriority(t *testing.T) {
	b := book.New()
	// The adjacent prices above 2^53 cannot be distinguished as float64.
	const price = 1<<53 + 3
	b.Submit(order("worse", 1, book.Sell, book.Limit, price+1, 4))
	b.Submit(order("S1", 2, book.Sell, book.Limit, price, 2))
	b.Submit(order("S2", 3, book.Sell, book.Limit, price, 3))

	r := b.Submit(order("buy", 2, book.Buy, book.Limit, price, 2))
	require.Equal(t, book.Result{
		Filled: 2,
		Fills:  []book.Fill{{MakerID: "S1", MakerUserID: 2, Price: price, Qty: 2}},
	}, r)
	require.False(t, b.Has("S1"))
	require.Equal(t, []book.Resting{
		{ID: "S2", UserID: 3, Side: book.Sell, Price: price, Remaining: 3},
		{ID: "worse", UserID: 1, Side: book.Sell, Price: price + 1, Remaining: 4},
	}, b.Resting())
}

func TestBook_FOKFailurePreservesEntireBook(t *testing.T) {
	b := book.New()
	b.Submit(order("maker", 1, book.Sell, book.Limit, 20, 1))
	b.Submit(order("too-far", 1, book.Sell, book.Limit, 21, 10))
	before := b.Resting()

	r := b.Submit(order("fok", 2, book.Buy, book.FOK, 20, 2))
	require.Equal(t, book.Result{Remaining: 2, Reject: book.RejectFOKUnfilled}, r)
	require.Equal(t, before, b.Resting())
}

func TestBook_FOKAcrossLevelsUsesMakerPrices(t *testing.T) {
	b := book.New()
	b.Submit(order("bid-1", 1, book.Buy, book.Limit, 22, 2))
	b.Submit(order("bid-2", 2, book.Buy, book.Limit, 21, 3))
	b.Submit(order("bid-3", 3, book.Buy, book.Limit, 20, 4))

	r := b.Submit(order("fok", 4, book.Sell, book.FOK, 21, 4))
	require.Equal(t, book.Result{Filled: 4, Fills: []book.Fill{
		{MakerID: "bid-1", MakerUserID: 1, Price: 22, Qty: 2},
		{MakerID: "bid-2", MakerUserID: 2, Price: 21, Qty: 2},
	}}, r)
	bids, asks := b.Depth(5)
	require.Equal(t, []book.Level{{Price: 21, Qty: 1, Orders: 1}, {Price: 20, Qty: 4, Orders: 1}}, bids)
	require.Empty(t, asks)
}

func TestBook_MarketSellWithoutLiquidity(t *testing.T) {
	b := book.New()
	b.Submit(order("ask", 1, book.Sell, book.Limit, 10, 3))
	r := b.Submit(order("market", 2, book.Sell, book.Market, 0, 2))
	require.Equal(t, book.Result{Remaining: 2, Reject: book.RejectNoLiquidity}, r)
	require.Equal(t, 1, b.Len())
}

func TestBook_NonRestingRemainders(t *testing.T) {
	for _, tt := range []struct {
		name string
		kind book.Kind
	}{
		{"market", book.Market},
		{"ioc", book.IOC},
		{"limit", book.Limit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := book.New()
			b.Submit(order("bid", 1, book.Buy, book.Limit, 20, 1))
			r := b.Submit(order("sell", 2, book.Sell, tt.kind, 20, 2))
			require.Equal(t, book.Result{
				Filled: 1, Remaining: 1, Resting: tt.kind == book.Limit,
				Fills: []book.Fill{{MakerID: "bid", MakerUserID: 1, Price: 20, Qty: 1}},
			}, r)
			require.Equal(t, tt.kind == book.Limit, b.Has("sell"))
		})
	}
}

func TestBook_CancelUpdatesLevelAndPreservesFIFO(t *testing.T) {
	b := book.New()
	b.Submit(order("first", 1, book.Buy, book.Limit, 10, 1))
	b.Submit(order("middle", 2, book.Buy, book.Limit, 10, 2))
	b.Submit(order("last", 3, book.Buy, book.Limit, 10, 3))
	b.Submit(order("best", 4, book.Buy, book.Limit, 11, 4))

	remaining, ok := b.Cancel("middle")
	require.True(t, ok)
	require.EqualValues(t, 2, remaining)
	_, ok = b.Cancel("middle")
	require.False(t, ok)
	bids, _ := b.Depth(1)
	require.Equal(t, []book.Level{{Price: 11, Qty: 4, Orders: 1}}, bids)
	bids[0].Qty = 0
	bids, _ = b.Depth(2)
	require.Equal(t, []book.Level{{Price: 11, Qty: 4, Orders: 1}, {Price: 10, Qty: 4, Orders: 2}}, bids)

	r := b.Submit(order("sweep", 5, book.Sell, book.Market, 99, 6))
	require.Equal(t, []book.Fill{
		{MakerID: "best", MakerUserID: 4, Price: 11, Qty: 4},
		{MakerID: "first", MakerUserID: 1, Price: 10, Qty: 1},
		{MakerID: "last", MakerUserID: 3, Price: 10, Qty: 1},
	}, r.Fills)
	require.Equal(t, []book.Resting{{ID: "last", UserID: 3, Side: book.Buy, Price: 10, Remaining: 2}}, b.Resting())
}

func TestBook_RestoreIsAtomicAndPreservesFIFO(t *testing.T) {
	b := book.New()
	orders := []book.Resting{
		{ID: "bid", UserID: 1, Side: book.Buy, Price: 9, Remaining: 1},
		{ID: "z", UserID: 2, Side: book.Sell, Price: 10, Remaining: 2},
		{ID: "a", UserID: 3, Side: book.Sell, Price: 10, Remaining: 3},
	}
	require.Error(t, b.Restore(append(orders, orders[0])))
	require.Zero(t, b.Len())
	require.NoError(t, b.Restore(orders))
	require.Equal(t, orders, b.Resting())
	require.Error(t, b.Restore(nil))
	r := b.Submit(order("taker", 4, book.Buy, book.IOC, 10, 2))
	require.Equal(t, []book.Fill{{MakerID: "z", MakerUserID: 2, Price: 10, Qty: 2}}, r.Fills)
}

func TestBook_RejectsAggregateOverflow(t *testing.T) {
	b := book.New()
	b.Submit(order("large", 1, book.Buy, book.Limit, 10, math.MaxInt64-1))
	require.NoError(t, b.CheckSubmit(order("last-tick", 2, book.Buy, book.Limit, 10, 1)))
	b.Submit(order("last-tick", 2, book.Buy, book.Limit, 10, 1))
	tooLarge := order("overflow", 3, book.Buy, book.Limit, 10, 1)
	require.ErrorIs(t, b.CheckSubmit(tooLarge), book.ErrLevelOverflow)
	bids, _ := b.Depth(1)
	require.Equal(t, []book.Level{{Price: 10, Qty: math.MaxInt64, Orders: 2}}, bids)
	require.ErrorIs(t, book.New().Restore(append(b.Resting(), book.Resting{
		ID: "overflow", UserID: 3, Side: book.Buy, Price: 10, Remaining: 1,
	})), book.ErrLevelOverflow)
}

func TestBook_FOKLiquidityAvoidsOverflowAcrossLevels(t *testing.T) {
	b := book.New()
	b.Submit(order("best", 1, book.Sell, book.Limit, 10, math.MaxInt64-1))
	b.Submit(order("next", 2, book.Sell, book.Limit, 11, math.MaxInt64))
	r := b.Submit(order("fok", 3, book.Buy, book.FOK, 11, math.MaxInt64))
	require.EqualValues(t, math.MaxInt64, r.Filled)
	require.Zero(t, r.Remaining)
	require.Len(t, r.Fills, 2)
	require.EqualValues(t, 1, r.Fills[1].Qty)
}
