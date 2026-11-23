// Package model 定义数据库模型。
package model

import (
	"time"

	"github.com/shopspring/decimal"
)

type User struct {
	ID           int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"username"`
	PasswordHash string    `gorm:"type:varchar(255);not null" json:"-"`
	Email        string    `gorm:"type:varchar(128)" json:"email"`
	Status       int       `gorm:"type:tinyint;default:1;not null" json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Roles        []Role    `gorm:"many2many:user_roles" json:"roles,omitempty"`
}

func (User) TableName() string { return "users" }

type Balance struct {
	UserID    int64           `gorm:"primaryKey;autoIncrement:false"`
	Asset     string          `gorm:"primaryKey;type:varchar(16)"`
	Available decimal.Decimal `gorm:"type:decimal(36,18)"`
	Frozen    decimal.Decimal `gorm:"type:decimal(36,18)"`
	UpdatedAt time.Time
}

func (Balance) TableName() string { return "balances" }

type LedgerEntry struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	RefType        string `gorm:"type:varchar(16)"`
	RefID          string `gorm:"type:varchar(96)"`
	Leg            int    `gorm:"type:tinyint"`
	UserID         int64
	Asset          string          `gorm:"type:varchar(16)"`
	DeltaAvailable decimal.Decimal `gorm:"type:decimal(36,18)"`
	DeltaFrozen    decimal.Decimal `gorm:"type:decimal(36,18)"`
	CreatedAt      time.Time
}

func (LedgerEntry) TableName() string { return "ledger_entries" }

type Order struct {
	ID              int64  `gorm:"primaryKey;autoIncrement"`
	OrderID         string `gorm:"type:varchar(64)"`
	UserID          int64
	IdempotencyKey  string          `gorm:"type:varchar(64)"`
	Symbol          string          `gorm:"type:varchar(32)"`
	Side            OrderSide       `gorm:"type:enum('buy','sell')"`
	Type            OrderType       `gorm:"column:type;type:enum('limit','market','ioc','fok')"`
	Price           decimal.Decimal `gorm:"type:decimal(36,18)"`
	Quantity        decimal.Decimal `gorm:"type:decimal(36,18)"`
	FilledQuantity  decimal.Decimal `gorm:"type:decimal(36,18)"`
	ClosedQuantity  decimal.Decimal `gorm:"type:decimal(36,18)"`
	FrozenAsset     string          `gorm:"type:varchar(16)"`
	FrozenAmount    decimal.Decimal `gorm:"type:decimal(36,18)"`
	Status          OrderStatus     `gorm:"type:enum('pending','open','partial_filled','filled','cancelled','rejected');default:pending"`
	RejectReason    string          `gorm:"type:varchar(32)"`
	CancelRequested bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (Order) TableName() string { return "orders" }

type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "limit"
	OrderTypeMarket OrderType = "market"
	OrderTypeIOC    OrderType = "ioc"
	OrderTypeFOK    OrderType = "fok"
)

type OrderStatus string

const (
	OrderStatusPending       OrderStatus = "pending"
	OrderStatusOpen          OrderStatus = "open"
	OrderStatusPartialFilled OrderStatus = "partial_filled"
	OrderStatusFilled        OrderStatus = "filled"
	OrderStatusCancelled     OrderStatus = "cancelled"
	OrderStatusRejected      OrderStatus = "rejected"
)

// IsFinal reports whether the order can no longer be matched.
func (s OrderStatus) IsFinal() bool {
	return s == OrderStatusFilled || s == OrderStatusCancelled || s == OrderStatusRejected
}

type Trade struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	TradeID     string `gorm:"type:varchar(96)"`
	Symbol      string `gorm:"type:varchar(32)"`
	BuyOrderID  string `gorm:"type:varchar(64)"`
	SellOrderID string `gorm:"type:varchar(64)"`
	BuyUserID   int64
	SellUserID  int64
	Price       decimal.Decimal `gorm:"type:decimal(36,18)"`
	Quantity    decimal.Decimal `gorm:"type:decimal(36,18)"`
	TakerSide   OrderSide       `gorm:"type:enum('buy','sell')"`
	LSN         uint64
	CreatedAt   time.Time
}

func (Trade) TableName() string { return "trades" }

type Role struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"name"`
	Description string    `gorm:"type:varchar(255)" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func (Role) TableName() string { return "roles" }
