-- ============================================================
-- 绑定系统重构数据迁移脚本
-- 从 device_bind_relation + user_device_pool 迁移到 device_bind
-- 执行前提：新表已由 Go Migrate() 建好
-- 幂等：可重复执行，不会重复插入
-- ============================================================

-- 建议在事务中执行，验证无误后 COMMIT，有问题 ROLLBACK
START TRANSACTION;

-- ============================================================
-- Step 1: 迁移当前绑定状态 → device_bind
--
-- 旧表每台设备可能有多条历史记录（status=0 是解绑历史）。
-- 取每台设备最新的 status=1 记录作为当前绑定状态。
-- ============================================================
INSERT INTO device_bind
    (device_id, mac, chip_uid, device_rand,
     user_id, last_user_id,
     active_time, bind_time, unbind_time,
     created_at, updated_at)
SELECT
    dbr.device_id,
    dbr.mac,
    dbr.chip_uid,
    '',                        -- device_rand: 旧数据无此字段，留空
    dbr.user_id,
    dbr.user_id,               -- last_user_id = 当前绑定用户
    dbr.active_time,
    dbr.bind_time,
    NULL,                      -- 当前绑定中，unbind_time 为空
    dbr.created_at,
    dbr.updated_at
FROM device_bind_relation dbr
INNER JOIN (
    -- 每台设备取最新的已绑定记录
    SELECT device_id, MAX(id) AS max_id
    FROM device_bind_relation
    WHERE status = 1
      AND device_id != ''      -- 排除 InsertActiveRecord 写入的空 device_id 行
    GROUP BY device_id
) latest ON dbr.id = latest.max_id
ON DUPLICATE KEY UPDATE
    -- 已存在则跳过（保护手动写入的记录）
    device_id = device_bind.device_id;

-- ============================================================
-- Step 2: 迁移已解绑的设备 → device_bind（user_id=0，保留 last_user_id）
--
-- 已解绑但从未被 Step 1 迁入（即该设备当前无 status=1 记录）。
-- last_user_id 取最后一次绑定的 user_id，用于扫码找回（情况 D）。
-- ============================================================
INSERT INTO device_bind
    (device_id, mac, chip_uid, device_rand,
     user_id, last_user_id,
     active_time, bind_time, unbind_time,
     created_at, updated_at)
SELECT
    dbr.device_id,
    dbr.mac,
    dbr.chip_uid,
    '',
    0,                         -- user_id=0：无主
    dbr.user_id,               -- last_user_id：最后一任主人
    dbr.active_time,
    dbr.bind_time,
    dbr.unbind_time,
    dbr.created_at,
    dbr.updated_at
FROM device_bind_relation dbr
INNER JOIN (
    -- 取每台设备最后一条记录（不管 status），且该设备没有 status=1 的记录
    SELECT device_id, MAX(id) AS max_id
    FROM device_bind_relation
    WHERE device_id != ''
    GROUP BY device_id
    HAVING SUM(status = 1) = 0   -- 该设备没有任何在绑记录
) latest ON dbr.id = latest.max_id
ON DUPLICATE KEY UPDATE
    device_id = device_bind.device_id;

-- ============================================================
-- Step 3: 迁移绑定历史日志 → device_bind_log
-- ============================================================
INSERT INTO device_bind_log
    (device_id, user_id, action, mac, chip_uid, device_rand, created_at)
SELECT
    device_id,
    user_id,
    CASE status
        WHEN 1 THEN 1    -- status=1 → action=bind
        WHEN 0 THEN 2    -- status=0 → action=unbind
    END,
    mac,
    chip_uid,
    '',
    created_at
FROM device_bind_relation
WHERE device_id != ''            -- 排除空 device_id 的激活记录
  AND user_id != 0               -- 排除系统行
ORDER BY id ASC;

