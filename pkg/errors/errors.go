// Package errors 定义业务错误码，统一错误处理
package errors

import (
	"fmt"
	"net/http"
)

// 业务错误码定义
const (
	// 系统级错误 (1xxx)
	CodeSuccess           = 0
	CodeInternalError     = 1001
	CodeInvalidParam      = 1002
	CodeUnauthorized      = 1003
	CodeForbidden         = 1004
	CodeNotFound          = 1005
	CodeConflict          = 1006
	CodeTooManyRequests   = 1007
	CodeServiceUnavailable = 1008

	// 用户相关错误 (2xxx)
	CodeUserNotFound      = 2001
	CodeUserExists        = 2002
	CodeInvalidPassword   = 2003
	CodeTokenInvalid      = 2004
	CodeTokenExpired      = 2005
	CodeInsufficientBalance = 2006

	// 订单相关错误 (3xxx)
	CodeOrderNotFound     = 3001
	CodeOrderExists       = 3002
	CodeOrderInvalid      = 3003
	CodeOrderCancelled    = 3004
	CodeOrderFilled       = 3005
	CodeOrderRejected     = 3006
	CodeIdempotencyKeyUsed = 3007

	// 撮合相关错误 (4xxx)
	CodeNoMatchingOrders  = 4001
	CodePriceOutOfRange  = 4002
	CodeQuantityTooSmall  = 4003
)

// BusinessError 业务错误结构
type BusinessError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

func (e *BusinessError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("code=%d, message=%s, err=%v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("code=%d, message=%s", e.Code, e.Message)
}

func (e *BusinessError) Unwrap() error {
	return e.Err
}

// ToHTTPStatus 错误码转 HTTP 状态码
func (e *BusinessError) ToHTTPStatus() int {
	switch e.Code {
	case CodeSuccess:
		return http.StatusOK
	case CodeInternalError, CodeServiceUnavailable:
		return http.StatusInternalServerError
	case CodeInvalidParam:
		return http.StatusBadRequest
	case CodeUnauthorized, CodeTokenInvalid, CodeTokenExpired:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound, CodeUserNotFound, CodeOrderNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeUserExists, CodeOrderExists, CodeIdempotencyKeyUsed:
		return http.StatusConflict
	case CodeTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// New 创建新的业务错误
func New(code int, message string) *BusinessError {
	return &BusinessError{
		Code:    code,
		Message: message,
	}
}

// NewWithError 创建带底层错误的业务错误
func NewWithError(code int, message string, err error) *BusinessError {
	return &BusinessError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// 预定义错误
var (
	ErrSuccess           = New(CodeSuccess, "success")
	ErrInternal         = New(CodeInternalError, "internal server error")
	ErrInvalidParam     = New(CodeInvalidParam, "invalid parameter")
	ErrUnauthorized     = New(CodeUnauthorized, "unauthorized")
	ErrForbidden        = New(CodeForbidden, "forbidden")
	ErrNotFound         = New(CodeNotFound, "resource not found")
	ErrConflict         = New(CodeConflict, "resource conflict")
	ErrTooManyRequests  = New(CodeTooManyRequests, "too many requests")

	ErrUserNotFound     = New(CodeUserNotFound, "user not found")
	ErrUserExists       = New(CodeUserExists, "user already exists")
	ErrInvalidPassword  = New(CodeInvalidPassword, "invalid password")
	ErrTokenInvalid     = New(CodeTokenInvalid, "invalid token")
	ErrTokenExpired     = New(CodeTokenExpired, "token expired")
	ErrBalance          = New(CodeInsufficientBalance, "insufficient balance")

	ErrOrderNotFound    = New(CodeOrderNotFound, "order not found")
	ErrOrderExists      = New(CodeOrderExists, "order already exists")
	ErrOrderInvalid     = New(CodeOrderInvalid, "invalid order")
	ErrOrderCancelled   = New(CodeOrderCancelled, "order already cancelled")
	ErrOrderFilled      = New(CodeOrderFilled, "order already filled")
	ErrOrderRejected    = New(CodeOrderRejected, "order rejected")
	ErrIdempotencyUsed  = New(CodeIdempotencyKeyUsed, "idempotency key already used")

	ErrNoMatchingOrders = New(CodeNoMatchingOrders, "no matching orders")
	ErrPriceRange       = New(CodePriceOutOfRange, "price out of range")
	ErrQuantitySmall    = New(CodeQuantityTooSmall, "quantity too small")
)
