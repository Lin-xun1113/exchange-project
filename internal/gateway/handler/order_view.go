package handler

import (
	"time"

	orderpb "github.com/linxun2025/exchange-project/api/gen/order/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	orderSides = [...]string{
		orderpb.OrderSide_ORDER_SIDE_BUY:  "buy",
		orderpb.OrderSide_ORDER_SIDE_SELL: "sell",
	}
	orderTypes = [...]string{
		orderpb.OrderType_ORDER_TYPE_LIMIT:  "limit",
		orderpb.OrderType_ORDER_TYPE_MARKET: "market",
		orderpb.OrderType_ORDER_TYPE_IOC:    "ioc",
		orderpb.OrderType_ORDER_TYPE_FOK:    "fok",
	}
	orderStatuses = [...]string{
		orderpb.OrderStatus_ORDER_STATUS_PENDING:        "pending",
		orderpb.OrderStatus_ORDER_STATUS_OPEN:           "open",
		orderpb.OrderStatus_ORDER_STATUS_PARTIAL_FILLED: "partial_filled",
		orderpb.OrderStatus_ORDER_STATUS_FILLED:         "filled",
		orderpb.OrderStatus_ORDER_STATUS_CANCELLED:      "cancelled",
		orderpb.OrderStatus_ORDER_STATUS_REJECTED:       "rejected",
	}
)

type orderView struct {
	OrderID         string `json:"order_id"`
	Symbol          string `json:"symbol"`
	Side            string `json:"side"`
	Type            string `json:"type"`
	Price           string `json:"price"`
	Quantity        string `json:"quantity"`
	FilledQuantity  string `json:"filled_quantity"`
	Status          string `json:"status"`
	RejectReason    string `json:"reject_reason"`
	CancelRequested bool   `json:"cancel_requested"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

func orderJSON(o *orderpb.Order) orderView {
	return orderView{
		OrderID: o.GetOrderId(), Symbol: o.GetSymbol(), Side: enumName(orderSides[:], int32(o.GetSide())),
		Type: enumName(orderTypes[:], int32(o.GetType())), Price: o.GetPrice(), Quantity: o.GetQuantity(),
		FilledQuantity: o.GetFilledQuantity(), Status: enumName(orderStatuses[:], int32(o.GetStatus())),
		RejectReason: o.GetRejectReason(), CancelRequested: o.GetCancelRequested(),
		CreatedAt: timestamp(o.GetCreatedAt()), UpdatedAt: timestamp(o.GetUpdatedAt()),
	}
}

type tradeView struct {
	TradeID   string `json:"trade_id"`
	Symbol    string `json:"symbol"`
	OrderID   string `json:"order_id"`
	Side      string `json:"side"`
	Price     string `json:"price"`
	Quantity  string `json:"quantity"`
	Maker     bool   `json:"maker"`
	CreatedAt string `json:"created_at"`
}

func tradeJSON(t *orderpb.Trade) tradeView {
	return tradeView{
		TradeID: t.GetTradeId(), Symbol: t.GetSymbol(), OrderID: t.GetOrderId(),
		Side: enumName(orderSides[:], int32(t.GetSide())), Price: t.GetPrice(), Quantity: t.GetQuantity(),
		Maker: t.GetMaker(), CreatedAt: timestamp(t.GetCreatedAt()),
	}
}

func enumName(names []string, value int32) string {
	if value > 0 && int(value) < len(names) {
		return names[value]
	}
	return "unspecified"
}

func timestamp(t *timestamppb.Timestamp) string {
	if t == nil {
		return ""
	}
	return t.AsTime().Format(time.RFC3339Nano)
}
