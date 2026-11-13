import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Space,
  Switch,
  Table,
  Tag,
  App as AntApp,
} from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import { pageTitle as title, useLoad, type AnyRow } from '../../shared/admin-ui';
import { voipConfigStatusNames } from '../../shared/admin-metadata';
import { ConfigPage, ServicePanel, type ConfigEntry } from './config-page';

export function VoIPPage() {
  const { message } = AntApp.useApp();
  const [saving, setSaving] = useState(false);
  const [access] = useLoad<{ permissions: string[] }>(() => api('/permissions'));
  const canWrite = !!access?.permissions.some((p) => p === '*' || p === 'voip.app.write');
  const [apps, appsLoading, reloadApps] = useLoad(
    () => api<{ items: AnyRow[]; revision: number }>('/voip/apps'),
    [],
  );
  const [entries, , reloadEntries] = useLoad(
    () => api<{ items: ConfigEntry[] }>('/configs?namespace=voip-server'),
    [],
  );
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
    if (saving) return;
    setSaving(true);
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
      reportError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <>
      {title(
        'VoIP 服务',
        '微信小程序应用、连接参数与配置发布',
        <Button
          type="primary"
          disabled={!canWrite}
          icon={<PlusOutlined />}
          onClick={() => setAppEdit({})}
        >
          新增微信应用
        </Button>,
      )}
      <ServicePanel service="voip-server" />
      <ConfigPage namespace="voip-server" embedded excludeGroups={['wechat']} />
      <Card
        title="微信小程序"
        extra={
          <Button
            icon={<ReloadOutlined />}
            loading={appsLoading}
            onClick={() => {
              reloadApps();
              reloadEntries();
            }}
          >
            刷新
          </Button>
        }
      >
        <Table
          rowKey="app_id"
          loading={appsLoading}
          dataSource={apps?.items}
          scroll={{ x: 800 }}
          columns={[
            { title: '小程序 AppID', dataIndex: 'app_id' },
            { title: '设备型号 ModelID', dataIndex: 'model_id' },
            {
              title: '状态',
              render: (_, app) => (
                <Space wrap>
                  {app.enabled ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>}
                  {app.is_default && <Tag color="blue">默认应用</Tag>}
                </Space>
              ),
            },
            {
              title: '配置状态',
              render: (_, app) => voipConfigStatusNames[app.config_status] || '未知',
            },
            { title: '密钥', render: (_, app) => (app.secret_configured ? '已配置' : '未配置') },
            {
              title: '操作',
              render: (_, app) => (
                <Button type="link" disabled={!canWrite} onClick={() => setAppEdit(app)}>
                  编辑
                </Button>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        open={appEdit !== null}
        title={appEdit?.app_id ? '编辑微信应用' : '新增微信应用'}
        footer={null}
        destroyOnHidden
        onCancel={() => setAppEdit(null)}
      >
        {appEdit !== null && (
          <Form
            name="voip-1"
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
            <Button type="primary" htmlType="submit" loading={saving}>
              校验并发布
            </Button>
          </Form>
        )}
      </Modal>
    </>
  );
}
