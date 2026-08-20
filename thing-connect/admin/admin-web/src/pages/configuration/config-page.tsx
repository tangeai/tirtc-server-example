import { useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { api, json } from '../../api';
import {
  StepUpFields,
  formatTime,
  pageTitle as title,
  serviceStatusTag as statusTag,
  useLoad,
  type AnyRow,
} from '../../shared/admin-ui';
import { FriendlyConfigFields, compactSecrets, type ConfigField } from '../../shared/config-fields';
import {
  configGroupNames,
  configNames,
  dependencyNames,
  serviceName,
} from '../../shared/admin-metadata';

export type Definition = {
  namespace: string;
  config_key: string;
  group: string;
  name: string;
  description?: string;
  default: unknown;
  secret_paths?: string[];
  fields?: ConfigField[];
  targets: string[];
};
export type ConfigEntry = {
  id?: number;
  namespace: string;
  config_key: string;
  value: unknown;
  secret_configured: boolean;
  revision: number;
  status: number;
};
const namespaceNames: Record<string, string> = {
  'device-server': '设备服务',
  'user-server': '用户服务',
  'voip-server': 'VoIP 服务',
  'ai-server': 'AI 服务',
  'call-server': '呼叫服务',
  common: '通用配置',
  system: '系统配置',
};
export function ConfigPage({
  namespace,
  embedded = false,
  excludeGroups = [],
}: {
  namespace: string;
  embedded?: boolean;
  excludeGroups?: string[];
}) {
  const [defs, , loadDefs] = useLoad(
    () => api<{ items: Definition[] }>(`/config-definitions?namespace=${namespace}`),
    [namespace],
  );
  const [entries, loading, loadEntries] = useLoad(
    () => api<{ items: ConfigEntry[] }>(`/configs?namespace=${namespace}`),
    [namespace],
  );
  const [editing, setEditing] = useState<{ definition: Definition; entry: ConfigEntry }>();
  const [adding, setAdding] = useState(false);
  const rows = useMemo(
    () =>
      defs?.items
        .filter((definition) => !excludeGroups.includes(definition.group))
        .map((definition) => ({
          definition,
          entry: entries?.items.find((x) => x.config_key === definition.config_key) || {
            namespace,
            config_key: definition.config_key,
            value: definition.default,
            secret_configured: false,
            revision: 0,
            status: 1,
          },
        })) || [],
    [defs, entries, namespace, excludeGroups.join(',')],
  );
  const uncreated = rows.filter((row) => !row.entry.id);
  const save = async (v: AnyRow) => {
    try {
      const fields = editing!.definition.fields;
      const configValue = fields?.length ? v.config : JSON.parse(v.value);
      if (editing!.definition.config_key === 'captcha' && !configValue.public_config)
        configValue.public_config = {};
      const body: AnyRow = {
        value: configValue,
        status: 1,
        expected_revision: editing!.entry.revision,
        reason: v.reason,
        confirm: true,
      };
      const secrets = fields
        ? compactSecrets(v.secrets)
        : v.secrets?.trim()
          ? JSON.parse(v.secrets)
          : undefined;
      if (secrets) body.secrets = secrets;
      if (namespace === 'system' && editing!.definition.config_key === 'mfa.policy')
        Object.assign(body, {
          current_password: v.current_password,
          current_mfa_code: v.current_mfa_code,
          current_recovery_code: v.current_recovery_code,
          confirm: true,
        });
      await api(`/configs/${namespace}/${editing!.definition.config_key}`, json('PUT', body));
      message.success('配置已发布');
      setEditing(undefined);
      loadEntries();
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const actions = (
    <Space>
      <Button href="/admin/audit-logs">操作日志</Button>
      <Button
        icon={<ReloadOutlined />}
        onClick={() => {
          loadDefs();
          loadEntries();
        }}
      >
        刷新
      </Button>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        disabled={!uncreated.length}
        onClick={() => setAdding(true)}
      >
        接管配置
      </Button>
    </Space>
  );
  return (
    <>
      {!embedded &&
        title(
          namespaceNames[namespace] || namespace,
          namespace === 'system' ? '管理后台安全与会话策略' : '服务状态、运行实例与注册配置项',
          actions,
        )}
      {!embedded && namespace === 'common' && <CommonServicePanel />}
      {!embedded && namespace !== 'system' && namespace !== 'common' && (
        <ServicePanel service={namespace} />
      )}
      <Card title={embedded ? '配置项' : undefined} extra={embedded ? actions : undefined}>
        <Table
          rowKey={(r) => r.definition.config_key}
          loading={loading}
          dataSource={rows}
          pagination={false}
          columns={[
            {
              title: '配置项',
              render: (_, r) => (
                <>
                  <b>{r.definition.name}</b>
                  {r.definition.description && (
                    <>
                      <br />
                      <Typography.Text type="secondary">{r.definition.description}</Typography.Text>
                    </>
                  )}
                  <br />
                  <Typography.Text type="secondary" code>
                    {r.definition.config_key}
                  </Typography.Text>
                </>
              ),
            },
            {
              title: '分组',
              render: (_, r) => configGroupNames[r.definition.group] || r.definition.group,
            },
            {
              title: '状态',
              render: (_, r) =>
                r.entry.id ? <Tag color="success">后台已接管</Tag> : <Tag>使用本地配置</Tag>,
            },
            {
              title: '密钥',
              render: (_, r) =>
                r.definition.secret_paths?.length ? (
                  <Tag color={r.entry.secret_configured ? 'success' : 'warning'}>
                    {r.entry.secret_configured ? '已配置' : '未配置'}
                  </Tag>
                ) : (
                  '—'
                ),
            },
            {
              title: '配置版本',
              render: (_, r) => (r.entry.revision ? `r${r.entry.revision}` : '—'),
            },
            {
              title: '生效服务',
              render: (_, r) => r.definition.targets.map(serviceName).join('、'),
            },
            {
              title: '操作',
              render: (_, r) => (
                <Button type="link" onClick={() => setEditing(r)}>
                  配置
                </Button>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        open={adding}
        title="由后台接管配置"
        footer={null}
        destroyOnClose
        onCancel={() => setAdding(false)}
      >
        <Form
          layout="vertical"
          onFinish={(v: AnyRow) => {
            const row = uncreated.find((x) => x.definition.config_key === v.config_key);
            if (row) {
              setAdding(false);
              setEditing(row);
            }
          }}
        >
          <Alert
            className="form-alert"
            type="info"
            showIcon
            message="发布后服务优先使用后台值；未接管的配置继续使用各服务 config.yaml。"
          />
          <Form.Item name="config_key" label="选择配置项" rules={[{ required: true }]}>
            <Select
              options={uncreated.map((x) => ({
                label: x.definition.name,
                value: x.definition.config_key,
              }))}
            />
          </Form.Item>
          <Button type="primary" htmlType="submit">
            填写配置
          </Button>
        </Form>
      </Modal>
      <Modal
        width={760}
        open={!!editing}
        title={editing?.definition.name}
        footer={null}
        destroyOnClose
        onCancel={() => setEditing(undefined)}
      >
        {editing && (
          <Form
            layout="vertical"
            onFinish={save}
            initialValues={{
              config: editing.entry.value,
              secrets: {},
              value: JSON.stringify(editing.entry.value, null, 2),
            }}
          >
            <Alert
              className="form-alert"
              type="info"
              showIcon
              message={`当前配置版本 r${editing.entry.revision}。密钥留空会保留原值，后台不会回显已保存的密钥。`}
            />
            {editing.definition.fields?.length ? (
              <FriendlyConfigFields
                configKey={editing.definition.config_key}
                fields={editing.definition.fields}
              />
            ) : (
              <>
                <Alert
                  className="form-alert"
                  type="warning"
                  showIcon
                  message="该扩展配置尚未注册可视化字段，请由熟悉配置结构的开发者编辑。"
                />
                <Form.Item name="value" label="高级配置（JSON）" rules={[{ required: true }]}>
                  <Input.TextArea rows={12} className="code" />
                </Form.Item>
                {editing.definition.secret_paths?.length && (
                  <Form.Item name="secrets" label="高级密钥配置（JSON）">
                    <Input.TextArea rows={5} className="code" placeholder="留空保留原密钥" />
                  </Form.Item>
                )}
              </>
            )}
            {namespace === 'system' && editing.definition.config_key === 'mfa.policy' && (
              <>
                <Form.Item name="current_password" label="当前密码" rules={[{ required: true }]}>
                  <Input.Password />
                </Form.Item>
                <StepUpFields />
              </>
            )}
            <Form.Item name="reason" label="发布原因" rules={[{ required: true }]}>
              <Input placeholder="说明为什么调整此配置" />
            </Form.Item>
            <Button type="primary" htmlType="submit">
              校验并发布
            </Button>
          </Form>
        )}
      </Modal>
    </>
  );
}
const revisionText = (value: AnyRow) =>
  Object.entries(value || {}).length ? (
    <Space wrap>
      {Object.entries(value).map(([key, revision]) => (
        <Tag key={key} title={key}>
          {configNames[key] || key} r{String(revision)}
        </Tag>
      ))}
    </Space>
  ) : (
    '使用本地配置'
  );
const dependencyText = (value: AnyRow) =>
  Object.entries(value || {}).map(([key, status]) => (
    <Tag key={key} color={status === 'healthy' ? 'success' : 'error'}>
      {dependencyNames[key] || key} · {status === 'healthy' ? '正常' : '异常'}
    </Tag>
  ));
const instanceColumns = [
  { title: '实例标识', dataIndex: 'instance_id' },
  {
    title: '所在节点',
    render: (_: unknown, r: AnyRow) => `${r.node}${r.zone ? ` / ${r.zone}` : ''}`,
  },
  { title: '程序版本', dataIndex: 'version', render: (v: string) => v || '—' },
  { title: '依赖状态', render: (_: unknown, r: AnyRow) => dependencyText(r.dependencies) },
  { title: '已应用配置', render: (_: unknown, r: AnyRow) => revisionText(r.config_revision) },
  { title: '启动时间', dataIndex: 'started_at', render: formatTime },
  { title: '最近心跳', dataIndex: 'last_heartbeat', render: formatTime },
];
export function ServicePanel({ service }: { service: string }) {
  const [data, loading, reload] = useLoad(
    () => api<AnyRow>(`/services/${service}/status`),
    [service],
  );
  return (
    <Card
      className="service-card"
      loading={loading}
      title={`${serviceName(service)}状态`}
      extra={<Button type="text" icon={<ReloadOutlined />} onClick={reload} />}
    >
      {data && (
        <>
          <Space size="large">
            {statusTag(data.status)}
            <span>
              共 {data.instance_count} 个实例，{data.healthy_count} 个健康
            </span>
          </Space>
          <Table<AnyRow>
            className="instance-table"
            rowKey="instance_id"
            size="small"
            pagination={false}
            scroll={{ x: 1100 }}
            dataSource={data.instances}
            columns={instanceColumns}
          />
        </>
      )}
    </Card>
  );
}
function CommonServicePanel() {
  const targets = ['user-server', 'voip-server', 'ai-server', 'call-server'];
  const [data, loading, reload] = useLoad(
    () => Promise.all(targets.map((service) => api<AnyRow>(`/services/${service}/status`))),
    [],
  );
  const rows = data?.flatMap((service) =>
    service.instances.length
      ? service.instances.map((instance: AnyRow) => ({
          ...instance,
          service: service.service,
          status: service.status,
        }))
      : [{ service: service.service, status: service.status, instance_id: '—', node: '—' }],
  );
  return (
    <Card
      className="service-card"
      loading={loading}
      title="通用配置生效服务"
      extra={<Button type="text" icon={<ReloadOutlined />} onClick={reload} />}
    >
      {data && (
        <Table<AnyRow>
          rowKey={(r) => `${r.service}/${r.instance_id}`}
          size="small"
          pagination={false}
          scroll={{ x: 1200 }}
          dataSource={rows}
          columns={[
            { title: '服务', dataIndex: 'service', render: serviceName },
            { title: '状态', dataIndex: 'status', render: statusTag },
            ...instanceColumns,
          ]}
        />
      )}
    </Card>
  );
}
