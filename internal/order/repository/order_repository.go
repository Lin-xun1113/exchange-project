// Package repository stores orders and trades in MySQL.
package repository

import (
	"database/sql"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"github.com/linxun2025/exchange-project/pkg/dberr"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func CreateOrder(tx *gorm.DB, o *model.Order) error {
	return tx.Create(o).Error
}

func GetOrderForUpdate(tx *gorm.DB, orderID string) (model.Order, error) {
	var o model.Order
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ?", orderID).Take(&o).Error
	return o, err
}

func GetOrder(db *gorm.DB, userID int64, orderID string) (model.Order, error) {
	var o model.Order
	err := db.Where("user_id = ? AND order_id = ?", userID, orderID).Take(&o).Error
	return o, err
}

func FindByIdempotencyKey(db *gorm.DB, userID int64, key string) (model.Order, error) {
	var o model.Order
	err := db.Where("user_id = ? AND idempotency_key = ?", userID, key).Take(&o).Error
	return o, err
}

func SaveOrder(tx *gorm.DB, o *model.Order) error {
	return tx.Model(o).Select("filled_quantity", "closed_quantity", "status", "reject_reason", "cancel_requested", "updated_at").Updates(o).Error
}

func ListOrders(db *gorm.DB, userID int64, symbol string, status model.OrderStatus, beforeID int64, limit int) ([]model.Order, error) {
	q := db.Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var orders []model.Order
	err := page(q, symbol, beforeID, limit).Find(&orders).Error
	return orders, err
}

func InsertTrade(tx *gorm.DB, t *model.Trade) (bool, error) {
	// MySQL implements DoNothing as a no-op update of the primary key.
	// Our connections use the default changed-rows semantics.
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(t)
	return res.RowsAffected == 1, res.Error
}

func GetTrade(tx *gorm.DB, tradeID string) (model.Trade, error) {
	var t model.Trade
	err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("trade_id = ?", tradeID).Take(&t).Error
	return t, err
}

func ListTrades(db *gorm.DB, userID int64, symbol string, beforeID int64, limit int) ([]model.Trade, error) {
	q := db.Where("(buy_user_id = ? OR sell_user_id = ?)", userID, userID)
	var trades []model.Trade
	err := page(q, symbol, beforeID, limit).Find(&trades).Error
	return trades, err
}

func page(q *gorm.DB, symbol string, beforeID int64, limit int) *gorm.DB {
	if symbol != "" {
		q = q.Where("symbol = ?", symbol)
	}
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	return q.Order("id DESC").Limit(limit)
}

// Transaction retries MySQL lock conflicts. fn may run more than once and
// must keep all side effects in the transaction.
func Transaction(db *gorm.DB, fn func(*gorm.DB) error, opts ...*sql.TxOptions) error {
	ctx := db.Statement.Context
	for attempt := 0; ; attempt++ {
		err := db.Transaction(fn, opts...)
		if !dberr.IsDeadlock(err) || attempt == 2 {
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
