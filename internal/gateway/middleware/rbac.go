// Package middleware 提供 RBAC 权限控制中间件
package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/linxun2025/exchange-project/pkg/response"
)

// Permission 权限定义
type Permission string

const (
	PermissionOrderCreate    Permission = "order:create"
	PermissionOrderCancel    Permission = "order:cancel"
	PermissionOrderQuery     Permission = "order:query"
	PermissionBalanceQuery   Permission = "balance:query"
	PermissionBalanceDeposit Permission = "balance:deposit"
)

// RolePermissionMap 角色权限映射
var RolePermissionMap = map[string][]Permission{
	"admin": {
		PermissionOrderCreate,
		PermissionOrderCancel,
		PermissionOrderQuery,
		PermissionBalanceQuery,
		PermissionBalanceDeposit,
	},
	"trader": {
		PermissionOrderCreate,
		PermissionOrderCancel,
		PermissionOrderQuery,
		PermissionBalanceQuery,
		PermissionBalanceDeposit,
	},
	"viewer": {
		PermissionOrderQuery,
		PermissionBalanceQuery,
	},
}

// RequirePermission 权限检查中间件
func RequirePermission(permission Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		if role == "" {
			response.Forbidden(c, "role not found")
			c.Abort()
			return
		}

		permissions, ok := RolePermissionMap[role]
		if !ok {
			response.Forbidden(c, "invalid role")
			c.Abort()
			return
		}

		for _, p := range permissions {
			if p == permission {
				c.Next()
				return
			}
		}

		response.Forbidden(c, "permission denied")
		c.Abort()
	}
}
