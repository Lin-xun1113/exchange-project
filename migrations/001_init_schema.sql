-- Exchange Project Database Schema
-- Version: v1.0.0

-- 创建数据库
CREATE DATABASE IF NOT EXISTS exchange CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE exchange;

-- 用户表
CREATE TABLE IF NOT EXISTS `users` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '用户ID',
    `username` VARCHAR(64) NOT NULL UNIQUE COMMENT '用户名',
    `password_hash` VARCHAR(255) NOT NULL COMMENT '密码哈希',
    `email` VARCHAR(128) DEFAULT NULL COMMENT '邮箱',
    `balance` DECIMAL(20, 8) NOT NULL DEFAULT 0.00000000 COMMENT '可用余额',
    `frozen_balance` DECIMAL(20, 8) NOT NULL DEFAULT 0.00000000 COMMENT '冻结余额',
    `status` TINYINT NOT NULL DEFAULT 1 COMMENT '状态: 1-正常, 0-禁用',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    INDEX `idx_username` (`username`),
    INDEX `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户表';

-- 订单表
CREATE TABLE IF NOT EXISTS `orders` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '订单ID',
    `order_id` VARCHAR(64) NOT NULL UNIQUE COMMENT '订单唯一ID',
    `idempotency_key` VARCHAR(128) DEFAULT NULL UNIQUE COMMENT '幂等键',
    `user_id` BIGINT UNSIGNED NOT NULL COMMENT '用户ID',
    `symbol` VARCHAR(32) NOT NULL COMMENT '交易对符号',
    `side` ENUM('buy', 'sell') NOT NULL COMMENT '订单方向',
    `order_type` ENUM('limit', 'market') NOT NULL DEFAULT 'limit' COMMENT '订单类型',
    `price` DECIMAL(20, 8) NOT NULL COMMENT '订单价格',
    `quantity` DECIMAL(20, 8) NOT NULL COMMENT '订单数量',
    `filled_quantity` DECIMAL(20, 8) NOT NULL DEFAULT 0.00000000 COMMENT '已成交数量',
    `status` ENUM('pending', 'partial_filled', 'filled', 'cancelled', 'rejected') NOT NULL DEFAULT 'pending' COMMENT '订单状态',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    INDEX `idx_user_id` (`user_id`),
    INDEX `idx_symbol` (`symbol`),
    INDEX `idx_status` (`status`),
    INDEX `idx_created_at` (`created_at`),
    INDEX `idx_idempotency_key` (`idempotency_key`),
    CONSTRAINT `fk_orders_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE RESTRICT ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='订单表';

-- 交易记录表
CREATE TABLE IF NOT EXISTS `trades` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '交易ID',
    `trade_id` VARCHAR(64) NOT NULL UNIQUE COMMENT '交易唯一ID',
    `buy_order_id` BIGINT UNSIGNED NOT NULL COMMENT '买方订单ID',
    `sell_order_id` BIGINT UNSIGNED NOT NULL COMMENT '卖方订单ID',
    `symbol` VARCHAR(32) NOT NULL COMMENT '交易对符号',
    `price` DECIMAL(20, 8) NOT NULL COMMENT '成交价格',
    `quantity` DECIMAL(20, 8) NOT NULL COMMENT '成交数量',
    `buy_user_id` BIGINT UNSIGNED NOT NULL COMMENT '买方用户ID',
    `sell_user_id` BIGINT UNSIGNED NOT NULL COMMENT '卖方用户ID',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '成交时间',
    INDEX `idx_buy_order_id` (`buy_order_id`),
    INDEX `idx_sell_order_id` (`sell_order_id`),
    INDEX `idx_symbol` (`symbol`),
    INDEX `idx_created_at` (`created_at`),
    CONSTRAINT `fk_trades_buy_order` FOREIGN KEY (`buy_order_id`) REFERENCES `orders` (`id`) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT `fk_trades_sell_order` FOREIGN KEY (`sell_order_id`) REFERENCES `orders` (`id`) ON DELETE RESTRICT ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='交易记录表';

-- 角色表
CREATE TABLE IF NOT EXISTS `roles` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '角色ID',
    `name` VARCHAR(64) NOT NULL UNIQUE COMMENT '角色名称',
    `description` VARCHAR(255) DEFAULT NULL COMMENT '角色描述',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    INDEX `idx_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='角色表';

-- 权限表
CREATE TABLE IF NOT EXISTS `permissions` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '权限ID',
    `name` VARCHAR(128) NOT NULL UNIQUE COMMENT '权限名称',
    `description` VARCHAR(255) DEFAULT NULL COMMENT '权限描述',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    INDEX `idx_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='权限表';

-- 用户角色关联表
CREATE TABLE IF NOT EXISTS `user_roles` (
    `user_id` BIGINT UNSIGNED NOT NULL COMMENT '用户ID',
    `role_id` BIGINT UNSIGNED NOT NULL COMMENT '角色ID',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`user_id`, `role_id`),
    CONSTRAINT `fk_user_roles_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT `fk_user_roles_role` FOREIGN KEY (`role_id`) REFERENCES `roles` (`id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户角色关联表';

-- 角色权限关联表
CREATE TABLE IF NOT EXISTS `role_permissions` (
    `role_id` BIGINT UNSIGNED NOT NULL COMMENT '角色ID',
    `permission_id` BIGINT UNSIGNED NOT NULL COMMENT '权限ID',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`role_id`, `permission_id`),
    CONSTRAINT `fk_role_permissions_role` FOREIGN KEY (`role_id`) REFERENCES `roles` (`id`) ON DELETE CASCADE ON UPDATE CASCADE,
    CONSTRAINT `fk_role_permissions_permission` FOREIGN KEY (`permission_id`) REFERENCES `permissions` (`id`) ON DELETE CASCADE ON UPDATE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='角色权限关联表';

-- 插入默认角色和权限
INSERT INTO `roles` (`name`, `description`) VALUES 
    ('admin', '管理员'),
    ('trader', '交易员'),
    ('viewer', '查看者')
ON DUPLICATE KEY UPDATE `description` = VALUES(`description`);

INSERT INTO `permissions` (`name`, `description`) VALUES
    ('order:create', '创建订单'),
    ('order:cancel', '取消订单'),
    ('order:query', '查询订单'),
    ('balance:query', '查询余额'),
    ('user:manage', '用户管理'),
    ('system:config', '系统配置')
ON DUPLICATE KEY UPDATE `description` = VALUES(`description`);

-- 为 admin 角色添加所有权限
INSERT IGNORE INTO `role_permissions` (`role_id`, `permission_id`)
SELECT r.id, p.id FROM `roles` r, `permissions` p WHERE r.name = 'admin';

-- 为 trader 角色添加交易权限
INSERT IGNORE INTO `role_permissions` (`role_id`, `permission_id`)
SELECT r.id, p.id FROM `roles` r, `permissions` p 
WHERE r.name = 'trader' AND p.name IN ('order:create', 'order:cancel', 'order:query', 'balance:query');

-- 为 viewer 角色添加查询权限
INSERT IGNORE INTO `role_permissions` (`role_id`, `permission_id`)
SELECT r.id, p.id FROM `roles` r, `permissions` p 
WHERE r.name = 'viewer' AND p.name IN ('order:query', 'balance:query');
