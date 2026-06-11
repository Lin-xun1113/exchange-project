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
	PermissionUserManage     Permission = "user:manage"
	PermissionSystemConfig   Permission = "system:config"
)

// RolePermissionMap 角色权限映射
var RolePermissionMap = map[string][]Permission{
	"admin": {
		PermissionOrderCreate,
		PermissionOrderCancel,
		PermissionOrderQuery,
		PermissionBalanceQuery,
		PermissionUserManage,
		PermissionSystemConfig,
	},
	"trader": {
		PermissionOrderCreate,
		PermissionOrderCancel,
		PermissionOrderQuery,
		PermissionBalanceQuery,
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

// RequireAnyPermission 任意权限检查（满足其一即可）
func RequireAnyPermission(permissions ...Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		if role == "" {
			response.Forbidden(c, "role not found")
			c.Abort()
			return
		}

		rolePermissions, ok := RolePermissionMap[role]
		if !ok {
			response.Forbidden(c, "invalid role")
			c.Abort()
			return
		}

		for _, required := range permissions {
			for _, allowed := range rolePermissions {
				if required == allowed {
					c.Next()
					return
				}
			}
		}

		response.Forbidden(c, "permission denied")
		c.Abort()
	}
}

// RequireAllPermissions 所有权限检查（必须全部满足）
func RequireAllPermissions(permissions ...Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		if role == "" {
			response.Forbidden(c, "role not found")
			c.Abort()
			return
		}

		rolePermissions, ok := RolePermissionMap[role]
		if !ok {
			response.Forbidden(c, "invalid role")
			c.Abort()
			return
		}

		for _, required := range permissions {
			found := false
			for _, allowed := range rolePermissions {
				if required == allowed {
					found = true
					break
				}
			}
			if !found {
				response.Forbidden(c, "permission denied")
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// RequireRole 角色检查中间件
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		if role == "" {
			response.Forbidden(c, "role not found")
			c.Abort()
			return
		}

		for _, r := range roles {
			if r == role {
				c.Next()
				return
			}
		}

		response.Forbidden(c, "role not allowed")
		c.Abort()
	}
}

// RequireAdmin 要求管理员角色
func RequireAdmin() gin.HandlerFunc {
	return RequireRole("admin")
}

// RequireTrader 要求交易员角色
func RequireTrader() gin.HandlerFunc {
	return RequireRole("admin", "trader")
}
