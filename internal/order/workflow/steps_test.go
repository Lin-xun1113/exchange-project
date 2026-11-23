package workflow_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/server"
	"github.com/linxun2025/exchange-project/internal/order/service"
	"github.com/linxun2025/exchange-project/internal/order/workflow"
	"github.com/linxun2025/exchange-project/internal/testutil/mysqltest"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

const market = "BTC/USDT"

type fakeUser struct {
	userpb.UserServiceClient
	freeze   func(*userpb.FreezeRequest) *userpb.FreezeResponse
	settled  chan *userpb.SettleTradeRequest
	released chan *userpb.UnfreezeRequest
}

func (f *fakeUser) Freeze(_ context.Context, req *userpb.FreezeRequest, _ ...grpc.CallOption) (*userpb.FreezeResponse, error) {
	if f.freeze != nil {
		return f.freeze(req), nil
	}
	return &userpb.FreezeResponse{Ok: true}, nil
}

func (f *fakeUser) SettleTrade(ctx context.Context, req *userpb.SettleTradeRequest, _ ...grpc.CallOption) (*userpb.SettleTradeResponse, error) {
	select {
	case f.settled <- req:
		return &userpb.SettleTradeResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeUser) Unfreeze(ctx context.Context, req *userpb.UnfreezeRequest, _ ...grpc.CallOption) (*userpb.UnfreezeResponse, error) {
	select {
	case f.released <- req:
		return &userpb.UnfreezeResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type fakeMatching struct {
	matchingpb.MatchingServiceClient
	submit func(context.Context, *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error)
	cancel func(context.Context, *matchingpb.CancelOrderRequest) (*matchingpb.CancelOrderResponse, error)
}

func (f *fakeMatching) SubmitOrder(ctx context.Context, req *matchingpb.SubmitOrderRequest, _ ...grpc.CallOption) (*matchingpb.SubmitOrderResponse, error) {
	return f.submit(ctx, req)
}

func (f *fakeMatching) CancelOrder(ctx context.Context, req *matchingpb.CancelOrderRequest, _ ...grpc.CallOption) (*matchingpb.CancelOrderResponse, error) {
	return f.cancel(ctx, req)
}

func startWorker(t *testing.T, w *workflow.Worker) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		stop()
		require.NoError(t, receive(t, done))
	})
	return stop, done
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(7 * time.Second):
		t.Fatal("timed out waiting for workflow event")
		var zero T
		return zero
	}
}

func awaitOutbox(t *testing.T, db *gorm.DB, orderID, step, want string) workflow.Entry {
	t.Helper()
	var e workflow.Entry
	require.Eventually(t, func() bool {
		return db.Where("aggregate_id = ? AND step = ? AND status = ?", orderID, step, want).Find(&e).RowsAffected == 1
	}, 7*time.Second, 10*time.Millisecond, "%s for order %s did not reach %s", step, orderID, want)
	return e
}

func create(t *testing.T, api *server.OrderServer, user int64, key string, side orderpb.OrderSide, kind orderpb.OrderType, price, qty string) *orderpb.Order {
	t.Helper()
	res, err := api.CreateOrder(t.Context(), &orderpb.CreateOrderRequest{
		UserId: user, Symbol: market, Side: side, Type: kind, Price: price, Quantity: qty, IdempotencyKey: key,
	})
	require.NoError(t, err)
	require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_PENDING, res.Order.Status)
	return res.Order
}

func readOrder(t *testing.T, api *server.OrderServer, o *orderpb.Order) *orderpb.Order {
	t.Helper()
	res, err := api.GetOrder(t.Context(), &orderpb.GetOrderRequest{UserId: o.UserId, OrderId: o.OrderId})
	require.NoError(t, err)
	return res.Order
}

func newUserFake() *fakeUser {
	return &fakeUser{settled: make(chan *userpb.SettleTradeRequest, 8), released: make(chan *userpb.UnfreezeRequest, 8)}
}

func resting(r *matchingpb.SubmitOrderRequest) *matchingpb.SubmitOrderResponse {
	return &matchingpb.SubmitOrderResponse{OrderId: r.OrderId, FilledQuantity: "0", RemainingQuantity: r.Quantity, Resting: true}
}

func buyFill(r *matchingpb.SubmitOrderRequest, maker *orderpb.Order, lsn uint64) *matchingpb.SubmitOrderResponse {
	return &matchingpb.SubmitOrderResponse{
		OrderId: r.OrderId, FilledQuantity: r.Quantity, RemainingQuantity: "0",
		Trades: []*matchingpb.Trade{{
			TradeId: fmt.Sprintf("BTC/USDT-%d-0", lsn), Symbol: market,
			BuyOrderId: r.OrderId, BuyUserId: r.UserId, SellOrderId: maker.OrderId, SellUserId: maker.UserId,
			Price: maker.Price, Quantity: r.Quantity, TakerSide: matchingpb.OrderSide_ORDER_SIDE_BUY,
			Lsn: lsn, CreatedAt: timestamppb.Now(),
		}},
	}
}

