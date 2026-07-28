-- ============================================================================
-- 生产环境迁移：device_role → ai_device_role，user_role → ai_user_role
-- 执行前检查表是否存在，执行后验证。
-- ============================================================================

-- 1. 重命名 device_role
SET @table_exists = (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'device_role');
SET @sql = IF(@table_exists > 0, 'RENAME TABLE device_role TO ai_device_role', 'SELECT "device_role does not exist, skipping" AS msg');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 2. 重命名 user_role
SET @table_exists = (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'user_role');
SET @sql = IF(@table_exists > 0, 'RENAME TABLE user_role TO ai_user_role', 'SELECT "user_role does not exist, skipping" AS msg');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- ============================================================================
-- 验证
-- ============================================================================
-- 检查旧表已不存在
SELECT COUNT(*) AS device_role_old FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'device_role';
SELECT COUNT(*) AS user_role_old FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'user_role';

-- 检查新表存在
SHOW CREATE TABLE ai_device_role;
SHOW CREATE TABLE ai_user_role;

-- 3. 重命名 device_contact → call_contact
SET @table_exists = (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'device_contact');
SET @sql = IF(@table_exists > 0, 'RENAME TABLE device_contact TO call_contact', 'SELECT "device_contact does not exist, skipping" AS msg');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- 验证
SHOW CREATE TABLE call_contact;
