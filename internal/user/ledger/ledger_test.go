package ledger_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/testutil/mysqltest"
	"github.com/linxun2025/exchange-project/internal/user/ledger"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDepositCapsIssuanceAcrossUsers(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	const funds = "999999999999999999.99999998"
	const lastTick = "0.00000001"
	_, err := l.Deposit(ctx, 1, "USDT", amount(funds), "fund")
	require.NoError(t, err)

	// The credit fits in user 2's balance but exceeds the shared capacity.
	_, err = l.Deposit(ctx, 2, "USDT", amount("0.00000002"), "top-up")
	require.ErrorIs(t, err, ledger.ErrBalanceCapacity)
	assertBalances(t, l, 2)
	require.EqualValues(t, 1, entryCount(t, db, "deposit"))

	// The failed deposit does not claim the reference or the remaining capacity.
	_, err = l.Deposit(ctx, 2, "USDT", amount(lastTick), "top-up")
	require.NoError(t, err)
	replay, err := l.Deposit(ctx, 2, "USDT", amount(lastTick), "top-up")
	require.NoError(t, err, "replay at capacity must still succeed")
	require.Equal(t, lastTick, replay.Available.String())
	_, err = l.Deposit(ctx, 1, "USDT", amount(lastTick), "top-up")
	require.ErrorIs(t, err, ledger.ErrDepositConflict)

	assertBalances(t, l, 1, [3]string{"USDT", funds, "0"})
	assertBalances(t, l, 2, [3]string{"USDT", lastTick, "0"})
	require.EqualValues(t, 2, entryCount(t, db, "deposit"))
	assertSupply(t, db, "USDT", "999999999999999999.99999999")
}

func TestConcurrentDepositsDeduplicateAtAssetCapacity(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()

	// From a fresh asset, one credit fits and two would exceed the cap.
	const funds = "500000000000000000"
	const calls = 4
	type outcome struct {
		userID int64
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, calls)
	for i := range calls {
		userID := int64(i%2 + 1)
		go func() {
			<-start
			_, err := l.Deposit(ctx, userID, "USDT", amount(funds), fmt.Sprintf("fund-%d", userID))
			results <- outcome{userID, err}
		}()
	}
	close(start)
	var outcomes [calls]outcome
	for i := range outcomes {
		outcomes[i] = <-results
	}

	var winner int64
	var successes int
	for _, result := range outcomes {
		if result.err != nil {
			require.ErrorIs(t, result.err, ledger.ErrBalanceCapacity)
			continue
		}
		if winner == 0 {
			winner = result.userID
		}
		require.Equal(t, winner, result.userID, "only one user can receive the credit")
		successes++
	}
	require.Equal(t, 2, successes, "the winning deposit and its replay must both succeed")
	assertBalances(t, l, winner, [3]string{"USDT", funds, "0"})
	assertBalances(t, l, 3-winner)
	require.EqualValues(t, 1, entryCount(t, db, "deposit"))
	assertSupply(t, db, "USDT", funds)
}

func TestDepositRollsBackIssuanceWhenBalanceWriteFails(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	const funds = "999999999999999999.99999999"
	const refID = "fund"
	require.NoError(t, db.Exec(`ALTER TABLE balances ADD CONSTRAINT reject_test_user CHECK (user_id <> 1)`).Error)
	_, err := l.Deposit(ctx, 1, "USDT", amount(funds), refID)
	require.Error(t, err)
	require.EqualValues(t, 0, entryCount(t, db, "deposit"))
	assertBalances(t, l, 1)

	require.NoError(t, db.Exec(`ALTER TABLE balances DROP CHECK reject_test_user`).Error)
	_, err = l.Deposit(ctx, 1, "USDT", amount(funds), refID)
	require.NoError(t, err, "the failed balance write must leave issuance capacity available")
	assertBalances(t, l, 1, [3]string{"USDT", funds, "0"})
	assertSupply(t, db, "USDT", funds)
}