func TestWorkflowPersistsTradeOnceAfterSubmitRetry(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry=%t", retry), func(t *testing.T) {
			db := mysqltest.Open(t)
			reg := prometheus.NewRegistry()
			m := metrics.NewOrder(reg)
			api := server.NewOrderServer(service.New(db, []string{market}, m))
			maker := create(t, api, 10, "maker", orderpb.OrderSide_ORDER_SIDE_SELL, orderpb.OrderType_ORDER_TYPE_LIMIT, "10.12345678", "3")
			var calls atomic.Int32
			var cached atomic.Pointer[matchingpb.SubmitOrderResponse]
			users := newUserFake()
			match := &fakeMatching{submit: func(_ context.Context, r *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error) {
				if r.OrderId == maker.OrderId {
					return resting(r), nil
				}
				attempt := calls.Add(1)
				if attempt == 1 {
					cached.Store(buyFill(r, maker, 2))
					if retry {
						return nil, status.Error(codes.Unavailable, "response lost")
					}
				}
				return cached.Load(), nil
			}}
			stop, done := startWorker(t, workflow.NewWorker(db, workflow.Handlers(db, users, match), workflow.Options{Poll: 5 * time.Millisecond}, m))
			awaitOutbox(t, db, maker.OrderId, "submit", "done")
			require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_OPEN, readOrder(t, api, maker).Status)
			taker := create(t, api, 20, "taker", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_LIMIT, "11.12345678", "1.00000001")
			awaitOutbox(t, db, taker.OrderId, "settle:BTC/USDT-2-0", "done")
			submit := awaitOutbox(t, db, taker.OrderId, "submit", "done")
			wantAttempts := 1
			if retry {
				wantAttempts++
			}
			require.Equal(t, wantAttempts, submit.Attempts)
			require.EqualValues(t, wantAttempts, calls.Load())

			posted := receive(t, users.settled)
			require.Equal(t, "BTC/USDT-2-0", posted.TradeId)
			require.Equal(t, "BTC", posted.BaseAsset)
			require.Equal(t, "USDT", posted.QuoteAsset)
			require.Equal(t, "11.12345678", posted.BuyOrderPrice)
			require.Equal(t, "10.12345678", posted.Price)
			require.Equal(t, "1.00000001", posted.Quantity)
			require.Empty(t, users.settled)
			gotMaker, gotTaker := readOrder(t, api, maker), readOrder(t, api, taker)
			require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED, gotMaker.Status)
			require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_FILLED, gotTaker.Status)
			require.Equal(t, "1.00000001", gotMaker.FilledQuantity)
			require.Equal(t, "1.00000001", gotTaker.FilledQuantity)
			var count int64
			require.NoError(t, db.Model(&model.Trade{}).Count(&count).Error)
			require.EqualValues(t, 1, count)

			for _, o := range []*orderpb.Order{maker, taker} {
				lst, err := api.ListTrades(t.Context(), &orderpb.ListTradesRequest{UserId: o.UserId, Symbol: "btc-usdt"})
				require.NoError(t, err)
				require.Len(t, lst.Trades, 1)
				require.Equal(t, o.OrderId, lst.Trades[0].OrderId)
				require.Equal(t, o.Side, lst.Trades[0].Side)
				require.Equal(t, o == maker, lst.Trades[0].Maker)
			}
			stop()
			require.NoError(t, receive(t, done))
			families, err := reg.Gather()
			require.NoError(t, err)
			var settleSeries bool
			for _, family := range families {
				if family.GetName() != "outbox_step_duration_seconds" {
					continue
				}
				for _, sample := range family.Metric {
					for _, label := range sample.Label {
						if label.GetName() == "step" {
							require.NotContains(t, label.GetValue(), ":")
							settleSeries = settleSeries || label.GetValue() == "settle"
						}
					}
				}
			}
			require.True(t, settleSeries)
		})
	}
}

