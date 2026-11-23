// Package ledger posts signed balance changes and their audit entries atomically.
package ledger

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/pkg/dberr"
	"github.com/linxun2025/exchange-project/pkg/metrics"
	"github.com/linxun2025/exchange-project/pkg/money"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrBalanceCapacity     = errors.New("balance capacity exceeded")
	ErrDepositConflict     = errors.New("deposit reference already used with different parameters")
	errDuplicate           = errors.New("ledger reference already posted")
)

// Reservations and releases may move any positive DECIMAL(36,18) amount.
const balanceScale = 18

type Settlement struct {
	TradeID, Base, Quote      string
	BuyUserID, SellUserID     int64
	Price, Qty, BuyOrderPrice decimal.Decimal
}

type Ledger struct {
	db      *gorm.DB
	metrics *metrics.User
}

func New(db *gorm.DB, m *metrics.User) *Ledger {
	return &Ledger{db: db, metrics: m}
}

func (l *Ledger) Balances(ctx context.Context, userID int64) ([]model.Balance, error) {
	var balances []model.Balance
	err := l.db.WithContext(ctx).Where("user_id = ? AND (available <> 0 OR frozen <> 0)", userID).
		Order("asset ASC").Find(&balances).Error
	return balances, err
}

func (l *Ledger) Deposit(ctx context.Context, userID int64, asset string, amount decimal.Decimal, refID string) (model.Balance, error) {
	if !validPositive(amount, money.MaxScale) {
		return model.Balance{}, money.ErrInvalidAmount
	}
	if err := l.post(ctx, "deposit", refID, []leg{{userID: userID, asset: asset, dAvail: amount}}); err != nil {
		return model.Balance{}, err
	}
	balance := model.Balance{UserID: userID, Asset: asset}
	err := l.db.WithContext(ctx).Where("user_id = ? AND asset = ?", userID, asset).Find(&balance).Error
	return balance, err
}

func (l *Ledger) Freeze(ctx context.Context, userID int64, asset string, amount decimal.Decimal, orderID string) error {
	if !validPositive(amount, balanceScale) {
		return money.ErrInvalidAmount
	}
	return l.post(ctx, "freeze", orderID, []leg{{userID, asset, amount.Neg(), amount}})
}

func (l *Ledger) Unfreeze(ctx context.Context, userID int64, asset string, amount decimal.Decimal, orderID string) error {
	if !validPositive(amount, balanceScale) {
		return money.ErrInvalidAmount
	}
	return l.post(ctx, "unfreeze", orderID, []leg{{userID, asset, amount, amount.Neg()}})
}

func (l *Ledger) SettleTrade(ctx context.Context, s Settlement) error {
	if !validPositive(s.Price, money.MaxScale) || !validPositive(s.Qty, money.MaxScale) || !validPositive(s.BuyOrderPrice, money.MaxScale) {
		return fmt.Errorf("%w: price, quantity and buy order price must be positive scale-%d decimals", money.ErrInvalidAmount, money.MaxScale)
	}
	if s.BuyOrderPrice.LessThan(s.Price) {
		return fmt.Errorf("%w: buy order price below trade price", money.ErrInvalidAmount)
	}
	reserved := s.BuyOrderPrice.Mul(s.Qty)
	if !validPositive(reserved, balanceScale) {
		return fmt.Errorf("%w: buy notional exceeds DECIMAL(36,18)", money.ErrInvalidAmount)
	}
	// The spent and refunded amounts are exact and each is at most reserved.
	spent := s.Price.Mul(s.Qty)
	refund := reserved.Sub(spent)

	// Self-trades keep the same four audit legs. Settlement charges no fee.
	return l.post(ctx, "trade", s.TradeID, []leg{
		{s.BuyUserID, s.Quote, refund, reserved.Neg()},
		{s.BuyUserID, s.Base, s.Qty, decimal.Zero},
		{s.SellUserID, s.Base, decimal.Zero, s.Qty.Neg()},
		{s.SellUserID, s.Quote, spent, decimal.Zero},
	})
}

// validPositive checks the input scale and the 18 integer digits of DECIMAL(36,18).
func validPositive(d decimal.Decimal, scale int32) bool {
	exp := int64(d.Exponent())
	return d.Sign() > 0 && exp >= -int64(scale) && int64(d.NumDigits())+exp <= 18
}

type leg struct {
	userID          int64
	asset           string
	dAvail, dFrozen decimal.Decimal
}

const maxStorageAmount = `CAST('999999999999999999.999999999999999999' AS DECIMAL(36,18))`

const seedSupply = `
INSERT INTO asset_supply (asset, total_issued) VALUES (?, 0)
ON DUPLICATE KEY UPDATE asset = asset_supply.asset`

const increaseSupply = `
UPDATE asset_supply
SET total_issued = total_issued + CAST(? AS DECIMAL(36,18))
WHERE asset = ? AND total_issued + CAST(? AS DECIMAL(36,18)) <= ` + maxStorageAmount

const seedBalance = `
INSERT INTO balances (user_id, asset, available, frozen, updated_at)
VALUES (?, ?, 0, 0, NOW(6))
ON DUPLICATE KEY UPDATE user_id = balances.user_id`

