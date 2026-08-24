import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
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
  voipConfigStatusNames,
  voipInvalidReasonNames,
} from '../../shared/admin-metadata';
import { ConfigPage, ServicePanel, type ConfigEntry } from './config-page';

export function VoIPPage() {
  const [apps, appsLoading, reloadApps] = useLoad(
    () => api<{ items: AnyRow[]; revision: number }>('/voip/apps'),
    [],
  );
  const [entries, , reloadEntries] = useLoad(
    () => api<{ items: ConfigEntry[] }>('/configs?namespace=voip-server'),
    [],
  );
  const [selectedAppID, setSelectedAppID] = useState('');
  const selected = useMemo(
    () => apps?.items.find((item) => item.app_id === selectedAppID),
    [apps, selectedAppID],
  );
  useEffect(() => {
    const items = apps?.items || [];
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
  const [profile, setProfile] = useState<AnyRow>();
  const [appEdit, setAppEdit] = useState<AnyRow | null>(null);
  const entry = entries?.items.find((x) => x.config_key === 'wechat.apps') || {
    namespace: 'voip-server',
    config_key: 'wechat.apps',
    value: { default_app_id: '', apps: {} },
    secret_configured: false,
    using_default: true,
    revision: 0,
    status: 1,
  };
  const saveApp = async (v: AnyRow) => {
    try {
      const current = structuredClone(entry.value as AnyRow);
      current.apps ||= {};
      const appID = appEdit?.app_id || v.app_id.trim();
      current.apps[appID] = { enabled: !!v.enabled, model_id: v.model_id.trim() };
      if (v.is_default) current.default_app_id = appID;
      else if (current.default_app_id === appID) {
        const replacement = Object.entries(current.apps).find(
          ([id, item]) => id !== appID && (item as AnyRow).enabled,
        );
        current.default_app_id = replacement?.[0] || '';
      }
      const secretValues: AnyRow = {};
      for (const key of ['secret', 'token', 'encoding_aes_key'])
        if (v[key]?.trim()) secretValues[key] = v[key].trim();
      const body: AnyRow = {
        value: current,
        status: 1,
        expected_revision: entry.revision,
        reason: v.reason,
        confirm: true,
      };
      if (Object.keys(secretValues).length) body.secrets = { apps: { [appID]: secretValues } };
      await api('/configs/voip-server/wechat.apps', json('PUT', body));
      message.success('微信应用已发布');
      setSelectedAppID(appID);
      setAppEdit(null);
      reloadEntries();
      reloadApps();
    } catch (e) {
      reportError(e);
    }
  };
  const openProfile = async (row: AnyRow) => {
    try {
      setProfile(await api(`/voip/devices/${row.device_id}/profile`));
    } catch (e) {
      reportError(e);
    }
  };
  const profileFields = [
    'screen_width',
    'screen_height',
    'camera_rotation',
    'aspect_ratio',
    'hor_mirror',
    'vert_mirror',
    'object_fit',
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
      {title(
        'VoIP 服务',
        '微信小程序应用、设备授权与设备上报属性',
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setAppEdit({})}>
          新增微信应用
        </Button>,
      )}
      <ServicePanel service="voip-server" />
      <ConfigPage namespace="voip-server" embedded excludeGroups={['wechat']} />
      <Card title="授权设备" style={{ marginTop: 16 }}>
        <Space className="toolbar" wrap>
          <Select
            loading={appsLoading}
            showSearch
            optionFilterProp="label"
            placeholder="选择微信小程序"
            style={{ width: 320 }}
            value={selectedAppID || undefined}
            options={(apps?.items || []).map((item) => ({
              value: item.app_id,
              label: `${item.app_id} / ${item.model_id}`,
            }))}
            onChange={(value) => {
              setPage(1);
              setSelectedAppID(value);
            }}
          />
          <Input.Search
            allowClear
            placeholder="设备名称、ID、用户邮箱或微信 OpenID"
            style={{ width: 320 }}
            onSearch={(value) => {
              setPage(1);
              setDeviceQuery(value.trim());
            }}
          />
          <Select
            allowClear
            placeholder="授权状态"
            style={{ width: 130 }}
            options={[
              { label: '有效', value: 'active' },
              { label: '失效', value: 'invalid' },
            ]}
            onChange={(value) => {
              setPage(1);
              setAuthStatus(value ?? '');
            }}
          />
          <Select
            allowClear
            placeholder="属性上报状态"
            style={{ width: 150 }}
            options={[
              { label: '已上报', value: 'true' },
              { label: '未上报', value: 'false' },
            ]}
            onChange={(value) => {
              setPage(1);
              setProfileReported(value ?? '');
            }}
          />
          <Button disabled={!selected} onClick={() => setAppEdit(selected || null)}>
            编辑微信应用
          </Button>
          <Button
            icon={<ReloadOutlined />}
            onClick={() => {
              reloadApps();
              reloadDevices();
            }}
          >
            刷新
          </Button>
        </Space>
        {selected && (
          <Space className="toolbar" wrap>
            {selected.enabled ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>}
            {selected.is_default ? <Tag color="blue">默认应用</Tag> : null}
            <Tag
              color={
                selected.config_status === 'healthy'
                  ? 'success'
                  : selected.config_status === 'incomplete'
                    ? 'warning'
                    : 'default'
              }
            >
              {voipConfigStatusNames[selected.config_status] || selected.config_status}
            </Tag>
            {selected.secret_configured ? (
              <Tag color="success">密钥已配置</Tag>
            ) : (
              <Tag color="warning">密钥未配置</Tag>
            )}
            <Typography.Text type="secondary">
              有效授权 {selected.active_auth_count} 个，失效 {selected.invalid_auth_count} 个
            </Typography.Text>
          </Space>
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
                  <Button type="link" onClick={() => openProfile(r)}>
                    查看
                  </Button>
                ) : (
                  <Typography.Text type="secondary">未上报</Typography.Text>
                ),
            },
          ]}
        />
      </Card>
      <Modal
        open={appEdit !== null}
        title={appEdit?.app_id ? '编辑微信应用' : '新增微信应用'}
        footer={null}
        destroyOnClose
        onCancel={() => setAppEdit(null)}
      >
        {appEdit !== null && (
          <Form
            layout="vertical"
            onFinish={saveApp}
            initialValues={{
              app_id: appEdit.app_id,
              model_id: appEdit.model_id,
              enabled: appEdit.app_id ? appEdit.enabled : true,
              is_default: appEdit.app_id
                ? appEdit.is_default
                : Object.keys((entry.value as AnyRow).apps || {}).length === 0,
              secret: appEdit.app_id
                ? (entry.secrets?.apps as AnyRow | undefined)?.[appEdit.app_id]?.secret
                : '',
              token: appEdit.app_id
                ? (entry.secrets?.apps as AnyRow | undefined)?.[appEdit.app_id]?.token
                : '',
              encoding_aes_key: appEdit.app_id
                ? (entry.secrets?.apps as AnyRow | undefined)?.[appEdit.app_id]?.encoding_aes_key
                : '',
            }}
          >
            <Form.Item
              name="app_id"
              label="小程序 AppID"
              rules={[{ required: true }]}
              extra="微信公众平台中的小程序 AppID，创建后不可修改"
            >
              <Input disabled={!!appEdit.app_id} />
            </Form.Item>
            <Form.Item name="model_id" label="设备型号 ModelID" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Form.Item name="enabled" label="启用应用" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="is_default" label="设为默认应用" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Alert
              className="form-alert"
              type="info"
              showIcon
              message="密钥默认隐藏，点击眼睛可查看原值；新增并启用应用时必须填写 AppSecret。"
            />
            <Form.Item name="secret" label="小程序密钥（AppSecret）">
              <Input.Password />
            </Form.Item>
            <Form.Item name="token" label="消息校验令牌（Token）">
              <Input.Password />
            </Form.Item>
            <Form.Item name="encoding_aes_key" label="消息加解密密钥（EncodingAESKey）">
              <Input.Password />
            </Form.Item>
            <Form.Item name="reason" label="发布原因" rules={[{ required: true }]}>
              <Input placeholder="说明新增或修改原因" />
            </Form.Item>
            <Button type="primary" htmlType="submit">
              校验并发布
            </Button>
          </Form>
        )}
      </Modal>
      <Drawer
        width={760}
        open={!!profile}
        title={`设备上报属性：${profile?.device_id || ''}`}
        onClose={() => setProfile(undefined)}
      >
        <Typography.Paragraph type="secondary">
          更新时间：{formatTime(profile?.updated_at)}
        </Typography.Paragraph>
        {profile && (
          <Descriptions
            bordered
            size="small"
            column={2}
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
        <Typography.Title level={5}>原始上报数据（供开发排查）</Typography.Title>
        <pre className="profile-json">{profile ? prettyJSON(profile.attributes) : ''}</pre>
      </Drawer>
    </>
  );
}
