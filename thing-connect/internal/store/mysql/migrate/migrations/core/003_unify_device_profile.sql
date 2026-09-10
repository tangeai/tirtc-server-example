-- Preserve VoIP profiles written by releases before the unified endpoint.
-- CAST intentionally aborts the migration when a legacy value is not valid
-- JSON; operators must repair that row instead of silently losing settings.
INSERT INTO device_profile (device_id, profile)
SELECT device_id, JSON_OBJECT('voip', CAST(profile AS JSON))
FROM voip_device_profile
WHERE JSON_TYPE(CAST(profile AS JSON)) = 'OBJECT'
ON DUPLICATE KEY UPDATE device_id=device_profile.device_id;

UPDATE device_profile current_profile
JOIN voip_device_profile legacy ON legacy.device_id=current_profile.device_id
SET current_profile.profile=JSON_SET(
    current_profile.profile,
    '$.voip',
    CAST(legacy.profile AS JSON)
)
WHERE NOT JSON_CONTAINS_PATH(current_profile.profile, 'one', '$.voip')
  AND JSON_TYPE(CAST(legacy.profile AS JSON)) = 'OBJECT';
