-- Exchange Project Database Schema

CREATE TABLE IF NOT EXISTS `users` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `username` VARCHAR(64) NOT NULL UNIQUE,
    `password_hash` VARCHAR(255) NOT NULL,
    `email` VARCHAR(128) DEFAULT NULL,
    `status` TINYINT NOT NULL DEFAULT 1,
    `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `balances` (
    `user_id` BIGINT UNSIGNED NOT NULL,
    `asset` VARCHAR(16) COLLATE utf8mb4_0900_bin NOT NULL,
    `available` DECIMAL(36,18) NOT NULL DEFAULT 0,
    `frozen` DECIMAL(36,18) NOT NULL DEFAULT 0,
    `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`user_id`, `asset`),
    CONSTRAINT `chk_available` CHECK (`available` >= 0),
    CONSTRAINT `chk_frozen` CHECK (`frozen` >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `ledger_entries` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `ref_type` VARCHAR(16) COLLATE utf8mb4_0900_bin NOT NULL,
    `ref_id` VARCHAR(96) COLLATE utf8mb4_0900_bin NOT NULL,
    `leg` TINYINT NOT NULL,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `asset` VARCHAR(16) COLLATE utf8mb4_0900_bin NOT NULL,
    `delta_available` DECIMAL(36,18) NOT NULL,
    `delta_frozen` DECIMAL(36,18) NOT NULL,
    `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY `uk_ref` (`ref_type`, `ref_id`, `leg`),
    KEY `idx_user_asset` (`user_id`, `asset`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `orders` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `order_id` VARCHAR(64) COLLATE utf8mb4_0900_bin NOT NULL UNIQUE,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `idempotency_key` VARCHAR(64) COLLATE utf8mb4_0900_bin NOT NULL,
    `symbol` VARCHAR(32) COLLATE utf8mb4_0900_bin NOT NULL,
    `side` ENUM('buy', 'sell') NOT NULL,
    `type` ENUM('limit', 'market', 'ioc', 'fok') NOT NULL,
    `price` DECIMAL(36,18) NOT NULL DEFAULT 0,
    `quantity` DECIMAL(36,18) NOT NULL,
    `filled_quantity` DECIMAL(36,18) NOT NULL DEFAULT 0,
    `closed_quantity` DECIMAL(36,18) NOT NULL DEFAULT 0,
    `frozen_asset` VARCHAR(16) COLLATE utf8mb4_0900_bin NOT NULL,
    `frozen_amount` DECIMAL(36,18) NOT NULL,
    `status` ENUM('pending', 'open', 'partial_filled', 'filled', 'cancelled', 'rejected') NOT NULL DEFAULT 'pending',
    `reject_reason` VARCHAR(32) NOT NULL DEFAULT '',
    `cancel_requested` BOOLEAN NOT NULL DEFAULT FALSE,
    `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE KEY `uk_user_idem` (`user_id`, `idempotency_key`),
    KEY `idx_user_id` (`user_id`, `id`),
    CONSTRAINT `chk_order_quantities` CHECK (
        `filled_quantity` >= 0 AND `closed_quantity` >= 0 AND `filled_quantity` + `closed_quantity` <= `quantity`
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `trades` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `trade_id` VARCHAR(96) COLLATE utf8mb4_0900_bin NOT NULL UNIQUE,
    `symbol` VARCHAR(32) COLLATE utf8mb4_0900_bin NOT NULL,
    `buy_order_id` VARCHAR(64) COLLATE utf8mb4_0900_bin NOT NULL,
    `sell_order_id` VARCHAR(64) COLLATE utf8mb4_0900_bin NOT NULL,
    `buy_user_id` BIGINT UNSIGNED NOT NULL,
    `sell_user_id` BIGINT UNSIGNED NOT NULL,
    `price` DECIMAL(36,18) NOT NULL,
    `quantity` DECIMAL(36,18) NOT NULL,
    `taker_side` ENUM('buy', 'sell') NOT NULL,
    `lsn` BIGINT UNSIGNED NOT NULL,
    `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY `idx_buy_order` (`buy_order_id`),
    KEY `idx_sell_order` (`sell_order_id`),
    KEY `idx_buy_user` (`buy_user_id`, `id`),
    KEY `idx_sell_user` (`sell_user_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `outbox` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    `aggregate_id` VARCHAR(64) COLLATE utf8mb4_0900_bin NOT NULL,
    `step` VARCHAR(128) COLLATE utf8mb4_0900_bin NOT NULL,
    `payload` JSON NOT NULL,
    `status` ENUM('pending', 'done', 'dead') NOT NULL DEFAULT 'pending',
    `attempts` INT NOT NULL DEFAULT 0,
    `next_run_at` DATETIME(6) NOT NULL,
    `locked_until` DATETIME(6) NULL,
    `last_error` TEXT NULL,
    `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    UNIQUE KEY `uk_step` (`aggregate_id`, `step`),
    KEY `idx_due` (`status`, `next_run_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

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
    ('balance:deposit', '演示充值'),
    ('user:manage', '用户管理'),
    ('system:config', '系统配置')
ON DUPLICATE KEY UPDATE `description` = VALUES(`description`);

-- 为 admin 角色添加所有权限
INSERT IGNORE INTO `role_permissions` (`role_id`, `permission_id`)
SELECT r.id, p.id FROM `roles` r, `permissions` p WHERE r.name = 'admin';

-- 为 trader 角色添加交易权限
INSERT IGNORE INTO `role_permissions` (`role_id`, `permission_id`)
SELECT r.id, p.id FROM `roles` r, `permissions` p 
WHERE r.name = 'trader' AND p.name IN ('order:create', 'order:cancel', 'order:query', 'balance:query', 'balance:deposit');

-- 为 viewer 角色添加查询权限
INSERT IGNORE INTO `role_permissions` (`role_id`, `permission_id`)
SELECT r.id, p.id FROM `roles` r, `permissions` p 
WHERE r.name = 'viewer' AND p.name IN ('order:query', 'balance:query');
