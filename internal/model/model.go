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

// Order 订单模型
type Order struct {
	ID              int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	OrderID        string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"order_id"`
	IdempotencyKey string    `gorm:"type:varchar(128);uniqueIndex" json:"idempotency_key,omitempty"`
	UserID         int64     `gorm:"index;not null" json:"user_id"`
	Symbol         string    `gorm:"type:varchar(32);not null;index" json:"symbol"`
	Side           string    `gorm:"type:enum('buy','sell');not null" json:"side"`
	OrderType      string    `gorm:"type:enum('limit','market','ioc','fok');default:'limit';not null" json:"order_type"`
	Price          float64   `gorm:"type:decimal(20,8);not null" json:"price"`
	Quantity       float64   `gorm:"type:decimal(20,8);not null" json:"quantity"`
	FilledQuantity float64   `gorm:"type:decimal(20,8);default:0;not null" json:"filled_quantity"`
	Status         string    `gorm:"type:enum('created','frozen','pending','partial_filled','filled','cancelled','rejected','submitted','settled');default:'created';not null;index" json:"status"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	User           *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (Order) TableName() string {
	return "orders"
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
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	TradeID     string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"trade_id"`
	BuyOrderID  string    `gorm:"type:varchar(64);index;not null" json:"buy_order_id"`
	SellOrderID string    `gorm:"type:varchar(64);index;not null" json:"sell_order_id"`
	Symbol      string    `gorm:"type:varchar(32);not null" json:"symbol"`
	Price       float64   `gorm:"type:decimal(20,8);not null" json:"price"`
	Quantity    float64   `gorm:"type:decimal(20,8);not null" json:"quantity"`
	BuyUserID   int64     `gorm:"not null" json:"buy_user_id"`
	SellUserID  int64     `gorm:"not null" json:"sell_user_id"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (Trade) TableName() string { return "trades" }

type Role struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"name"`
	Description string    `gorm:"type:varchar(255)" json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func (Role) TableName() string { return "roles" }
