package server_test

import (
	"testing"

	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/testutil/mysqltest"
	"github.com/linxun2025/exchange-project/internal/user/ledger"
	"github.com/linxun2025/exchange-project/internal/user/repository"
	"github.com/linxun2025/exchange-project/internal/user/server"
	"github.com/linxun2025/exchange-project/internal/user/service"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExactNotionalRPCs(t *testing.T) {
	db := mysqltest.Open(t)
	s := server.NewUserServer(nil, ledger.New(db, nil))
	ctx := t.Context()

	const (
		funds    = "999999999999999999.12345678"
		qty      = "0.00000001"
		reserved = "0.0000000200000001"
	)
	_, err := s.Deposit(ctx, &userpb.DepositRequest{UserId: 1, Asset: "USDT", Amount: funds, RefId: "buyer"})
	require.NoError(t, err)
	_, err = s.Deposit(ctx, &userpb.DepositRequest{UserId: 2, Asset: "BTC", Amount: qty, RefId: "seller"})
	require.NoError(t, err)

	freeze, err := s.Freeze(ctx, &userpb.FreezeRequest{UserId: 1, Asset: "USDT", Amount: reserved, OrderId: "cancelled-buy"})
	require.NoError(t, err)
	require.True(t, freeze.GetOk())
	assertRPCBalances(t, s, 1, [3]string{"USDT", "999999999999999999.1234567599999999", reserved})

	_, err = s.Unfreeze(ctx, &userpb.UnfreezeRequest{UserId: 1, Asset: "USDT", Amount: reserved, OrderId: "cancelled-buy"})
	require.NoError(t, err)
	assertRPCBalances(t, s, 1, [3]string{"USDT", funds, "0"})

	freeze, err = s.Freeze(ctx, &userpb.FreezeRequest{UserId: 1, Asset: "USDT", Amount: reserved, OrderId: "buy"})
	require.NoError(t, err)
	require.True(t, freeze.GetOk())
	freeze, err = s.Freeze(ctx, &userpb.FreezeRequest{UserId: 2, Asset: "BTC", Amount: qty, OrderId: "sell"})
	require.NoError(t, err)
	require.True(t, freeze.GetOk())
	_, err = s.SettleTrade(ctx, &userpb.SettleTradeRequest{
		TradeId: "trade", BaseAsset: "BTC", QuoteAsset: "USDT", BuyUserId: 1, SellUserId: 2,
		Price: "1.00000001", Quantity: qty, BuyOrderPrice: "2.00000001",
	})
	require.NoError(t, err)
	assertRPCBalances(t, s, 1, [3]string{"BTC", qty, "0"}, [3]string{"USDT", "999999999999999999.1234567699999999", "0"})
	assertRPCBalances(t, s, 2, [3]string{"USDT", "0.0000000100000001", "0"})
}

func TestFreezeUnfreezeAtStorageScale(t *testing.T) {
	db := mysqltest.Open(t)
	s := server.NewUserServer(nil, ledger.New(db, nil))
	ctx := t.Context()
	_, err := s.Deposit(ctx, &userpb.DepositRequest{UserId: 1, Asset: "USDT", Amount: "1", RefId: "fund"})
	require.NoError(t, err)

	const smallest = "0.000000000000000001"
	freeze, err := s.Freeze(ctx, &userpb.FreezeRequest{UserId: 1, Asset: "USDT", Amount: smallest, OrderId: "order"})
	require.NoError(t, err)
	require.True(t, freeze.GetOk())
	assertRPCBalances(t, s, 1, [3]string{"USDT", "0.999999999999999999", smallest})

	_, err = s.Unfreeze(ctx, &userpb.UnfreezeRequest{UserId: 1, Asset: "USDT", Amount: smallest, OrderId: "order"})
	require.NoError(t, err)
	assertRPCBalances(t, s, 1, [3]string{"USDT", "1", "0"})
}

func TestRPCRejectsAmountsOutsideSupportedScale(t *testing.T) {
	s := server.NewUserServer(nil, ledger.New(nil, nil))
	ctx := t.Context()
	for _, amount := range []string{"0", "-1", "0.0000000000000000001", "1000000000000000000"} {
		t.Run(amount, func(t *testing.T) {
			_, err := s.Freeze(ctx, &userpb.FreezeRequest{UserId: 1, Asset: "USDT", Amount: amount, OrderId: "buy"})
			require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
			_, err = s.Unfreeze(ctx, &userpb.UnfreezeRequest{UserId: 1, Asset: "USDT", Amount: amount, OrderId: "buy"})
			require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
		})
	}
	_, err := s.Deposit(ctx, &userpb.DepositRequest{UserId: 1, Asset: "USDT", Amount: "0.000000001", RefId: "deposit"})
	require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
}

func TestRPCRejectsInvalidSettlementBeforePosting(t *testing.T) {
	s := server.NewUserServer(nil, ledger.New(nil, nil))
	tests := []struct {
		name, price, qty, buyPrice string
	}{
		{"price over limit", "3", "2", "2"},
		{"notional outside column", "1", "2", "999999999999999999"},
		{"price scale", "1.123456789", "1", "2"},
		{"quantity scale", "1", "0.000000001", "2"},
		{"buy price scale", "1", "1", "1.123456789"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.SettleTrade(t.Context(), &userpb.SettleTradeRequest{
				TradeId: "trade", BaseAsset: "BTC", QuoteAsset: "USDT", BuyUserId: 1, SellUserId: 2,
				Price: tc.price, Quantity: tc.qty, BuyOrderPrice: tc.buyPrice,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
		})
	}
}

func TestLedgerRPCInsufficientBalanceStatuses(t *testing.T) {
	db := mysqltest.Open(t)
	s := server.NewUserServer(nil, ledger.New(db, nil))
	ctx := t.Context()

	frozen, err := s.Freeze(ctx, &userpb.FreezeRequest{UserId: 1, Asset: "USDT", Amount: "1", OrderId: "buy"})
	require.NoError(t, err)
	require.False(t, frozen.GetOk())
	require.Equal(t, "insufficient_balance", frozen.GetReason())
	_, err = s.Unfreeze(ctx, &userpb.UnfreezeRequest{UserId: 1, Asset: "USDT", Amount: "1", OrderId: "buy"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
}

func TestSettleTradeRPCRejectsBalanceCapacity(t *testing.T) {
	db := mysqltest.Open(t)
	s := server.NewUserServer(nil, ledger.New(db, nil))
	ctx := t.Context()

	for _, req := range []*userpb.DepositRequest{
		{UserId: 1, Asset: "USDT", Amount: "1", RefId: "fund-buyer"},
		{UserId: 2, Asset: "BTC", Amount: "1", RefId: "fund-seller"},
		{UserId: 2, Asset: "USDT", Amount: "999999999999999999.99999999", RefId: "fund-seller-quote"},
	} {
		_, err := s.Deposit(ctx, req)
		require.NoError(t, err)
	}
	for _, req := range []*userpb.FreezeRequest{
		{UserId: 1, Asset: "USDT", Amount: "1", OrderId: "buy"},
		{UserId: 2, Asset: "BTC", Amount: "1", OrderId: "sell"},
	} {
		res, err := s.Freeze(ctx, req)
		require.NoError(t, err)
		require.True(t, res.GetOk())
	}

	_, err := s.SettleTrade(ctx, &userpb.SettleTradeRequest{
		TradeId: "trade", BaseAsset: "BTC", QuoteAsset: "USDT", BuyUserId: 1, SellUserId: 2,
		Price: "1", Quantity: "1", BuyOrderPrice: "1",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
	require.Equal(t, ledger.ErrBalanceCapacity.Error(), status.Convert(err).Message())
}

func TestDepositCapacityLeavesRoomForUnfreeze(t *testing.T) {
	db := mysqltest.Open(t)
	s := server.NewUserServer(nil, ledger.New(db, nil))
	ctx := t.Context()

	const funds = "999999999999999999.99999999"
	_, err := s.Deposit(ctx, &userpb.DepositRequest{UserId: 1, Asset: "USDT", Amount: funds, RefId: "fund"})
	require.NoError(t, err)
	freeze, err := s.Freeze(ctx, &userpb.FreezeRequest{UserId: 1, Asset: "USDT", Amount: "1", OrderId: "order"})
	require.NoError(t, err)
	require.True(t, freeze.GetOk())

	// The credit fits in available alone but takes the total one storage unit over the limit.
	_, err = s.Deposit(ctx, &userpb.DepositRequest{UserId: 1, Asset: "USDT", Amount: "0.00000001", RefId: "credit"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
	assertRPCBalances(t, s, 1, [3]string{"USDT", "999999999999999998.99999999", "1"})
	var posted int64
	require.NoError(t, db.Model(&model.LedgerEntry{}).Where("ref_type = ? AND ref_id = ?", "deposit", "credit").Count(&posted).Error)
	require.Zero(t, posted)

	_, err = s.Unfreeze(ctx, &userpb.UnfreezeRequest{UserId: 1, Asset: "USDT", Amount: "1", OrderId: "order"})
	require.NoError(t, err)
	assertRPCBalances(t, s, 1, [3]string{"USDT", funds, "0"})
}

func TestDepositReplayRequiresSamePosting(t *testing.T) {
	db := mysqltest.Open(t)
	s := server.NewUserServer(nil, ledger.New(db, nil))
	ctx := t.Context()

	const refID = "fund"
	_, err := s.Deposit(ctx, &userpb.DepositRequest{UserId: 1, Asset: "USDT", Amount: "10.50", RefId: refID})
	require.NoError(t, err)

	for _, tc := range []struct {
		name   string
		userID int64
		asset  string
		amount string
	}{
		{name: "user", userID: 2, asset: "USDT", amount: "10.50"},
		{name: "asset", userID: 1, asset: "BTC", amount: "10.50"},
		{name: "amount", userID: 1, asset: "USDT", amount: "10.51"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, replayErr := s.Deposit(t.Context(), &userpb.DepositRequest{
				UserId: tc.userID, Asset: tc.asset, Amount: tc.amount, RefId: refID,
			})
			require.Equal(t, codes.AlreadyExists, status.Code(replayErr), "%v", replayErr)
			require.Nil(t, res)
		})
	}

	replay, err := s.Deposit(ctx, &userpb.DepositRequest{UserId: 1, Asset: "USDT", Amount: "10.5", RefId: refID})
	require.NoError(t, err)
	require.Equal(t, "USDT", replay.GetBalance().GetAsset())
	require.Equal(t, "10.5", replay.GetBalance().GetAvailable())
	assertRPCBalances(t, s, 1, [3]string{"USDT", "10.5", "0"})
	assertRPCBalances(t, s, 2)
	var posted int64
	require.NoError(t, db.Model(&model.LedgerEntry{}).Where("ref_type = ?", "deposit").Count(&posted).Error)
	require.EqualValues(t, 1, posted)
}

func TestAccountRPCs(t *testing.T) {
	db := mysqltest.Open(t)
	s := server.NewUserServer(service.NewUserService(repository.NewUserRepository(db)), ledger.New(db, nil))
	ctx := t.Context()
	account := &userpb.CreateUserRequest{Username: "trader1", Password: "password123", Email: "trader@example.test"}
	created, err := s.CreateUser(ctx, account)
	require.NoError(t, err)
	require.Positive(t, created.GetUser().GetId())
	assertRPCBalances(t, s, created.GetUser().GetId())

	user, err := s.GetUser(ctx, &userpb.GetUserRequest{UserId: created.GetUser().GetId()})
	require.NoError(t, err)
	require.Equal(t, account.GetUsername(), user.GetUser().GetUsername())
	require.Equal(t, account.GetEmail(), user.GetUser().GetEmail())
	_, err = s.GetUser(ctx, &userpb.GetUserRequest{UserId: created.GetUser().GetId() + 1})
	require.Equal(t, codes.NotFound, status.Code(err), "%v", err)

	_, err = s.CreateUser(ctx, account)
	require.Equal(t, codes.AlreadyExists, status.Code(err), "%v", err)
	login, err := s.Login(ctx, &userpb.LoginRequest{Username: account.GetUsername(), Password: account.GetPassword()})
	require.NoError(t, err)
	require.Equal(t, created.GetUser().GetId(), login.GetUserId())
	require.Equal(t, "trader", login.GetRole())
	_, err = s.Login(ctx, &userpb.LoginRequest{Username: account.GetUsername(), Password: "different"})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "%v", err)
}

func assertRPCBalances(t *testing.T, s *server.UserServer, userID int64, want ...[3]string) {
	t.Helper()
	res, err := s.GetBalances(t.Context(), &userpb.GetBalancesRequest{UserId: userID})
	require.NoError(t, err)
	require.Len(t, res.GetBalances(), len(want))
	for i, b := range res.GetBalances() {
		require.Equal(t, want[i], [3]string{b.GetAsset(), b.GetAvailable(), b.GetFrozen()})
	}
}
