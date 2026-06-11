-- Trade Persistence: 使用字符串订单ID的 Trades 表
-- 此表与 Matching Engine 的撮合结果直接对应

CREATE TABLE IF NOT EXISTS `trades` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '交易ID',
    `trade_id` VARCHAR(64) NOT NULL UNIQUE COMMENT '交易唯一ID',
    `buy_order_id` VARCHAR(64) NOT NULL COMMENT '买方订单ID（字符串）',
    `sell_order_id` VARCHAR(64) NOT NULL COMMENT '卖方订单ID（字符串）',
    `symbol` VARCHAR(32) NOT NULL COMMENT '交易对符号',
    `price` DECIMAL(20, 8) NOT NULL COMMENT '成交价格',
    `quantity` DECIMAL(20, 8) NOT NULL COMMENT '成交数量',
    `buy_user_id` BIGINT UNSIGNED NOT NULL COMMENT '买方用户ID',
    `sell_user_id` BIGINT UNSIGNED NOT NULL COMMENT '卖方用户ID',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '成交时间',
    INDEX `idx_buy_order_id` (`buy_order_id`),
    INDEX `idx_sell_order_id` (`sell_order_id`),
    INDEX `idx_symbol` (`symbol`),
    INDEX `idx_buy_user_id` (`buy_user_id`),
    INDEX `idx_sell_user_id` (`sell_user_id`),
    INDEX `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='交易记录表（持久化撮合结果）';