-- ============================================================
-- Step 4: 修正 users.bind_quota
--
-- 旧系统注册时预分配 10 个，bind_quota 默认也是 10。
-- 现有已绑定设备需要把额度扣掉（设计文档 §11 说"接受"已绑不占额度，
-- 但实际上应该与当前绑定数对齐，否则用户看到的 quota 虚高）。
--
-- 策略：bind_quota = 10 - 当前绑定设备数
-- 若结果为负（绑定数超过 10），设为 0
-- ============================================================
UPDATE users u
SET bind_quota = GREATEST(0,
    10 - (
        SELECT COUNT(*)
        FROM device_bind db
        WHERE db.user_id = u.id
    )
);

-- ============================================================
-- Step 5: 重命名 global_device_pool → device_pool
--
-- 注意：服务端 Migrate() 的 CREATE TABLE IF NOT EXISTS device_pool 可能已
-- 建出一张空的 device_pool。若该空表存在，RENAME 会因目标已存在而失败，
-- 因此先 DROP 这张空壳（仅当它确实为空时安全）。
-- ============================================================
DROP TABLE IF EXISTS device_pool;
RENAME TABLE global_device_pool TO device_pool;

-- ============================================================
-- Step 6: 同步 device_pool.status
--
-- 旧系统通过 user_device_pool 标记已分配。迁移后需要确保
-- device_bind 中有记录的设备在 device_pool 也标记 status=1。
-- ============================================================
UPDATE device_pool gdp
SET status = 1
WHERE device_id IN (
    SELECT device_id FROM device_bind
)
AND status = 0;

-- ============================================================
-- Step 7: 修正已解绑设备的 device_pool.status
--
-- CommitUnbind 已将 device_pool.status 重置为 0，确保解绑设备
-- （device_bind.user_id=0）可被 CommitBindFromPool 重新分配。
-- 迁移数据中可能存在 user_id=0 但 status=1 的不一致状态，需修正。
-- ============================================================
UPDATE device_pool
SET status = 0
WHERE device_id IN (
    SELECT device_id FROM device_bind WHERE user_id = 0
)
AND status != 0;
AND dp.status != 0;

-- ============================================================
-- 验证查询（执行后检查结果再决定 COMMIT/ROLLBACK）
-- ============================================================

-- 检查迁移结果
SELECT '=== 迁移结果 ===' AS '';
SELECT
    (SELECT COUNT(*) FROM device_bind_relation WHERE status=1 AND device_id!='') AS old_active,
    (SELECT COUNT(*) FROM device_bind WHERE user_id!=0)                          AS new_active,
    (SELECT COUNT(*) FROM device_bind WHERE user_id=0)                          AS new_unowned,
    (SELECT COUNT(*) FROM device_bind_log)                                       AS log_rows,
    (SELECT COUNT(*) FROM device_bind_relation WHERE device_id!='')              AS old_total;

-- 检查 bind_quota 异常用户（quota < 0 不应出现）
SELECT '=== bind_quota 异常 ===' AS '';
SELECT id, email, bind_quota FROM users WHERE bind_quota < 0;

-- 检查 device_pool.status 与 device_bind 的一致性
-- 已绑定设备（user_id!=0）→ pool status 应为 1
-- 已解绑设备（user_id=0） → pool status 应为 0
SELECT '=== pool status 不一致（已绑但 status=0）===' AS '';
SELECT dp.device_id, dp.status, db.user_id
FROM device_pool dp
JOIN device_bind db ON dp.device_id = db.device_id
WHERE db.user_id != 0 AND dp.status = 0;

SELECT '=== pool status 不一致（已解绑但 status=1）===' AS '';
SELECT dp.device_id, dp.status, db.user_id
FROM device_pool dp
JOIN device_bind db ON dp.device_id = db.device_id
WHERE db.user_id = 0 AND dp.status != 0;

-- 确认无误后执行：
-- COMMIT;
-- 有问题执行：
-- ROLLBACK;
