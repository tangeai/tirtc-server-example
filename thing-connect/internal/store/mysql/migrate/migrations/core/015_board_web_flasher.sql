ALTER TABLE board_resources ADD COLUMN web_flasher_url TEXT NULL COMMENT '板型专用在线烧录地址';

UPDATE board_resources
SET web_flasher_url = flashing_guide_url,
    flashing_guide_url = 'https://github.com/tangeai/xiaotai/blob/main/docs/boards/lckfb-esp32s3/README.md#烧录',
    revision = revision + 1
WHERE detail_slug = 'esp32-s3-wroom-1-n16r8' AND web_flasher_url IS NULL
  AND BINARY flashing_guide_url = 'https://xiaotai.chat/web-flasher/?board=lckfb-esp32s3';

UPDATE board_resources
SET web_flasher_url = flashing_guide_url,
    flashing_guide_url = 'https://github.com/tangeai/xiaotai/blob/main/docs/boards/mgkfb-bk7258/README.md#烧录',
    revision = revision + 1
WHERE detail_slug = 'mgkfb-bk7258' AND web_flasher_url IS NULL
  AND BINARY flashing_guide_url = 'https://xiaotai.chat/web-flasher/?board=mgkfb-bk7258';

UPDATE board_resources
SET web_flasher_url = flashing_guide_url,
    flashing_guide_url = 'https://github.com/tangeai/xiaotai/blob/main/docs/boards/lckfb-bk7258/README.md#烧录',
    revision = revision + 1
WHERE detail_slug = 'bk7258' AND web_flasher_url IS NULL
  AND BINARY flashing_guide_url = 'https://xiaotai.chat/web-flasher/?board=lckfb-bk7258';

UPDATE board_resources
SET web_flasher_url = flashing_guide_url,
    flashing_guide_url = 'https://github.com/tangeai/xiaotai/blob/main/docs/boards/m5stack-cores3-lite/README.md#烧录',
    revision = revision + 1
WHERE detail_slug = 'm5stack-cores3-lite' AND web_flasher_url IS NULL
  AND BINARY flashing_guide_url = 'https://xiaotai.chat/web-flasher/?board=m5stack-cores3-lite';

UPDATE board_resources
SET web_flasher_url = flashing_guide_url,
    flashing_guide_url = 'https://github.com/tangeai/xiaotai/blob/main/docs/boards/waveshare-esp32p4-touch-lcd-43c-v10/README.md#烧录',
    revision = revision + 1
WHERE detail_slug = 'esp32-p4-c3' AND web_flasher_url IS NULL
  AND BINARY flashing_guide_url = 'https://xiaotai.chat/web-flasher/?board=waveshare-esp32p4-touch-lcd-43c-v10';