func TestConcurrentFreezeCannotOverdraw(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	_, err := l.Deposit(ctx, 1, "USDT", amount("100"), "fund")
	require.NoError(t, err)

	const count = 20
	start := make(chan struct{})
	results := make(chan error, count)
	for i := range count {
		go func() {
			<-start
			results <- l.Freeze(ctx, 1, "USDT", amount("10"), fmt.Sprintf("order-%d", i))
		}()
	}
	close(start)

	var success, insufficient int
	for range count {
		switch err := <-results; {
		case err == nil:
			success++
		case errors.Is(err, ledger.ErrInsufficientBalance):
			insufficient++
		default:
			t.Errorf("unexpected freeze error: %v", err)
		}
	}
	require.Equal(t, 10, success)
	require.Equal(t, 10, insufficient)
	assertBalances(t, l, 1, [3]string{"USDT", "0", "100"})
	var posted int64
	require.NoError(t, db.Model(&model.LedgerEntry{}).Where("ref_type = ? AND delta_frozen > 0", "freeze").Count(&posted).Error)
	require.EqualValues(t, 10, posted)
	require.EqualValues(t, count, entryCount(t, db, "freeze"))
}

func TestRejectedFreezeStaysRejectedAfterDeposit(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	requested := amount("3")

	require.ErrorIs(t, l.Freeze(ctx, 1, "USDT", requested, "rejected"), ledger.ErrInsufficientBalance)
	var rows int64
	require.NoError(t, db.Model(&model.Balance{}).Count(&rows).Error)
	require.Zero(t, rows, "denial keeps its ledger decision only")

	_, err := l.Deposit(ctx, 1, "USDT", amount("10"), "fund")
	require.NoError(t, err)
	require.ErrorIs(t, l.Freeze(ctx, 1, "USDT", requested, "rejected"), ledger.ErrInsufficientBalance)
	assertBalances(t, l, 1, [3]string{"USDT", "10", "0"})

	require.NoError(t, l.Freeze(ctx, 1, "USDT", requested, "fresh"))
	require.NoError(t, l.Freeze(ctx, 1, "USDT", requested, "fresh"))
	assertBalances(t, l, 1, [3]string{"USDT", "7", "3"})
	require.EqualValues(t, 2, entryCount(t, db, "freeze"))
}

func TestRejectedFreezeAtCapacityStaysRejectedAfterRelease(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	const funds = "999999999999999999"
	_, err := l.Deposit(ctx, 1, "USDT", amount(funds), "fund")
	require.NoError(t, err)
	require.NoError(t, l.Freeze(ctx, 1, "USDT", amount(funds), "all"))

	// The second freeze lacks available funds and would also overflow frozen.
	require.ErrorIs(t, l.Freeze(ctx, 1, "USDT", amount("1"), "rejected"), ledger.ErrInsufficientBalance)
	assertBalances(t, l, 1, [3]string{"USDT", "0", funds})
	var decision model.LedgerEntry
	require.NoError(t, db.Where("ref_type = ? AND ref_id = ? AND leg = 0", "freeze", "rejected").Take(&decision).Error)
	require.True(t, decision.DeltaAvailable.IsZero())
	require.True(t, decision.DeltaFrozen.IsZero())

	require.NoError(t, l.Unfreeze(ctx, 1, "USDT", amount(funds), "all"))
	require.ErrorIs(t, l.Freeze(ctx, 1, "USDT", amount("1"), "rejected"), ledger.ErrInsufficientBalance)
	assertBalances(t, l, 1, [3]string{"USDT", funds, "0"})
	require.NoError(t, l.Freeze(ctx, 1, "USDT", amount("1"), "fresh"))
	assertBalances(t, l, 1, [3]string{"USDT", "999999999999999998", "1"})
}

