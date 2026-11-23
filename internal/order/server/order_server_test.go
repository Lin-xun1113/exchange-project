package server_test

import (
	"context"
	"sync"
	"testing"

	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/server"
	"github.com/linxun2025/exchange-project/internal/order/service"
	"github.com/linxun2025/exchange-project/internal/order/workflow"
	"github.com/linxun2025/exchange-project/internal/testutil/mysqltest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func validRequest() *orderpb.CreateOrderRequest {
	return &orderpb.CreateOrderRequest{
		UserId: 10, Symbol: "btc-usdt", Side: orderpb.OrderSide_ORDER_SIDE_BUY, Type: orderpb.OrderType_ORDER_TYPE_LIMIT,
		Price: "3.00000001", Quantity: "1.00000001", IdempotencyKey: "order_1",
	}
}

func TestCreateOrderScopesIdempotencyAndQueuesOneFreeze(t *testing.T) {
	db := mysqltest.Open(t)
	api := server.NewOrderServer(service.New(db, []string{"BTC/USDT"}, nil))
	req := validRequest()

	const n = 8
	type outcome struct {
		resp *orderpb.CreateOrderResponse
		err  error
	}
	results := make(chan outcome, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			resp, err := api.CreateOrder(t.Context(), req)
			results <- outcome{resp, err}
		})
	}
	wg.Wait()
	close(results)
	var orderID string
	newCount := 0
	for result := range results {
		require.NoError(t, result.err)
		if orderID == "" {
			orderID = result.resp.Order.OrderId
		}
		require.Equal(t, orderID, result.resp.Order.OrderId)
		if !result.resp.Replayed {
			newCount++
		}
	}
	require.Equal(t, 1, newCount)

	other := proto.CloneOf(req)
	other.Quantity = "2"
	_, err := api.CreateOrder(t.Context(), other)
	require.Equal(t, codes.AlreadyExists, status.Code(err))
	var row model.Order
	require.NoError(t, db.Where("order_id = ?", orderID).Take(&row).Error)
	require.Equal(t, "USDT", row.FrozenAsset)
	require.Equal(t, "3.0000000400000001", row.FrozenAmount.String())
	var steps []workflow.Entry
	require.NoError(t, db.Find(&steps).Error)
	require.Len(t, steps, 1)
	require.Equal(t, orderID, steps[0].AggregateID)
	require.Equal(t, "freeze", steps[0].Step)

	other.UserId++
	res, err := api.CreateOrder(t.Context(), other)
	require.NoError(t, err)
	require.False(t, res.Replayed)
	require.NotEqual(t, orderID, res.Order.OrderId)
}

func TestOrderValidationGuardsTickAndDecimalRanges(t *testing.T) {
	// Invalid requests must return before accessing the database.
	api := server.NewOrderServer(service.New(nil, []string{"BTC/USDT"}, nil))
	for _, test := range []struct {
		name     string
		price    string
		quantity string
	}{
		{"price overflow", "92233720368.54775808", "1"},
		{"quantity overflow", "1", "92233720368.54775808"},
		{"notional overflow", "1000000000", "1000000000"},
		{"price scale", "0.000000001", "1"},
		{"quantity scale", "1", "0.000000001"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := validRequest()
			req.Price, req.Quantity = test.price, test.quantity
			_, err := api.CreateOrder(context.Background(), req)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
	req := validRequest()
	req.Type, req.Price = orderpb.OrderType_ORDER_TYPE_MARKET, ""
	_, err := api.CreateOrder(context.Background(), req)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "market buy not supported", status.Convert(err).Message())
}

func TestOrderQueriesScopeUserAndUseKeysetPagination(t *testing.T) {
	db := mysqltest.Open(t)
	api := server.NewOrderServer(service.New(db, []string{"BTC/USDT"}, nil))
	req := validRequest()
	older, err := api.CreateOrder(t.Context(), req)
	require.NoError(t, err)
	req.IdempotencyKey = "order_2"
	newer, err := api.CreateOrder(t.Context(), req)
	require.NoError(t, err)
	viewer := req.UserId + 1
	for _, call := range []func() error{
		func() error {
			_, err := api.GetOrder(t.Context(), &orderpb.GetOrderRequest{UserId: viewer, OrderId: newer.Order.OrderId})
			return err
		},
		func() error {
			_, err := api.CancelOrder(t.Context(), &orderpb.CancelOrderRequest{UserId: viewer, OrderId: newer.Order.OrderId})
			return err
		},
	} {
		require.Equal(t, codes.NotFound, status.Code(call()))
	}
	first, err := api.ListOrders(t.Context(), &orderpb.ListOrdersRequest{UserId: req.UserId, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, first.Orders, 1)
	require.Equal(t, newer.Order.OrderId, first.Orders[0].OrderId)
	require.NotEmpty(t, first.NextPageToken)

	req.IdempotencyKey = "inserted-between-pages"
	_, err = api.CreateOrder(t.Context(), req)
	require.NoError(t, err)
	second, err := api.ListOrders(t.Context(), &orderpb.ListOrdersRequest{UserId: req.UserId, PageSize: 1, PageToken: first.NextPageToken})
	require.NoError(t, err)
	require.Len(t, second.Orders, 1)
	require.Equal(t, older.Order.OrderId, second.Orders[0].OrderId)
	require.Empty(t, second.NextPageToken)

	_, err = api.ListOrders(t.Context(), &orderpb.ListOrdersRequest{UserId: req.UserId, Status: orderpb.OrderStatus(99)})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = api.ListTrades(t.Context(), &orderpb.ListTradesRequest{UserId: req.UserId, PageToken: "bad token"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
