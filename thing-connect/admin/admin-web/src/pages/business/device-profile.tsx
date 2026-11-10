import { Collapse, Descriptions, Empty, Tabs, Typography } from 'antd';
import { prettyJSON, profileFieldNames } from '../../shared/admin-metadata';

export type ReportedProfiles = Partial<Record<'stream' | 'call' | 'voip', Record<string, unknown>>>;
const scenes = [
  { key: 'stream', label: '实时音视频' },
  { key: 'call', label: '设备通话' },
  { key: 'voip', label: '微信通话' },
] as const;
const names: Record<string, string> = {
  ...profileFieldNames,
  up_audio_mt: '上行音频格式',
  up_video_streamid: '上行视频流 ID',
  up_audio_streamid: '上行音频流 ID',
  down_video_streamid: '下行视频流 ID',
  down_audio_streamid: '下行音频流 ID',
  has_camera: '支持视频采集（推导）',
  has_screen: '支持视频接收（推导）',
  voip_room_type: '通话类型（推导）',
};
function displayValue(value: unknown): string {
  if (typeof value === 'boolean') return value ? '是' : '否';
  if (Array.isArray(value)) return value.map(String).join(' / ') || '空列表';
  return value == null ? '—' : String(value);
}

export function DeviceReportedProfiles({ profiles }: { profiles?: ReportedProfiles }) {
  const initial = scenes.find(({ key }) => profiles?.[key]);
  if (!initial)
    return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无可展示的能力上报" />;
  return (
    <Tabs
      defaultActiveKey={initial.key}
      items={scenes.map(({ key, label }) => {
        const fields = profiles?.[key];
        return {
          key,
          label,
          children: fields ? (
            <>
              <Descriptions
                bordered
                size="small"
                column={{ xs: 1, sm: 2 }}
                items={Object.entries(fields).map(([field, value]) => ({
                  key: field,
                  label: (
                    <>
                      {names[field] || field}
                      <br />
                      <Typography.Text type="secondary" code>
                        {field}
                      </Typography.Text>
                    </>
                  ),
                  children: displayValue(value),
                }))}
              />
              <Collapse
                style={{ marginTop: 12 }}
                items={[
                  {
                    key: 'json',
                    label: '查看能力数据 JSON',
                    children: <pre className="profile-json">{prettyJSON(fields)}</pre>,
                  },
                ]}
              />
            </>
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="此场景尚未上报" />
          ),
        };
      })}
    />
  );
}
