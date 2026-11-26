ALTER TABLE recognition_user_event_outbox ADD COLUMN label_text VARCHAR(256) NOT NULL DEFAULT '' COMMENT '异常识别标签展示文本';

ALTER TABLE push_event ADD COLUMN label_text VARCHAR(256) NOT NULL DEFAULT '' COMMENT '异常识别标签展示文本';
