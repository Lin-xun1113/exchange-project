// Package model 定义数据库模型
package model

import (
	"time"
)

// User 用户模型
type User struct {
	ID            int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Username      string    `gorm:"type:varchar(64);uniqueIndex;not null" json:"username"`
	PasswordHash  string    `gorm:"type:varchar(255);not null" json:"-"`
	Email         string    `gorm:"type:varchar(128)" json:"email"`
	Balance       float64   `gorm:"type:decimal(20,8);default:0;not null" json:"balance"`
	FrozenBalance float64   `gorm:"type:decimal(20,8);default:0;not null" json:"frozen_balance"`
	Status        int       `gorm:"type:tinyint;default:1;not null" json:"status"`
	CreatedAt     time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	Roles         []Role    `gorm:"many2many:user_roles;" json:"roles,omitempty"`
}

func (User) TableName() string {
	return "users"
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
	Status         string    `gorm:"type:enum('pending','partial_filled','filled','cancelled','rejected');default:'pending';not null;index" json:"status"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index" json:"created_at"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	User           *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (Order) TableName() string {
	return "orders"
}

// OrderSide 订单方向
type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

// OrderType 订单类型
type OrderType string

const (
	OrderTypeLimit  OrderType = "limit"
	OrderTypeMarket OrderType = "market"
	OrderTypeIOC    OrderType = "ioc"
	OrderTypeFOK    OrderType = "fok"
)

func (t OrderType) String() string {
	switch t {
	case OrderTypeLimit:
		return "limit"
	case OrderTypeMarket:
		return "market"
	case OrderTypeIOC:
		return "ioc"
	case OrderTypeFOK:
		return "fok"
	default:
		return "unknown"
	}
}

// OrderStatus 订单状态
type OrderStatus string

const (
	OrderStatusPending      OrderStatus = "pending"
	OrderStatusPartialFilled OrderStatus = "partial_filled"
	OrderStatusFilled       OrderStatus = "filled"
	OrderStatusCancelled    OrderStatus = "cancelled"
	OrderStatusRejected     OrderStatus = "rejected"
)

// IsFinalStatus 判断是否为终态
func (s OrderStatus) IsFinalStatus() bool {
	return s == OrderStatusFilled || s == OrderStatusCancelled || s == OrderStatusRejected
}

// Trade 交易记录模型
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

func (Trade) TableName() string {
	return "trades"
}

// Role 角色模型
type Role struct {
	ID          int64        `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string       `gorm:"type:varchar(64);uniqueIndex;not null" json:"name"`
	Description string       `gorm:"type:varchar(255)" json:"description"`
	CreatedAt  time.Time    `gorm:"autoCreateTime" json:"created_at"`
	Permissions []Permission `gorm:"many2many:role_permissions;" json:"permissions,omitempty"`
	Users      []User       `gorm:"many2many:user_roles;" json:"users,omitempty"`
}

func (Role) TableName() string {
	return "roles"
}

// Permission 权限模型
type Permission struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"type:varchar(128);uniqueIndex;not null" json:"name"`
	Description string    `gorm:"type:varchar(255)" json:"description"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (Permission) TableName() string {
	return "permissions"
}

// UserRole 用户角色关联
type UserRole struct {
	UserID    int64     `gorm:"primaryKey" json:"user_id"`
	RoleID    int64     `gorm:"primaryKey" json:"role_id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (UserRole) TableName() string {
	return "user_roles"
}

// RolePermission 角色权限关联
type RolePermission struct {
	RoleID       int64     `gorm:"primaryKey" json:"role_id"`
	PermissionID int64     `gorm:"primaryKey" json:"permission_id"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (RolePermission) TableName() string {
	return "role_permissions"
}