func TestSettleTradePostsOnce(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	_, err := l.Deposit(ctx, 1, "USDT", amount("30"), "fund-buyer")
	require.NoError(t, err)
	_, err = l.Deposit(ctx, 2, "BTC", amount("4"), "fund-seller")
	require.NoError(t, err)
	require.NoError(t, l.Freeze(ctx, 1, "USDT", amount("24"), "buy"))
	require.NoError(t, l.Freeze(ctx, 2, "BTC", amount("2"), "sell"))

	settlement := trade(1, 2)
	require.NoError(t, l.SettleTrade(ctx, settlement))
	require.NoError(t, l.SettleTrade(ctx, settlement))

	assertBalances(t, l, 1, [3]string{"BTC", "2", "0"}, [3]string{"USDT", "10", "0"})
	assertBalances(t, l, 2, [3]string{"BTC", "2", "0"}, [3]string{"USDT", "20", "0"})
	require.EqualValues(t, 4, entryCount(t, db, "trade"))
}

func TestUnfreezeUnderflowRollsBackEntry(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	_, err := l.Deposit(ctx, 1, "USDT", amount("10"), "fund")
	require.NoError(t, err)
	require.NoError(t, l.Freeze(ctx, 1, "USDT", amount("3"), "buy"))

	require.ErrorIs(t, l.Unfreeze(ctx, 1, "USDT", amount("4"), "buy"), ledger.ErrInsufficientBalance)
	assertBalances(t, l, 1, [3]string{"USDT", "7", "3"})
	require.Zero(t, entryCount(t, db, "unfreeze"))

	require.NoError(t, l.Unfreeze(ctx, 1, "USDT", amount("3"), "buy"))
	require.NoError(t, l.Unfreeze(ctx, 1, "USDT", amount("3"), "buy"))
	assertBalances(t, l, 1, [3]string{"USDT", "10", "0"})
	require.EqualValues(t, 1, entryCount(t, db, "unfreeze"))
}

func TestSettlementUnderflowRollsBackAllLegs(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	_, err := l.Deposit(ctx, 1, "USDT", amount("30"), "fund-buyer")
	require.NoError(t, err)
	require.NoError(t, l.Freeze(ctx, 1, "USDT", amount("24"), "buy"))

	require.ErrorIs(t, l.SettleTrade(ctx, trade(1, 2)), ledger.ErrInsufficientBalance)
	assertBalances(t, l, 1, [3]string{"USDT", "6", "24"})
	assertBalances(t, l, 2)
	require.Zero(t, entryCount(t, db, "trade"))

	var rows int64
	require.NoError(t, db.Model(&model.Balance{}).Count(&rows).Error)
	require.EqualValues(t, 1, rows, "the transaction must also roll back newly seeded rows")
}

func TestSettlementPreservesIssuanceAtAssetCapacity(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	// Seed sub-tick dust to reach the exact storage maximum with scale-8 deposits.
	const dust = "0.000000009999999999"
	const quoteSupply = "999999999999999999.999999999999999999"
	const sellerQuote = "999999999999999969.99999999"
	require.NoError(t, db.Exec(`INSERT INTO asset_supply (asset, total_issued) VALUES ('USDT', ?)`, dust).Error)
	require.NoError(t, db.Exec(`INSERT INTO balances (user_id, asset, available) VALUES (3, 'USDT', ?)`, dust).Error)
	require.NoError(t, db.Exec(`INSERT INTO ledger_entries
		(ref_type, ref_id, leg, user_id, asset, delta_available, delta_frozen)
		VALUES ('deposit', 'dust', 0, 3, 'USDT', ?, 0)`, dust).Error)

	_, err := l.Deposit(ctx, 1, "USDT", amount("30"), "fund-buyer")
	require.NoError(t, err)
	_, err = l.Deposit(ctx, 2, "BTC", amount("4"), "fund-seller")
	require.NoError(t, err)
	_, err = l.Deposit(ctx, 2, "USDT", amount(sellerQuote), "fund-seller-quote")
	require.NoError(t, err)
	assertSupply(t, db, "USDT", quoteSupply)
	require.NoError(t, l.Freeze(ctx, 1, "USDT", amount("24"), "buy"))
	require.NoError(t, l.Freeze(ctx, 2, "BTC", amount("2"), "sell"))
	require.NoError(t, l.Freeze(ctx, 2, "USDT", amount("20"), "reserve-quote"))

	require.NoError(t, l.SettleTrade(ctx, trade(1, 2)))
	assertBalances(t, l, 1, [3]string{"BTC", "2", "0"}, [3]string{"USDT", "10", "0"})
	assertBalances(t, l, 2, [3]string{"BTC", "2", "0"}, [3]string{"USDT", sellerQuote, "20"})
	require.EqualValues(t, 4, entryCount(t, db, "trade"))

	require.NoError(t, l.Unfreeze(ctx, 2, "USDT", amount("20"), "reserve-quote"))
	assertBalances(t, l, 2, [3]string{"BTC", "2", "0"}, [3]string{"USDT", "999999999999999989.99999999", "0"})
	assertBalances(t, l, 3, [3]string{"USDT", dust, "0"})
	assertSupply(t, db, "USDT", quoteSupply)
	assertSupply(t, db, "BTC", "4")
}

