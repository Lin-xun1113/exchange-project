package repository

import (
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"gorm.io/gorm"
)

func ListMarketTrades(db *gorm.DB, symbol string, limit int) ([]model.Trade, error) {
	var trades []model.Trade
	err := db.Where("symbol = ?", symbol).
		Order("created_at DESC, id DESC").Limit(limit).Find(&trades).Error
	return trades, err
}

func TradesSince(db *gorm.DB, symbol string, since time.Time) ([]model.Trade, error) {
	var trades []model.Trade
	err := db.Select("id, price, quantity, created_at").Where("symbol = ? AND created_at >= ?", symbol, since).
		Order("created_at, id").Find(&trades).Error
	return trades, err
}

func LatestTrade(db *gorm.DB, symbol string) (model.Trade, bool, error) {
	var trade model.Trade
	res := db.Where("symbol = ?", symbol).
		Order("created_at DESC, id DESC").Limit(1).Find(&trade)
	return trade, res.RowsAffected != 0, res.Error
}
