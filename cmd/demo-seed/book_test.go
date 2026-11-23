package main

import (
	"testing"

	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestBookLevelsKeepDemoAskInsideTheSpread(t *testing.T) {
	last := decimal.RequireFromString("61840.00")
	bid := levelPrice(last, bookBuy, 0)
	ask := levelPrice(last, bookSell, 0)
	demoAsk := ask.Sub(decimal.RequireFromString("0.50"))
	require.True(t, bid.Equal(decimal.RequireFromString("61836.29")))
	require.True(t, ask.Equal(decimal.RequireFromString("61843.71")))
	require.True(t, demoAsk.GreaterThan(bid))
	require.True(t, demoAsk.LessThan(ask))
	for i := 1; i < 25; i++ {
		require.True(t, levelPrice(last, bookBuy, i).LessThan(levelPrice(last, bookBuy, i-1)))
		require.True(t, levelPrice(last, bookSell, i).GreaterThan(levelPrice(last, bookSell, i-1)))
	}
}

func TestDemoPollRequiresExactPartialFill(t *testing.T) {
	want := orderExpectation{
		id: "demo-ask", status: orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED,
		filled: decimal.RequireFromString("0.015"),
	}
	for _, tc := range []struct {
		name   string
		status orderpb.OrderStatus
		filled string
		ready  bool
		fail   bool
	}{
		{"pending", orderpb.OrderStatus_ORDER_STATUS_PENDING, "0", false, false},
		{"still open", orderpb.OrderStatus_ORDER_STATUS_OPEN, "0", false, false},
		{"exact partial fill", orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED, "0.015", true, false},
		{"wrong partial fill", orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED, "0.01", false, true},
		{"unexpected full fill", orderpb.OrderStatus_ORDER_STATUS_FILLED, "0.04", false, true},
		{"rejected", orderpb.OrderStatus_ORDER_STATUS_REJECTED, "0", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready, err := want.ready(&orderpb.Order{OrderId: want.id, Status: tc.status, FilledQuantity: tc.filled})
			require.Equal(t, tc.ready, ready)
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
