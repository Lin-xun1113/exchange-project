-- Migration: Add outbox table and new order states
-- Version: 002
-- Description: Implement transactional outbox pattern and extend order states

USE exchange;

-- Add new order states to support saga pattern
ALTER TABLE orders MODIFY COLUMN status 
    ENUM('created', 'frozen', 'pending', 'partial_filled', 'filled', 'cancelled', 'rejected', 'submitted', 'settled') 
    NOT NULL DEFAULT 'created';

-- Create outbox table for transactional outbox pattern
CREATE TABLE IF NOT EXISTS `outbox` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT 'Outbox entry ID',
    `saga_id` VARCHAR(64) NOT NULL COMMENT 'Saga identifier (same as idempotency_key)',
    `step_name` VARCHAR(64) NOT NULL COMMENT 'Step name within the saga',
    `action_type` VARCHAR(32) NOT NULL COMMENT 'Action type: freeze_balance, submit_matching, update_status, unfreeze_balance',
    `payload` JSON NOT NULL COMMENT 'JSON payload with action details',
    `status` ENUM('pending', 'processing', 'done', 'failed', 'dead_letter') NOT NULL DEFAULT 'pending' COMMENT 'Processing status',
    `retry_count` INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Number of retry attempts',
    `max_retries` INT UNSIGNED NOT NULL DEFAULT 5 COMMENT 'Maximum retry attempts before dead letter',
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT 'Creation timestamp',
    `processed_at` TIMESTAMP NULL COMMENT 'When the entry was successfully processed',
    `error_message` TEXT NULL COMMENT 'Last error message if failed',
    INDEX `idx_saga_id` (`saga_id`),
    INDEX `idx_status` (`status`),
    INDEX `idx_created_at` (`created_at`),
    INDEX `idx_pending_stale` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Transactional outbox for saga pattern';
