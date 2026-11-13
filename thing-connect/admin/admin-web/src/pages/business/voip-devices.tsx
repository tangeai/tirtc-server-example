import { QueryFilter, ProFormText, ProFormSelect } from '@ant-design/pro-components';
import { useEffect, useMemo, useState } from 'react';
import { Button, Card, Descriptions, Drawer, Table, Tag, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { api } from '../../api';
import {
  formatTime,
  pageTitle as title,
  useLoad,
  type AnyRow,
  type PageData,
} from '../../shared/admin-ui';
import {
  prettyJSON,
  profileFieldNames,
  voipAuthStatusNames,
  voipInvalidReasonNames,
} from '../../shared/admin-metadata';

export function VoIPDevicesPage() {
  const [apps, appsLoading, reloadApps] = useLoad(
    () => api<{ items: AnyRow[]; revision: number }>('/voip/apps'),
    [],
  );
  const [selectedAppID, setSelectedAppID] = useState('');
  const selected = useMemo(
    () => apps?.items.find((item) => item.app_id === selectedAppID),
    [apps, selectedAppID],
  );
  useEffect(() => {
    if (!apps) return;
    const items = apps.items;
    if (!items.length) {
      if (selectedAppID) setSelectedAppID('');
      return;
    }
    if (!items.some((item) => item.app_id === selectedAppID)) {
      setSelectedAppID((items.find((item) => item.is_default) || items[0]).app_id);
    }
  }, [apps, selectedAppID]);
  const [deviceQuery, setDeviceQuery] = useState('');
  const [authStatus, setAuthStatus] = useState('');
  const [profileReported, setProfileReported] = useState('');
  const [page, setPage] = useState(1);
  const pageSize = 20;
  const deviceParams = useMemo(() => {
    const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (deviceQuery) params.set('keyword', deviceQuery);
    if (authStatus) params.set('auth_status', authStatus);
    if (profileReported) params.set('profile_reported', profileReported);
    return params.toString();
  }, [page, deviceQuery, authStatus, profileReported]);
  const [devices, devicesLoading, reloadDevices] = useLoad(
    () =>
      selectedAppID
        ? api<PageData>(`/voip/apps/${encodeURIComponent(selectedAppID)}/devices?${deviceParams}`)
        : Promise.resolve<PageData>({ items: [], page, page_size: pageSize, total: 0 }),
    [selectedAppID, deviceParams],
  );
  const [access] = useLoad<{ permissions: string[] }>(() => api('/permissions'));
  const canReadProfile = !!access?.permissions.some((p) => p === '*' || p === 'voip.profile.read');
  const [profileID, setProfileID] = useState('');
  const [profile, profileLoading] = useLoad<AnyRow | undefined>(
    () =>
      profileID
        ? api(`/voip/devices/${encodeURIComponent(profileID)}/profile`)
        : Promise.resolve(undefined),
    [profileID],
  );
  const profileFields = [
    'screen_width',
    'screen_height',
    'camera_rotation',
    'down_video_rotation',
    'aspect_ratio',
    'hor_mirror',
    'vert_mirror',
    'object_fit',
    'video_res_mode',
    'audio_rate',
    'audio_channels',
    'up_video_mt',
    'down_video_mt',
    'down_audio_mt',
    'no_video',
    'calling_timeout_sec',
  ];
  return (
    <>
      {title('VoIP 授权设备', '按微信小程序查看授权设备、授权状态与设备上报属性')}
      <Card
        title="授权设备"
        style={{ marginTop: 16 }}
        extra={
          <Button
            icon={<ReloadOutlined />}
            onClick={() => {
              reloadApps();
              reloadDevices();
            }}
          >
            刷新
          </Button>
        }
      >
        <QueryFilter
          name="voip-devices-query-1"
          defaultCollapsed={false}
          onFinish={async (values) => {
            setPage(1);
            setDeviceQuery(values.keyword?.trim() || '');
            setAuthStatus(values.auth_status || '');
            setProfileReported(values.profile_reported || '');
          }}
          onReset={() => {
            setPage(1);
            setDeviceQuery('');
            setAuthStatus('');
            setProfileReported('');
          }}
        >
          <ProFormSelect
            label="微信小程序"
            fieldProps={{
              loading: appsLoading,
              showSearch: true,
              optionFilterProp: 'label',
              value: selectedAppID || undefined,
              onChange: (value) => {
                setPage(1);
                setSelectedAppID(String(value));
              },
            }}
            options={(apps?.items || []).map((item) => ({
              value: item.app_id,
              label: `${item.app_id} / ${item.model_id}`,
            }))}
            allowClear={false}
          />
          <ProFormText
            name="keyword"
            label="设备 / 用户"
            placeholder="设备 ID、名称、用户邮箱或 OpenID"
          />
          <ProFormSelect
            name="auth_status"
            label="授权状态"
            options={[
              { label: '有效', value: 'active' },
              { label: '失效', value: 'invalid' },
            ]}
          />
          <ProFormSelect
            name="profile_reported"
            label="属性上报"
            options={[
              { label: '已上报', value: 'true' },
              { label: '未上报', value: 'false' },
            ]}
          />
        </QueryFilter>
        {selected && (
          <Typography.Paragraph type="secondary">
            有效授权 {selected.active_auth_count ?? '—'} 个，失效{' '}
            {selected.invalid_auth_count ?? '—'} 个
          </Typography.Paragraph>
        )}
        <Table
          rowKey="id"
          loading={devicesLoading}
          dataSource={devices?.items}
          locale={{ emptyText: selectedAppID ? '没有符合条件的授权设备' : '请先选择微信小程序' }}
          pagination={{
            current: page,
            pageSize,
            total: devices?.total || 0,
            showSizeChanger: false,
            showTotal: (total) => `共 ${total} 条`,
            onChange: setPage,
          }}
          scroll={{ x: 1000 }}
          columns={[
            {
              title: '设备',
              render: (_, r) => (
                <>
                  <b>{r.authorized_device_name || r.device_id}</b>
                  <br />
                  <Typography.Text type="secondary">{r.device_id}</Typography.Text>
                  {r.wx_model_id ? (
                    <>
                      <br />
                      <Typography.Text type="secondary">型号：{r.wx_model_id}</Typography.Text>
                    </>
                  ) : null}
                </>
              ),
            },
            { title: '所属用户', render: (_, r) => r.owner_email || '未绑定' },
            { title: '微信用户标识', dataIndex: 'wx_open_id', ellipsis: true },
            {
              title: '授权状态',
              render: (_, r) => (
                <>
                  <Tag color={r.auth_status === 'active' ? 'success' : 'default'}>
                    {voipAuthStatusNames[r.auth_status] || r.auth_status}
                  </Tag>
                  {r.invalid_reason ? (
                    <>
                      <br />
                      <Typography.Text type="secondary">
                        {voipInvalidReasonNames[r.invalid_reason] || r.invalid_reason}
                      </Typography.Text>
                    </>
                  ) : null}
                </>
              ),
            },
            {
              title: '授权时间 / 最近校验',
              render: (_, r) => (
                <>
                  {formatTime(r.created_at)}
                  <br />
                  {r.last_verified_at ? formatTime(r.last_verified_at) : '尚未校验'}
                </>
              ),
            },
            {
              title: '设备上报属性',
              render: (_, r) =>
                r.profile_updated_at ? (
                  <Button
                    type="link"
                    disabled={!canReadProfile}
                    onClick={() => setProfileID(r.device_id)}
                  >
                    查看
                  </Button>
                ) : (
                  <Typography.Text type="secondary">未上报</Typography.Text>
                ),
            },
          ]}
        />
      </Card>
      <Drawer
        width="min(760px, 100vw)"
        open={!!profileID}
        loading={profileLoading}
        title={`设备上报属性：${profileID}`}
        onClose={() => setProfileID('')}
      >
        <Typography.Paragraph type="secondary">
          更新时间：{formatTime(profile?.updated_at)}
        </Typography.Paragraph>
        {profile && (
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, sm: 3 }}
            items={[
              {
                key: 'chip_model',
                label: '芯片型号',
                children: profile.hardware?.chip_model || '未上报',
              },
              {
                key: 'board_model',
                label: '开发板型号',
                children: profile.hardware?.board_model || '未上报',
              },
              {
                key: 'firmware_version',
                label: '固件版本',
                children: profile.firmware_version || '未上报',
              },
            ]}
          />
        )}
        <Typography.Title level={5}>VoIP 媒体属性</Typography.Title>
        {profile && (
          <Descriptions
            bordered
            size="small"
            column={{ xs: 1, sm: 2 }}
            items={profileFields.map((key) => ({
              key,
              label: (
                <>
                  {profileFieldNames[key] || key}
                  <br />
                  <Typography.Text type="secondary" code>
                    {key}
                  </Typography.Text>
                </>
              ),
              children: String(profile.attributes?.[key] ?? '—'),
            }))}
          />
        )}
        <Typography.Title level={5}>原始 VoIP 属性（供开发排查）</Typography.Title>
        <pre className="profile-json">{profile ? prettyJSON(profile.attributes) : ''}</pre>
      </Drawer>
    </>
  );
}
