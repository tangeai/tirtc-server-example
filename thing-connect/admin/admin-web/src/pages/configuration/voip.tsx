import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
  Row,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, json } from '../../api';
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
import { ServicePanel, type ConfigEntry } from './config-page';

export function VoIPPage() {
  const [apps, appsLoading, reloadApps] = useLoad(
    () => api<{ items: AnyRow[]; revision: number }>('/voip/apps'),
    [],
  );
  const [entries, , reloadEntries] = useLoad(
    () => api<{ items: ConfigEntry[] }>('/configs?namespace=voip-server'),
    [],
  );
  const [selected, setSelected] = useState<AnyRow>();
  const [devices, setDevices] = useState<PageData>();
  const [devicesLoading, setDevicesLoading] = useState(false);
  const [profile, setProfile] = useState<AnyRow>();
  const [appEdit, setAppEdit] = useState<AnyRow | null>(null);
  useEffect(() => {
    if (!selected) {
      setDevices(undefined);
      return;
    }
    setDevicesLoading(true);
    api<PageData>(`/voip/apps/${selected.app_id}/devices?page=1&page_size=100`)
      .then(setDevices)
      .catch((e) => message.error(e.message))
      .finally(() => setDevicesLoading(false));
  }, [selected]);
  const entry = entries?.items.find((x) => x.config_key === 'wechat.apps') || {
    namespace: 'voip-server',
    config_key: 'wechat.apps',
    value: { default_app_id: '', apps: {} },
    secret_configured: false,
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
      setAppEdit(null);
      reloadEntries();
      reloadApps();
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const openProfile = async (row: AnyRow) => {
    try {
      setProfile(await api(`/voip/devices/${row.device_id}/profile`));
    } catch (e) {
      message.error((e as Error).message);
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
      <Row gutter={16}>
        <Col span={10}>
          <Card
            title="小程序列表"
            extra={
              <Button icon={<ReloadOutlined />} onClick={reloadApps}>
                刷新
              </Button>
            }
          >
            <Table
              rowKey="app_id"
              loading={appsLoading}
              dataSource={apps?.items}
              pagination={false}
              onRow={(row) => ({ onClick: () => setSelected(row) })}
              columns={[
                {
                  title: '小程序 / 设备型号',
                  render: (_, r) => (
                    <>
                      <b>{r.app_id}</b>
                      <br />
                      <Typography.Text type="secondary">设备型号：{r.model_id}</Typography.Text>
                    </>
                  ),
                },
                {
                  title: '状态',
                  render: (_, r) => (
                    <Space direction="vertical" size={2}>
                      {r.enabled ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>}
                      {r.is_default ? <Tag color="blue">默认应用</Tag> : null}
                      <Tag
                        color={
                          r.config_status === 'healthy'
                            ? 'success'
                            : r.config_status === 'incomplete'
                              ? 'warning'
                              : 'default'
                        }
                      >
                        {voipConfigStatusNames[r.config_status] || r.config_status}
                      </Tag>
                    </Space>
                  ),
                },
                {
                  title: '密钥',
                  dataIndex: 'secret_configured',
                  render: (v) =>
                    v ? <Tag color="success">已配置</Tag> : <Tag color="warning">未配置</Tag>,
                },
                {
                  title: '设备授权',
                  render: (_, r) => (
                    <>
                      {r.active_auth_count} 个有效
                      <br />
                      {r.invalid_auth_count} 个失效
                    </>
                  ),
                },
                { title: '更新时间', dataIndex: 'updated_at', render: formatTime },
                {
                  title: '操作',
                  render: (_, r) => (
                    <Button
                      type="link"
                      onClick={(e) => {
                        e.stopPropagation();
                        setAppEdit(r);
                      }}
                    >
                      编辑
                    </Button>
                  ),
                },
              ]}
            />
          </Card>
        </Col>
        <Col span={14}>
          <Card title={selected ? `${selected.app_id} 的授权设备` : '请从左侧选择一个微信应用'}>
            <Table
              rowKey="id"
              loading={devicesLoading}
              dataSource={devices?.items}
              pagination={false}
              scroll={{ x: 1000 }}
              columns={[
                {
                  title: '设备',
                  render: (_, r) => (
                    <>
                      <b>{r.authorized_device_name || r.device_id}</b>
                      <br />
                      <Typography.Text type="secondary">{r.device_id}</Typography.Text>
                    </>
                  ),
                },
                { title: '所属用户', render: (_, r) => r.owner_email || '未绑定' },
                { title: '设备型号', dataIndex: 'wx_model_id' },
                { title: '微信用户标识', dataIndex: 'wx_open_id', ellipsis: true },
                {
                  title: '授权状态',
                  dataIndex: 'auth_status',
                  render: (v) => (
                    <Tag color={v === 'active' ? 'success' : 'default'}>
                      {voipAuthStatusNames[v] || v}
                    </Tag>
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
                  title: '失效原因',
                  dataIndex: 'invalid_reason',
                  render: (v) => voipInvalidReasonNames[v] || v || '—',
                },
                {
                  title: '设备上报属性',
                  render: (_, r) => (
                    <Button
                      type="link"
                      disabled={!r.profile_updated_at}
                      onClick={() => openProfile(r)}
                    >
                      查看
                    </Button>
                  ),
                },
              ]}
            />
          </Card>
        </Col>
      </Row>
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
              message="密钥不会回显；留空保留现有值。新增应用至少填写 AppSecret。"
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