func TestLateMakerSubmitAndTakerPreserveCancellation(t *testing.T) {
	db := mysqltest.Open(t)
	api := server.NewOrderServer(service.New(db, []string{market}, nil))
	maker := create(t, api, 10, "maker", orderpb.OrderSide_ORDER_SIDE_SELL, orderpb.OrderType_ORDER_TYPE_LIMIT, "10", "3")
	first := create(t, api, 20, "first", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_LIMIT, "11", "1")
	second := create(t, api, 30, "second", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_LIMIT, "11", "1")
	makerCalled, secondCalled := make(chan struct{}, 1), make(chan struct{}, 1)
	finishMaker, finishSecond := make(chan struct{}), make(chan struct{})
	allowFirst, allowSecond := make(chan struct{}), make(chan struct{})
	users := newUserFake()
	match := &fakeMatching{
		submit: func(ctx context.Context, r *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error) {
			switch r.OrderId {
			case maker.OrderId:
				makerCalled <- struct{}{}
				select {
				case <-finishMaker:
					return resting(r), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case first.OrderId:
				select {
				case <-allowFirst:
					return buyFill(r, maker, 2), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case second.OrderId:
				select {
				case <-allowSecond:
					secondCalled <- struct{}{}
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				select {
				case <-finishSecond:
					return buyFill(r, maker, 3), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			default:
				return nil, status.Error(codes.InvalidArgument, "unexpected order")
			}
		},
		cancel: func(_ context.Context, req *matchingpb.CancelOrderRequest) (*matchingpb.CancelOrderResponse, error) {
			if req.OrderId != maker.OrderId || req.Symbol != market {
				return nil, status.Error(codes.InvalidArgument, "unexpected cancellation")
			}
			return &matchingpb.CancelOrderResponse{Found: true, RemainingQuantity: "1"}, nil
		},
	}
	startWorker(t, workflow.NewWorker(db, workflow.Handlers(db, users, match), workflow.Options{Poll: 5 * time.Millisecond}, nil))
	receive(t, makerCalled)
	close(allowFirst)
	awaitOutbox(t, db, first.OrderId, "submit", "done")
	close(allowSecond)
	receive(t, secondCalled)
	require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED, readOrder(t, api, maker).Status)

	_, err := api.CancelOrder(t.Context(), &orderpb.CancelOrderRequest{UserId: maker.UserId, OrderId: maker.OrderId})
	require.NoError(t, err)
	awaitOutbox(t, db, maker.OrderId, "release", "done")
	released := receive(t, users.released)
	require.Equal(t, maker.OrderId, released.OrderId)
	require.Equal(t, "1", released.Amount)
	require.Equal(t, "BTC", released.Asset)
	close(finishMaker)
	awaitOutbox(t, db, maker.OrderId, "submit", "done")
	got := readOrder(t, api, maker)
	require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_CANCELLED, got.Status)
	require.Equal(t, "1", got.FilledQuantity)

	close(finishSecond)
	awaitOutbox(t, db, second.OrderId, "settle:BTC/USDT-3-0", "done")
	got = readOrder(t, api, maker)
	require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_CANCELLED, got.Status)
	require.Equal(t, "2", got.FilledQuantity)
	var row model.Order
	require.NoError(t, db.Where("order_id = ?", maker.OrderId).Take(&row).Error)
	require.Equal(t, "1", row.ClosedQuantity.String())
	require.Empty(t, users.released)
}

func TestInvalidMatchingResultLeavesOrdersAndTradesUnchanged(t *testing.T) {
	for name, corrupt := range map[string]func(*matchingpb.SubmitOrderResponse){
		"wrong taker":          func(r *matchingpb.SubmitOrderResponse) { r.Trades[0].BuyUserId++ },
		"inconsistent fills":   func(r *matchingpb.SubmitOrderResponse) { r.Trades[0].Quantity = "0.5" },
		"wrong maker price":    func(r *matchingpb.SubmitOrderResponse) { r.Trades[0].Price = "9" },
		"wrong maker":          func(r *matchingpb.SubmitOrderResponse) { r.Trades[0].SellUserId++ },
		"inconsistent resting": func(r *matchingpb.SubmitOrderResponse) { r.Resting = true },
	} {
		t.Run(name, func(t *testing.T) {
			db := mysqltest.Open(t)
			api := server.NewOrderServer(service.New(db, []string{market}, nil))
			maker := create(t, api, 10, "maker", orderpb.OrderSide_ORDER_SIDE_SELL, orderpb.OrderType_ORDER_TYPE_LIMIT, "10", "2")
			users := newUserFake()
			match := &fakeMatching{submit: func(_ context.Context, r *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error) {
				if r.OrderId == maker.OrderId {
					return resting(r), nil
				}
				result := buyFill(r, maker, 2)
				corrupt(result)
				return result, nil
			}}
			startWorker(t, workflow.NewWorker(db, workflow.Handlers(db, users, match), workflow.Options{Poll: 5 * time.Millisecond, MaxAttempts: 1}, nil))
			awaitOutbox(t, db, maker.OrderId, "submit", "done")
			taker := create(t, api, 20, "taker", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_LIMIT, "11", "1")
			awaitOutbox(t, db, taker.OrderId, "submit", "dead")
			gotMaker, gotTaker := readOrder(t, api, maker), readOrder(t, api, taker)
			require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_OPEN, gotMaker.Status)
			require.Equal(t, orderpb.OrderStatus_ORDER_STATUS_PENDING, gotTaker.Status)
			require.Equal(t, "0", gotMaker.FilledQuantity)
			require.Equal(t, "0", gotTaker.FilledQuantity)
			var n int64
			require.NoError(t, db.Model(&model.Trade{}).Count(&n).Error)
			require.Zero(t, n)
			require.Empty(t, users.settled)
			require.Empty(t, users.released)
		})
	}
}