func TestSelfTradeKeepsFourDistinctLegs(t *testing.T) {
	db := mysqltest.Open(t)
	l := ledger.New(db, nil)
	ctx := t.Context()
	// Near capacity, the matching base debit must make room for the self-trade credit.
	const baseFunds = "999999999999999999.99999999"
	_, err := l.Deposit(ctx, 1, "USDT", amount("30"), "fund-quote")
	require.NoError(t, err)
	_, err = l.Deposit(ctx, 1, "BTC", amount(baseFunds), "fund-base")
	require.NoError(t, err)
	require.NoError(t, l.Freeze(ctx, 1, "USDT", amount("24"), "buy"))
	require.NoError(t, l.Freeze(ctx, 1, "BTC", amount("2"), "sell"))

	require.NoError(t, l.SettleTrade(ctx, trade(1, 1)))
	assertBalances(t, l, 1, [3]string{"BTC", baseFunds, "0"}, [3]string{"USDT", "30", "0"})
	var entries []model.LedgerEntry
	require.NoError(t, db.Where("ref_type = ? AND ref_id = ?", "trade", "match").Order("leg").Find(&entries).Error)
	want := [][3]string{{"USDT", "4", "-24"}, {"BTC", "2", "0"}, {"BTC", "0", "-2"}, {"USDT", "20", "0"}}
	require.Len(t, entries, len(want))
	for i, entry := range entries {
		require.Equal(t, i, entry.Leg)
		require.EqualValues(t, 1, entry.UserID)
		require.Equal(t, want[i], [3]string{entry.Asset, entry.DeltaAvailable.String(), entry.DeltaFrozen.String()})
	}
}

func trade(buyer, seller int64) ledger.Settlement {
	return ledger.Settlement{
		TradeID: "match", Base: "BTC", Quote: "USDT", BuyUserID: buyer, SellUserID: seller,
		Price: amount("10"), Qty: amount("2"), BuyOrderPrice: amount("12"),
	}
}

func assertBalances(t *testing.T, l *ledger.Ledger, userID int64, want ...[3]string) {
	t.Helper()
	balances, err := l.Balances(t.Context(), userID)
	require.NoError(t, err)
	require.Len(t, balances, len(want))
	for i, b := range balances {
		require.Equal(t, want[i], [3]string{b.Asset, b.Available.String(), b.Frozen.String()})
	}
}

func entryCount(t *testing.T, db *gorm.DB, refType string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.LedgerEntry{}).Where("ref_type = ?", refType).Count(&count).Error)
	return count
}

func assertSupply(t *testing.T, db *gorm.DB, asset, want string) {
	t.Helper()
	var issued decimal.Decimal
	require.NoError(t, db.WithContext(t.Context()).Raw("SELECT total_issued FROM asset_supply WHERE asset = ?", asset).Row().Scan(&issued))
	require.Equal(t, want, issued.String())
}

func amount(s string) decimal.Decimal { return decimal.RequireFromString(s) }
