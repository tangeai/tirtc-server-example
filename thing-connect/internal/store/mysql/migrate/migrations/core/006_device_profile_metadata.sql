ALTER TABLE device_profile
    MODIFY COLUMN profile JSON NOT NULL COMMENT '设备级硬件、固件元数据及按 stream、call、voip 场景保存的媒体能力';