func TestWorkflowReleasesOnlyDurablyClosedFunds(t *testing.T) {
	for _, test := range []struct {
		name         string
		side         orderpb.OrderSide
		kind         orderpb.OrderType
		price        string
		asset        string
		amount       string
		freezeReject bool
		matchReject  matchingpb.RejectReason
		state        orderpb.OrderStatus
		reason       string
	}{
		{"freeze rejection", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_LIMIT, "2", "USDT", "3", true,
			matchingpb.RejectReason_REJECT_REASON_NONE, orderpb.OrderStatus_ORDER_STATUS_REJECTED, "insufficient_balance"},
		{"FOK rejection", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_FOK, "2.12345678", "USDT", "3.18518517", false,
			matchingpb.RejectReason_REJECT_REASON_FOK_UNFILLED, orderpb.OrderStatus_ORDER_STATUS_REJECTED, "fok_unfilled"},
		{"market no liquidity", orderpb.OrderSide_ORDER_SIDE_SELL, orderpb.OrderType_ORDER_TYPE_MARKET, "", "BTC", "1.5", false,
			matchingpb.RejectReason_REJECT_REASON_NO_LIQUIDITY, orderpb.OrderStatus_ORDER_STATUS_REJECTED, "no_liquidity"},
		{"limit exceeds resting capacity", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_LIMIT, "2", "USDT", "3", false,
			matchingpb.RejectReason_REJECT_REASON_NONE, orderpb.OrderStatus_ORDER_STATUS_CANCELLED, ""},
		{"IOC zero fills", orderpb.OrderSide_ORDER_SIDE_BUY, orderpb.OrderType_ORDER_TYPE_IOC, "2", "USDT", "3", false,
			matchingpb.RejectReason_REJECT_REASON_NONE, orderpb.OrderStatus_ORDER_STATUS_CANCELLED, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := mysqltest.Open(t)
			api := server.NewOrderServer(service.New(db, []string{market}, nil))
			o := create(t, api, 10, "one", test.side, test.kind, test.price, "1.5")
			frozen := make(chan *userpb.FreezeRequest, 1)
			users := newUserFake()
			users.freeze = func(req *userpb.FreezeRequest) *userpb.FreezeResponse {
				frozen <- req
				if test.freezeReject {
					return &userpb.FreezeResponse{Reason: "insufficient_balance"}
				}
				return &userpb.FreezeResponse{Ok: true}
			}
			match := &fakeMatching{submit: func(_ context.Context, r *matchingpb.SubmitOrderRequest) (*matchingpb.SubmitOrderResponse, error) {
				return &matchingpb.SubmitOrderResponse{OrderId: r.OrderId, FilledQuantity: "0", RemainingQuantity: r.Quantity, RejectReason: test.matchReject}, nil
			}}
			startWorker(t, workflow.NewWorker(db, workflow.Handlers(db, users, match), workflow.Options{Poll: 5 * time.Millisecond}, nil))
			awaitOutbox(t, db, o.OrderId, "freeze", "done")
			req := receive(t, frozen)
			require.Equal(t, o.OrderId, req.OrderId)
			require.Equal(t, test.asset, req.Asset)
			require.Equal(t, test.amount, req.Amount)
			if test.freezeReject {
				var n int64
				require.NoError(t, db.Model(&workflow.Entry{}).Where("aggregate_id = ?", o.OrderId).Count(&n).Error)
				require.EqualValues(t, 1, n, "a rejected freeze must not submit or release")
			} else {
				awaitOutbox(t, db, o.OrderId, "release", "done")
				rel := receive(t, users.released)
				require.Equal(t, o.OrderId, rel.OrderId)
				require.Equal(t, test.asset, rel.Asset)
				require.Equal(t, test.amount, rel.Amount)
			}
			got := readOrder(t, api, o)
			require.Equal(t, test.state, got.Status)
			require.Equal(t, test.reason, got.RejectReason)
			require.Equal(t, "0", got.FilledQuantity)
			require.Empty(t, users.released)
			require.Empty(t, users.settled)
		})
	}
}
