// Package repository 提供交易记录仓储层实现
package repository

import (
	"context"
	"time"

	"github.com/linxun2025/exchange-project/internal/model"
	"gorm.io/gorm"
)

// TradeRepository 交易记录仓储
type TradeRepository struct {
	db *gorm.DB
}

// NewTradeRepository 创建交易记录仓储
func NewTradeRepository(db *gorm.DB) *TradeRepository {
	return &TradeRepository{
		db: db,
	}
}

// Create 创建交易记录
func (r *TradeRepository) Create(ctx context.Context, trade *model.Trade) error {
	return r.db.WithContext(ctx).Create(trade).Error
}

// CreateBatch 批量创建交易记录
func (r *TradeRepository) CreateBatch(ctx context.Context, trades []*model.Trade) error {
	if len(trades) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(trades, 100).Error
}

// GetByTradeID 根据交易ID获取交易记录
func (r *TradeRepository) GetByTradeID(ctx context.Context, tradeID string) (*model.Trade, error) {
	var trade model.Trade
	result := r.db.WithContext(ctx).Where("trade_id = ?", tradeID).First(&trade)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, result.Error
	}
	return &trade, nil
}

// ListByUser 列出用户的交易记录
func (r *TradeRepository) ListByUser(ctx context.Context, userID int64, symbol string, limit, offset int) ([]*model.Trade, error) {
	query := r.db.WithContext(ctx).Model(&model.Trade{}).
		Where("buy_user_id = ? OR sell_user_id = ?", userID, userID)

	if symbol != "" {
		query = query.Where("symbol = ?", symbol)
	}

	var trades []*model.Trade
	result := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&trades)
	if result.Error != nil {
		return nil, result.Error
	}

	return trades, nil
}

// ListByOrderID 列出订单关联的交易记录
func (r *TradeRepository) ListByOrderID(ctx context.Context, orderID string, limit, offset int) ([]*model.Trade, error) {
	var trades []*model.Trade
	result := r.db.WithContext(ctx).Model(&model.Trade{}).
		Where("buy_order_id = ? OR sell_order_id = ?", orderID, orderID).
		Order("created_at DESC").
		Limit(limit).Offset(offset).
		Find(&trades)
	if result.Error != nil {
		return nil, result.Error
	}
	return trades, nil
}

// ListBySymbol 列出交易对的交易记录
func (r *TradeRepository) ListBySymbol(ctx context.Context, symbol string, limit, offset int) ([]*model.Trade, error) {
	var trades []*model.Trade
	result := r.db.WithContext(ctx).Model(&model.Trade{}).
		Where("symbol = ?", symbol).
		Order("created_at DESC").
		Limit(limit).Offset(offset).
		Find(&trades)
	if result.Error != nil {
		return nil, result.Error
	}
	return trades, nil
}

// CountByUser 统计用户的交易记录数量
func (r *TradeRepository) CountByUser(ctx context.Context, userID int64, symbol string) (int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Trade{}).
		Where("buy_user_id = ? OR sell_user_id = ?", userID, userID)

	if symbol != "" {
		query = query.Where("symbol = ?", symbol)
	}

	var count int64
	err := query.Count(&count).Error
	return count, err
}

// GetRecentTrades 获取最近成交记录（用于 WebSocket 推送）
func (r *TradeRepository) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]*model.Trade, error) {
	if limit <= 0 {
		limit = 100
	}

	var trades []*model.Trade
	result := r.db.WithContext(ctx).Model(&model.Trade{}).
		Where("symbol = ?", symbol).
		Order("created_at DESC").
		Limit(limit).
		Find(&trades)
	if result.Error != nil {
		return nil, result.Error
	}

	return trades, nil
}

// GetUserTradeStats 获取用户的交易统计
func (r *TradeRepository) GetUserTradeStats(ctx context.Context, userID int64, symbol string) (buyVolume, sellVolume, totalTrades float64, err error) {
	query := r.db.WithContext(ctx).Model(&model.Trade{}).
		Where("(buy_user_id = ? OR sell_user_id = ?) AND created_at >= ?", userID, userID, time.Now().AddDate(0, 0, -1))

	if symbol != "" {
		query = query.Where("symbol = ?", symbol)
	}

	type Result struct {
		BuyVolume    float64
		SellVolume   float64
		TotalTrades  int64
	}

	var res Result
	err = query.Select(
		"SUM(CASE WHEN buy_user_id = ? THEN quantity ELSE 0 END) as buy_volume, "+
			"SUM(CASE WHEN sell_user_id = ? THEN quantity ELSE 0 END) as sell_volume, "+
			"COUNT(*) as total_trades", userID, userID,
	).Scan(&res).Error

	if err != nil {
		return 0, 0, 0, err
	}

	return res.BuyVolume, res.SellVolume, float64(res.TotalTrades), nil
}
