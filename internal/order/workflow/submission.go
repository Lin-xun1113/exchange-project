package workflow

import (
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	matchingpb "github.com/linxun2025/exchange-project/api/gen/matching/v1"
	userpb "github.com/linxun2025/exchange-project/api/gen/user/v1"
	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/internal/order/repository"
	"github.com/linxun2025/exchange-project/pkg/money"
	"github.com/linxun2025/exchange-project/pkg/symbol"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type submission struct {
	filled, remaining decimal.Decimal
	resting           bool
	rejectReason      string
	trades            []model.Trade
}

func validateSubmit(o model.Order, resp *matchingpb.SubmitOrderResponse) (submission, error) {
	if resp == nil || resp.OrderId != o.OrderID {
		return submission{}, invalidMatch("submit response order ID mismatch")
	}
	filled, filledOK := matchQuantity(resp.FilledQuantity)
	remaining, remainingOK := matchQuantity(resp.RemainingQuantity)
	if !filledOK || !remainingOK || !filled.Add(remaining).Equal(o.Quantity) {
		return submission{}, invalidMatch("invalid filled or remaining quantity")
	}
	// Capacity overflow can close a limit order without executing any fills.
	if (resp.Resting && (o.Type != model.OrderTypeLimit || remaining.IsZero())) ||
		(!resp.Resting && o.Type == model.OrderTypeLimit && remaining.IsPositive() && filled.IsPositive()) {
		return submission{}, invalidMatch("resting flag contradicts order type or remaining quantity")
	}

	result := submission{filled: filled, remaining: remaining, resting: resp.Resting}
	switch resp.RejectReason {
	case matchingpb.RejectReason_REJECT_REASON_NONE:
		if (o.Type == model.OrderTypeFOK && !remaining.IsZero()) || (o.Type == model.OrderTypeMarket && filled.IsZero()) {
			return submission{}, invalidMatch("missing rejection reason")
		}
	case matchingpb.RejectReason_REJECT_REASON_FOK_UNFILLED:
		if o.Type != model.OrderTypeFOK {
			return submission{}, invalidMatch("unexpected FOK rejection")
		}
		result.rejectReason = "fok_unfilled"
	case matchingpb.RejectReason_REJECT_REASON_NO_LIQUIDITY:
		if o.Type != model.OrderTypeMarket {
			return submission{}, invalidMatch("unexpected no-liquidity rejection")
		}
		result.rejectReason = "no_liquidity"
	default:
		return submission{}, invalidMatch("unknown rejection reason")
	}
	if result.rejectReason != "" && (!filled.IsZero() || len(resp.Trades) != 0) {
		return submission{}, invalidMatch("rejected order has fills")
	}

	seen := make(map[string]struct{}, len(resp.Trades))
	result.trades = make([]model.Trade, 0, len(resp.Trades))
	total := decimal.Zero
	var lsn uint64
	for _, t := range resp.Trades {
		if t == nil || !validID(t.TradeId, 96) || t.Symbol != o.Symbol || t.TakerSide != matchingSide(o.Side) ||
			!validID(t.BuyOrderId, 64) || !validID(t.SellOrderId, 64) || t.BuyOrderId == t.SellOrderId ||
			t.BuyUserId <= 0 || t.SellUserId <= 0 || t.Lsn == 0 || (lsn != 0 && t.Lsn != lsn) {
			return submission{}, invalidMatch("invalid trade identity")
		}
		if _, exists := seen[t.TradeId]; exists {
			return submission{}, invalidMatch("duplicate trade ID in submit response")
		}
		seen[t.TradeId] = struct{}{}
		lsn = t.Lsn

		takerID, takerUser := t.BuyOrderId, t.BuyUserId
		if o.Side == model.OrderSideSell {
			takerID, takerUser = t.SellOrderId, t.SellUserId
		}
		if takerID != o.OrderID || takerUser != o.UserID {
			return submission{}, invalidMatch("trade taker mismatch")
		}
		price, priceOK := matchQuantity(t.Price)
		qty, qtyOK := matchQuantity(t.Quantity)
		if !priceOK || !qtyOK || !price.IsPositive() || !qty.IsPositive() {
			return submission{}, invalidMatch("invalid trade price or quantity")
		}
		if err := t.CreatedAt.CheckValid(); err != nil || t.CreatedAt.AsTime().Year() < 1000 {
			return submission{}, invalidMatch("invalid trade timestamp")
		}
		total = total.Add(qty)
		if total.GreaterThan(filled) {
			return submission{}, invalidMatch("trades exceed filled quantity")
		}
		result.trades = append(result.trades, model.Trade{
			TradeID: t.TradeId, Symbol: t.Symbol, BuyOrderID: t.BuyOrderId, SellOrderID: t.SellOrderId,
			BuyUserID: t.BuyUserId, SellUserID: t.SellUserId, TakerSide: o.Side,
			Price: price, Quantity: qty, LSN: t.Lsn, CreatedAt: t.CreatedAt.AsTime().Truncate(time.Microsecond),
		})
	}
	if !total.Equal(filled) {
		return submission{}, invalidMatch("trades do not add up to filled quantity")
	}
	return result, nil
}

func (r submission) apply(tx *gorm.DB, takerID string) error {
	// Lock every affected order in one order across all submissions. Locking
	// the taker first can deadlock with a late maker's submit transaction.
	type lockedOrder struct {
		model.Order
		dirty bool
	}
	orders := make(map[string]*lockedOrder, len(r.trades)+1)
	ids := make([]string, 0, len(r.trades)+1)
	add := func(id string) {
		if _, exists := orders[id]; !exists {
			orders[id] = nil
			ids = append(ids, id)
		}
	}
	add(takerID)
	for _, trade := range r.trades {
		add(trade.BuyOrderID)
		add(trade.SellOrderID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o, err := repository.GetOrderForUpdate(tx, id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invalidMatch("trade references unknown order")
		}
		if err != nil {
			return err
		}
		orders[id] = &lockedOrder{Order: o}
	}

	taker := orders[takerID]
	if taker.RejectReason != "" && taker.RejectReason != r.rejectReason {
		return invalidMatch("submit contradicts recorded rejection")
	}
	var pair symbol.Pair
	if len(r.trades) > 0 {
		var err error
		pair, err = symbol.Parse(taker.Symbol)
		if err != nil {
			return invalidMatch("invalid order symbol")
		}
	}
	for _, trade := range r.trades {
		buy, sell := orders[trade.BuyOrderID], orders[trade.SellOrderID]
		if err := validateTradeOrders(trade, buy.Order, sell.Order); err != nil {
			return err
		}
		inserted, err := repository.InsertTrade(tx, &trade)
		if err != nil {
			return err
		}
		if !inserted {
			previous, err := repository.GetTrade(tx, trade.TradeID)
			if err != nil {
				return err
			}
			if !sameTrade(previous, trade) {
				return invalidMatch("trade ID reused with different details")
			}
			continue
		}

		for _, o := range [2]*lockedOrder{buy, sell} {
			o.FilledQuantity = o.FilledQuantity.Add(trade.Quantity)
			if o.FilledQuantity.Add(o.ClosedQuantity).GreaterThan(o.Quantity) {
				return invalidMatch("trade would exceed order quantity")
			}
			o.dirty = true
		}
		if err := Enqueue(tx, takerID, settleKey(trade.TradeID), &userpb.SettleTradeRequest{
			TradeId: trade.TradeID, BaseAsset: pair.Base, QuoteAsset: pair.Quote,
			BuyUserId: trade.BuyUserID, SellUserId: trade.SellUserID,
			Price: trade.Price.String(), Quantity: trade.Quantity.String(), BuyOrderPrice: buy.Price.String(),
		}); err != nil {
			return err
		}
	}

	// The response includes only the taker fills from this submission. Other
	// submits may already have recorded maker fills against the resting order.
	if taker.FilledQuantity.LessThan(r.filled) || (!r.resting && !taker.FilledQuantity.Equal(r.filled)) {
		return invalidMatch("submit fills contradict recorded fills")
	}
	if !r.resting && r.remaining.IsPositive() {
		if !taker.ClosedQuantity.IsZero() && !taker.ClosedQuantity.Equal(r.remaining) {
			return invalidMatch("submit contradicts recorded closed quantity")
		}
		taker.ClosedQuantity = r.remaining
		taker.RejectReason = r.rejectReason
		if err := Enqueue(tx, takerID, ReleaseStep, nil); err != nil {
			return err
		}
	}
	if taker.Status == model.OrderStatusPending {
		taker.Status = model.OrderStatusOpen
	}
	taker.dirty = true
	for _, id := range ids {
		o := orders[id]
		if o.dirty {
			if err := saveOrder(tx, &o.Order); err != nil {
				return err
			}
		}
	}
	if r.resting && taker.CancelRequested && !taker.Status.IsFinal() {
		return Enqueue(tx, takerID, CancelStep, nil)
	}
	return nil
}

func validateTradeOrders(t model.Trade, buy, sell model.Order) error {
	if buy.Symbol != t.Symbol || sell.Symbol != t.Symbol || buy.UserID != t.BuyUserID || sell.UserID != t.SellUserID ||
		buy.Side != model.OrderSideBuy || sell.Side != model.OrderSideSell || buy.RejectReason != "" || sell.RejectReason != "" {
		return invalidMatch("trade orders do not match their persisted details")
	}
	maker := buy
	if t.TakerSide == model.OrderSideBuy {
		maker = sell
	}
	if maker.Type != model.OrderTypeLimit || !maker.Price.Equal(t.Price) ||
		t.Price.GreaterThan(buy.Price) || (sell.Type != model.OrderTypeMarket && t.Price.LessThan(sell.Price)) {
		return invalidMatch("trade price violates maker price or taker limit")
	}
	return nil
}

func sameTrade(a, b model.Trade) bool {
	return a.TradeID == b.TradeID && a.Symbol == b.Symbol && a.BuyOrderID == b.BuyOrderID && a.SellOrderID == b.SellOrderID &&
		a.BuyUserID == b.BuyUserID && a.SellUserID == b.SellUserID && a.TakerSide == b.TakerSide && a.LSN == b.LSN &&
		a.Price.Equal(b.Price) && a.Quantity.Equal(b.Quantity) && a.CreatedAt.Equal(b.CreatedAt)
}

func matchQuantity(text string) (decimal.Decimal, bool) {
	d, err := decimal.NewFromString(text)
	if err != nil || d.Sign() < 0 || d.Exponent() < -money.MaxScale {
		return decimal.Zero, false
	}
	ticks, err := money.ToTicks(d)
	if err != nil {
		return decimal.Zero, false
	}
	return money.FromTicks(ticks), true
}

func validID(id string, maxChars int) bool {
	return id != "" && utf8.ValidString(id) && (len(id) <= maxChars || utf8.RuneCountInString(id) <= maxChars)
}
