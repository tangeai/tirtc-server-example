import { Typography } from 'antd';

export type IPLocation = {
  status: string;
  country?: string;
  province?: string;
  city?: string;
  region?: string;
  isp?: string;
};

export function IPAddress({
  ip,
  location,
  compact = false,
}: {
  ip?: string;
  location?: IPLocation;
  compact?: boolean;
}) {
  if (!ip) return <Typography.Text type="secondary">未记录</Typography.Text>;
  const special: Record<string, string> = {
    loopback: '回环地址',
    private: '内网地址',
    reserved: '特殊用途地址',
    invalid: '无效地址',
    unavailable: '归属地暂不可用',
    unknown: '归属地未知',
  };
  const region =
    location?.region ||
    [...new Set([location?.country, location?.province, location?.city].filter(Boolean))].join(' ');
  const description =
    location?.status === 'found'
      ? region || '地区未知'
      : special[location?.status || 'unknown'] || '归属地未知';
  const lineStyle = { display: 'block', maxWidth: '100%', overflowWrap: 'anywhere' as const };
  return (
    <div style={{ minWidth: 0, maxWidth: '100%' }}>
      <Typography.Text
        style={{ ...lineStyle, fontFamily: 'monospace' }}
        copyable={{ text: ip, tooltips: ['复制 IP', '已复制'] }}
      >
        {ip}
      </Typography.Text>
      <Typography.Text
        type="secondary"
        style={lineStyle}
        ellipsis={compact ? { tooltip: description } : false}
      >
        {description}
      </Typography.Text>
      {location?.status === 'found' && (
        <Typography.Text
          type="secondary"
          style={lineStyle}
          ellipsis={compact ? { tooltip: location.isp || '运营商未知' } : false}
        >
          {location.isp || '运营商未知'}
        </Typography.Text>
      )}
    </div>
  );
}
