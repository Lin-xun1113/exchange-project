// Package response 定义统一 HTTP 响应结构
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/pkg/errors"
)

// Response 统一响应结构
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
}

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:      errors.CodeSuccess,
		Message:   "success",
		Data:      data,
		RequestID: c.GetString("request_id"),
	})
}

// SuccessWithCode 自定义成功响应
func SuccessWithCode(c *gin.Context, code int, message string, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:      code,
		Message:   message,
		Data:      data,
		RequestID: c.GetString("request_id"),
	})
}

// Error 错误响应
func Error(c *gin.Context, err error) {
	if be, ok := err.(*errors.BusinessError); ok {
		c.JSON(be.ToHTTPStatus(), Response{
			Code:      be.Code,
			Message:   be.Message,
			RequestID: c.GetString("request_id"),
		})
		return
	}

	// 默认内部错误
	c.JSON(http.StatusInternalServerError, Response{
		Code:      errors.CodeInternalError,
		Message:   "internal server error",
		RequestID: c.GetString("request_id"),
	})
}

// ErrorWithStatus 错误响应（指定 HTTP 状态码）
func ErrorWithStatus(c *gin.Context, httpStatus int, code int, message string) {
	c.JSON(httpStatus, Response{
		Code:      code,
		Message:   message,
		RequestID: c.GetString("request_id"),
	})
}

// BadRequest 400 错误
func BadRequest(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusBadRequest, errors.CodeInvalidParam, message)
}

// Unauthorized 401 错误
func Unauthorized(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusUnauthorized, errors.CodeUnauthorized, message)
}

// Forbidden 403 错误
func Forbidden(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusForbidden, errors.CodeForbidden, message)
}

// NotFound 404 错误
func NotFound(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusNotFound, errors.CodeNotFound, message)
}

// Conflict 409 错误
func Conflict(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusConflict, errors.CodeConflict, message)
}

// TooManyRequests 429 错误
func TooManyRequests(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusTooManyRequests, errors.CodeTooManyRequests, message)
}

// InternalServerError 500 错误
func InternalServerError(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusInternalServerError, errors.CodeInternalError, message)
}

// PageData 分页数据
type PageData struct {
	List       interface{} `json:"list"`
	Total      int64       `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
}

// SuccessWithPage 分页成功响应
func SuccessWithPage(c *gin.Context, list interface{}, total int64, page, pageSize int) {
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	SuccessWithCode(c, errors.CodeSuccess, "success", PageData{
		List:       list,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	})
}