// The total must fit in one column so that all frozen funds can be released.
// Check the source balances before assigning either destination column.
const updateBalance = `
UPDATE balances
SET available = available + CAST(? AS DECIMAL(36,18)),
    frozen = frozen + CAST(? AS DECIMAL(36,18)),
    updated_at = NOW(6)
WHERE user_id = ? AND asset = ?
  AND available + CAST(? AS DECIMAL(36,18)) >= 0
  AND frozen + CAST(? AS DECIMAL(36,18)) >= 0
  AND available + frozen + CAST(? AS DECIMAL(36,18)) + CAST(? AS DECIMAL(36,18)) <= ` + maxStorageAmount

func (l *Ledger) post(ctx context.Context, refType, refID string, legs []leg) error {
	result := "error"
	if l.metrics != nil {
		defer func() { l.metrics.LedgerOp(refType, result) }()
	}

	entries := make([]model.LedgerEntry, len(legs))
	for i, x := range legs {
		entries[i] = model.LedgerEntry{
			RefType: refType, RefID: refID, Leg: i,
			UserID: x.userID, Asset: x.asset, DeltaAvailable: x.dAvail, DeltaFrozen: x.dFrozen,
		}
	}
	// Order balance locks consistently and preserve the original leg numbers.
	slices.SortFunc(entries, func(a, b model.LedgerEntry) int {
		if c := cmp.Compare(a.UserID, b.UserID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Asset, b.Asset); c != 0 {
			return c
		}
		// For a self-trade, make room on the same row before applying its credits.
		if c := cmp.Compare(a.DeltaFrozen.Sign(), b.DeltaFrozen.Sign()); c != 0 {
			return c
		}
		return cmp.Compare(a.Leg, b.Leg)
	})

	const maxRetries = 3
	for attempt := 0; ; attempt++ {
		denied := false
		err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Omit("ID", "CreatedAt").Create(&entries).Error; err != nil {
				if dberr.IsDuplicate(err) {
					return errDuplicate // Roll back before checking the recorded outcome.
				}
				return err
			}
			if refType == "freeze" {
				if err := tx.Exec("SAVEPOINT freeze_balance").Error; err != nil {
					return err
				}
			}
			for i, entry := range entries {
				dAvail, dFrozen := entry.DeltaAvailable.String(), entry.DeltaFrozen.String()
				if refType == "deposit" {
					// The ledger key has been claimed. Serialize issuance for this asset
					// before changing the balance; both writes roll back together.
					if err := tx.Exec(seedSupply, entry.Asset).Error; err != nil {
						return err
					}
					res := tx.Exec(increaseSupply, dAvail, entry.Asset, dAvail)
					if res.Error != nil {
						return res.Error
					}
					if res.RowsAffected == 0 {
						return ErrBalanceCapacity
					}
				}
				if i == 0 || entry.UserID != entries[i-1].UserID || entry.Asset != entries[i-1].Asset {
					// MySQL checks the proposed INSERT row even when the key already
					// exists. Seed with zeros, then apply signed deltas under the same lock.
					if err := tx.Exec(seedBalance, entry.UserID, entry.Asset).Error; err != nil {
						return err
					}
				}
				res := tx.Exec(updateBalance, dAvail, dFrozen, entry.UserID, entry.Asset, dAvail, dFrozen, dAvail, dFrozen)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 0 {
					continue
				}
				// Every leg changes the row if matched. Debit legs never increase the
				// total, so only credit-only legs can hit the capacity predicate.
				if entry.DeltaAvailable.Sign() >= 0 && entry.DeltaFrozen.Sign() >= 0 {
					return ErrBalanceCapacity
				}
				if refType != "freeze" {
					return ErrInsufficientBalance
				}
				// Keep a denied order ID durable even if a stale worker retries
				// after funds become available. A successful freeze always moves funds.
				if err := tx.Exec("ROLLBACK TO SAVEPOINT freeze_balance").Error; err != nil {
					return err
				}
				if err := tx.Exec(`UPDATE ledger_entries SET delta_available = 0, delta_frozen = 0
					WHERE ref_type = 'freeze' AND ref_id = ? AND leg = 0`, refID).Error; err != nil {
					return err
				}
				denied = true
				return nil
			}
			return nil
		})

		switch {
		case err == nil && denied:
			result = "insufficient"
			return ErrInsufficientBalance
		case err == nil:
			result = "ok"
			return nil
		case errors.Is(err, errDuplicate):
			switch refType {
			case "freeze":
				var decision model.LedgerEntry
				if err := l.db.WithContext(ctx).Select("delta_frozen").
					Where("ref_type = 'freeze' AND ref_id = ? AND leg = 0", refID).Take(&decision).Error; err != nil {
					return err
				}
				if decision.DeltaFrozen.IsZero() {
					result = "duplicate"
					return ErrInsufficientBalance
				}
			case "deposit":
				var posted model.LedgerEntry
				if err := l.db.WithContext(ctx).Select("user_id", "asset", "delta_available").
					Where("ref_type = 'deposit' AND ref_id = ? AND leg = 0", refID).Take(&posted).Error; err != nil {
					return err
				}
				requested := &entries[0]
				if posted.UserID != requested.UserID || posted.Asset != requested.Asset || !posted.DeltaAvailable.Equal(requested.DeltaAvailable) {
					return ErrDepositConflict
				}
			}
			result = "duplicate"
			return nil
		case errors.Is(err, ErrInsufficientBalance):
			result = "insufficient"
			return err
		case !dberr.IsDeadlock(err) || attempt == maxRetries:
			return err
		}

		timer := time.NewTimer(10 * time.Millisecond << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
