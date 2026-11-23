package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	apperrors "github.com/linxun2025/exchange-project/pkg/errors"
	"github.com/linxun2025/exchange-project/pkg/logger"
	"github.com/linxun2025/exchange-project/pkg/response"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func writeRPCError(c *gin.Context, err error) {
	st := status.Convert(err)
	message := st.Message()
	var httpCode, appCode int
	switch st.Code() {
	case codes.InvalidArgument:
		httpCode, appCode = http.StatusBadRequest, apperrors.CodeInvalidParam
	case codes.Unauthenticated:
		httpCode, appCode = http.StatusUnauthorized, apperrors.CodeUnauthorized
	case codes.PermissionDenied:
		httpCode, appCode = http.StatusForbidden, apperrors.CodeForbidden
	case codes.NotFound:
		httpCode, appCode = http.StatusNotFound, apperrors.CodeNotFound
	case codes.AlreadyExists, codes.FailedPrecondition:
		httpCode, appCode = http.StatusConflict, apperrors.CodeConflict
	case codes.Unavailable, codes.ResourceExhausted:
		httpCode, appCode, message = http.StatusServiceUnavailable, apperrors.CodeServiceUnavailable, "service unavailable"
	case codes.DeadlineExceeded:
		httpCode, appCode, message = http.StatusGatewayTimeout, apperrors.CodeServiceUnavailable, "upstream timeout"
	default:
		httpCode, appCode, message = http.StatusInternalServerError, apperrors.CodeInternalError, "internal server error"
	}
	if httpCode >= 500 {
		logger.WithContext(c.Request.Context()).Warn("downstream RPC failed", logger.S("route", c.FullPath()), logger.Err(err))
	}
	response.ErrorWithStatus(c, httpCode, appCode, message)
}
