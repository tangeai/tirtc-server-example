import { useEffect, useState } from 'react';
import {
  Alert,
  App as AntApp,
  Button,
  Card,
  Checkbox,
  Col,
  Form,
  Input,
  InputNumber,
  List,
  Progress,
  Result,
  Row,
  Select,
  Space,
  Spin,
  Steps,
  Tag,
  Typography,
} from 'antd';
import { ADMIN_PASSWORD_POLICY_MESSAGE, validateAdminPassword } from './password-policy';

type SetupMode = 'fresh' | 'recovery' | 'installed' | 'normal';
type MQTTAuthMode = 'username' | 'clientid';

export type SetupSnapshot = {
  mode: SetupMode;
  operation_id?: string;
  phase?: string;
  percent?: number;
  message?: string;
  retryable?: boolean;
  needs_token?: boolean;
  services?: { name: string; state: string; problem?: SetupProblem }[];
  problem?: SetupProblem;
};

export type SetupProblem = {
  code: string;
  message: string;
  suggestions?: string[];
};

type SetupDraft = {
  setup_token: string;
  admin_password_confirm: string;
  optional_services?: string[];
  database: {
    host: string;
    port: number;
    name: string;
    migration_user: string;
    migration_password: string;
    runtime_user?: string;
    runtime_password?: string;
    tls: string;
  };
  redis: { host: string; port: number; password?: string; db: number };
  mqtt: {
    broker: string;
    auth_mode: MQTTAuthMode;
    username?: string;
    client_ids?: Record<string, string>;
    password: string;
  };
  network: { public_base_url?: string; cookie_secure: boolean; trusted_proxies?: string };
  admin: { email: string; nick_name?: string; password: string };
};

type SetupPlan = {
  digest: string;
  database: {
    class: string;
    table_count: number;
    create_admin: boolean;
    description: string;
  };
  actions: string[];
  warnings?: string[];
};

type Envelope<T> = { code: number; msg: string; data?: T };

export class SetupRequestError extends Error {
  problem: SetupProblem;

  constructor(problem: SetupProblem) {
    super(problem.message);
    this.name = 'SetupRequestError';
    this.problem = problem;
  }
}

function fallbackProblem(error: unknown): SetupProblem {
  if (error instanceof SetupRequestError) return error.problem;
  return {
    code: 'REQUEST_FAILED',
    message: error instanceof Error ? error.message : '安装请求失败',
    suggestions: ['检查网络连接后重试；如果问题持续，请保留当前页面并检查 Admin 服务日志'],
  };
}

export async function setupAPI<T>(path: string, init: RequestInit = {}, token = ''): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body) headers.set('Content-Type', 'application/json');
  if (token) headers.set('X-Setup-Token', token);
  const response = await fetch(`/v1/setup${path}`, {
    ...init,
    headers,
    credentials: 'same-origin',
  });
  let body: Envelope<T>;
  try {
    body = (await response.json()) as Envelope<T>;
  } catch {
    throw new SetupRequestError({
      code: 'INVALID_RESPONSE',
      message: `安装服务返回了无效响应（HTTP ${response.status}）`,
      suggestions: ['确认请求到达 Admin Server，且反向代理没有替换 JSON 响应'],
    });
  }
  if (!response.ok || body.code !== 200) {
    const detail = body.data as SetupProblem | undefined;
    throw new SetupRequestError({
      code: detail?.code || `HTTP_${response.status}`,
      message: detail?.message || body.msg || `HTTP ${response.status}`,
      suggestions: detail?.suggestions,
    });
  }
  if (body.data === undefined) {
    throw new SetupRequestError({
      code: 'INVALID_RESPONSE',
      message: '安装服务响应缺少 data',
      suggestions: ['确认 Admin Server 与 Admin Web 来自同一版本，然后重新加载页面'],
    });
  }
  return body.data;
}

export async function loadSetupStatus(): Promise<SetupSnapshot | undefined> {
  const response = await fetch('/v1/setup/status', { credentials: 'same-origin' });
  if (response.status === 404) return undefined;
  const body = (await response.json()) as Envelope<SetupSnapshot>;
  if (!response.ok || body.code !== 200) throw new Error(body.msg || `HTTP ${response.status}`);
  return body.data;
}

export function SetupProblemAlert({
  problem,
  onClose,
}: {
  problem: SetupProblem;
  onClose?: () => void;
}) {
  const suggestions = problem.suggestions?.filter(Boolean) || [];
  return (
    <Alert
      type="error"
      showIcon
      closable={Boolean(onClose)}
      onClose={onClose}
      message={problem.message}
      description={
        suggestions.length ? (
          <div>
            <Typography.Text strong>处理建议</Typography.Text>
            <ul>
              {suggestions.map((suggestion) => (
                <li key={suggestion}>{suggestion}</li>
              ))}
            </ul>
          </div>
        ) : undefined
      }
    />
  );
}

function phaseIndex(phase?: string) {
  const phases = [
    'validating',
    'dependencies_verified',
    'database_claimed',
    'admin_ready',
    'awaiting_admin_restart',
    'starting_services',
    'installed',
  ];
  return Math.max(0, phases.indexOf(phase || 'validating'));
}

const serviceNames: Record<string, string> = {
  'admin-server': '管理后台',
  'device-server': '设备服务',
  'user-server': '用户服务',
  'voip-server': 'VoIP 服务',
  'ai-server': 'AI 服务',
  'call-server': '设备通话服务',
};

const serviceStates: Record<string, { label: string; color: string }> = {
  checking: { label: '正在检查', color: 'processing' },
  starting: { label: '正在启动', color: 'processing' },
  ready: { label: '已就绪', color: 'success' },
  failed: { label: '启动失败', color: 'error' },
  not_ready: { label: '未就绪', color: 'error' },
};

export function SetupPage({ initial }: { initial: SetupSnapshot }) {
  const [form] = Form.useForm<SetupDraft>();
  const { message: messageAPI } = AntApp.useApp();
  const [snapshot, setSnapshot] = useState(initial);
  const [plan, setPlan] = useState<SetupPlan>();
  const [draft, setDraft] = useState<SetupDraft>();
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [requestProblem, setRequestProblem] = useState<SetupProblem>();
  const mqttAuthMode = Form.useWatch(['mqtt', 'auth_mode'], form) || 'username';
  const optionalServices = Form.useWatch('optional_services', form) || [];
  const running = Boolean(snapshot.operation_id && snapshot.phase !== 'installed');
  const formProblem = requestProblem || (showForm && !plan ? snapshot.problem : undefined);

  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => {
      loadSetupStatus()
        .then((next) => {
          if (!next) return;
          setSnapshot(next);
          if (next.mode === 'installed') window.setTimeout(() => window.location.reload(), 1200);
        })
        .catch(() => {
          // A short connection failure is expected while Admin restarts.
        });
    }, 1500);
    return () => window.clearInterval(timer);
  }, [running]);

  const normalize = (values: SetupDraft) => {
    const {
      setup_token: _setupToken,
      admin_password_confirm: _adminPasswordConfirm,
      network,
      mqtt,
      ...rest
    } = values;
    const mqttServices = ['device-server', 'user-server'];
    if (rest.optional_services?.includes('voip-server')) mqttServices.push('voip-server');
    if (rest.optional_services?.includes('call-server')) mqttServices.push('call-server');
    const normalizedMQTT =
      mqtt.auth_mode === 'clientid'
        ? {
            broker: mqtt.broker,
            auth_mode: mqtt.auth_mode,
            client_ids: Object.fromEntries(
              mqttServices.map((service) => [service, mqtt.client_ids?.[service] || '']),
            ),
            password: mqtt.password,
          }
        : {
            broker: mqtt.broker,
            auth_mode: 'username' as const,
            username: mqtt.username || '',
            password: mqtt.password,
          };
    return {
      ...rest,
      mqtt: normalizedMQTT,
      network: {
        ...network,
        trusted_proxies: (network.trusted_proxies || '')
          .split(',')
          .map((value) => value.trim())
          .filter(Boolean),
      },
    };
  };

  const preview = async (values: SetupDraft) => {
    setBusy(true);
    setRequestProblem(undefined);
    try {
      const result = await setupAPI<SetupPlan>(
        '/preview',
        { method: 'POST', body: JSON.stringify(normalize(values)) },
        values.setup_token,
      );
      setToken(values.setup_token);
      setDraft(values);
      setPlan(result);
    } catch (error) {
      const problem = fallbackProblem(error);
      setRequestProblem(problem);
      messageAPI.error(problem.message);
    } finally {
      setBusy(false);
    }
  };

  const execute = async () => {
    if (!plan || !draft) return;
    setBusy(true);
    setRequestProblem(undefined);
    try {
      const result = await setupAPI<SetupSnapshot>(
        '/execute',
        {
          method: 'POST',
          body: JSON.stringify({ draft: normalize(draft), plan_digest: plan.digest }),
        },
        token,
      );
      setSnapshot(result);
      setPlan(undefined);
    } catch (error) {
      const problem = fallbackProblem(error);
      setRequestProblem(problem);
      messageAPI.error(problem.message);
    } finally {
      setBusy(false);
    }
  };

  const resume = async () => {
    setBusy(true);
    setRequestProblem(undefined);
    try {
      const result = await setupAPI<SetupSnapshot>(
        '/execute',
        { method: 'POST', body: JSON.stringify({}) },
        token,
      );
      setSnapshot(result);
    } catch (error) {
      const problem = fallbackProblem(error);
      setRequestProblem(problem);
      messageAPI.error(problem.message);
    } finally {
      setBusy(false);
    }
  };

  if (snapshot.mode === 'installed') {
    return (
      <div className="setup-shell">
        <Result status="success" title="ThingConnect 安装完成" subTitle="正在进入管理后台…" />
      </div>
    );
  }

  if ((running || snapshot.mode === 'recovery') && !showForm) {
    return (
      <div className="setup-shell">
        <Card className="setup-card">
          <Typography.Title level={2}>ThingConnect 安装与恢复</Typography.Title>
          <Steps
            current={phaseIndex(snapshot.phase)}
            size="small"
            items={[
              { title: '预检' },
              { title: '依赖' },
              { title: '数据库' },
              { title: '管理员' },
              { title: '配置' },
              { title: '服务' },
              { title: '完成' },
            ]}
          />
          <Progress
            percent={snapshot.percent || 0}
            status={snapshot.problem ? 'exception' : 'active'}
          />
          {requestProblem ? (
            <SetupProblemAlert
              problem={requestProblem}
              onClose={() => setRequestProblem(undefined)}
            />
          ) : snapshot.problem ? (
            <SetupProblemAlert problem={snapshot.problem} />
          ) : (
            <Alert type="info" showIcon message={snapshot.message || '正在处理'} />
          )}
          {snapshot.services?.length ? (
            <List
              className="setup-services"
              dataSource={snapshot.services}
              renderItem={(service) => (
                <List.Item>
                  <List.Item.Meta
                    title={`${serviceNames[service.name] || service.name}（${service.name}）`}
                    description={
                      service.problem ? (
                        <div>
                          <Typography.Text type="danger">{service.problem.message}</Typography.Text>
                          {service.problem.suggestions?.length ? (
                            <ul>
                              {service.problem.suggestions.map((suggestion) => (
                                <li key={suggestion}>{suggestion}</li>
                              ))}
                            </ul>
                          ) : null}
                        </div>
                      ) : undefined
                    }
                  />
                  <Tag color={serviceStates[service.state]?.color || 'default'}>
                    {serviceStates[service.state]?.label || service.state}
                  </Tag>
                </List.Item>
              )}
            />
          ) : null}
          {snapshot.retryable ? (
            <>
              <Space.Compact block className="setup-resume">
                <Input.Password
                  placeholder="输入一次性安装令牌"
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                />
                <Button type="primary" loading={busy} disabled={!token} onClick={resume}>
                  继续安装
                </Button>
              </Space.Compact>
              {(snapshot.percent || 0) < 70 ? (
                <Button block className="setup-actions" onClick={() => setShowForm(true)}>
                  重新输入连接信息
                </Button>
              ) : null}
            </>
          ) : (
            <Spin />
          )}
        </Card>
      </div>
    );
  }

  return (
    <div className="setup-shell">
      <Card className="setup-card setup-form-card">
        <Typography.Title level={2}>初始化 ThingConnect</Typography.Title>
        <Typography.Paragraph type="secondary">
          连接信息通过预检后才会执行安装。陌生非空数据库、未来版本或结构不一致的数据库不会被修改。
        </Typography.Paragraph>
        {formProblem ? (
          <SetupProblemAlert
            problem={formProblem}
            onClose={requestProblem ? () => setRequestProblem(undefined) : undefined}
          />
        ) : null}
        {plan ? (
          <>
            <Alert
              type="success"
              showIcon
              message={plan.database.description}
              description={`发现 ${plan.database.table_count} 张表；数据库动作将在锁内重新检查。`}
            />
            <Typography.Title level={4}>将执行</Typography.Title>
            <List
              size="small"
              dataSource={plan.actions}
              renderItem={(item) => <List.Item>{item}</List.Item>}
            />
            {plan.warnings?.map((warning) => (
              <Alert key={warning} type="warning" showIcon message={warning} />
            ))}
            <Space className="setup-actions">
              <Button onClick={() => setPlan(undefined)}>返回修改</Button>
              <Button type="primary" danger loading={busy} onClick={execute}>
                确认并开始安装
              </Button>
            </Space>
          </>
        ) : (
          <Form
            form={form}
            layout="vertical"
            initialValues={{
              optional_services: [],
              database: { host: '127.0.0.1', port: 3306, name: 'thing_connect', tls: 'false' },
              redis: { host: '127.0.0.1', port: 6379, db: 0 },
              mqtt: {
                broker: 'mqtt://127.0.0.1:1883',
                auth_mode: 'username',
                client_ids: {
                  'device-server': 'devicesrv',
                  'user-server': 'usrsrv',
                  'voip-server': 'voipsrv',
                  'call-server': 'callsrv',
                },
              },
              network: { cookie_secure: false, trusted_proxies: '127.0.0.1' },
            }}
            onFinish={preview}
          >
            <Typography.Title level={4}>安装授权</Typography.Title>
            <Form.Item name="setup_token" label="一次性安装令牌" rules={[{ required: true }]}>
              <Input.Password autoComplete="one-time-code" />
            </Form.Item>
            <Typography.Title level={4}>业务服务</Typography.Title>
            <Typography.Paragraph type="secondary">
              设备服务和用户服务是基础服务，固定安装。其他能力可按需启用，未选择的服务不会生成配置、启动或参与就绪检查。
            </Typography.Paragraph>
            <Space direction="vertical" className="setup-service-selection">
              <Checkbox checked disabled>
                设备服务（必需）
              </Checkbox>
              <Checkbox checked disabled>
                用户服务（必需）
              </Checkbox>
              <Form.Item name="optional_services" noStyle>
                <Checkbox.Group
                  options={[
                    { label: 'VoIP 服务（可选）', value: 'voip-server' },
                    { label: 'AI 服务（可选）', value: 'ai-server' },
                    { label: '设备通话服务（可选）', value: 'call-server' },
                  ]}
                />
              </Form.Item>
            </Space>
            <Typography.Title level={4}>MySQL 8</Typography.Title>
            <Row gutter={16}>
              <Col xs={24} md={16}>
                <Form.Item name={['database', 'host']} label="主机" rules={[{ required: true }]}>
                  <Input />
                </Form.Item>
              </Col>
              <Col xs={24} md={6}>
                <Form.Item name={['database', 'port']} label="端口" rules={[{ required: true }]}>
                  <InputNumber min={1} max={65535} className="full-width" />
                </Form.Item>
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} md={12}>
                <Form.Item
                  name={['database', 'name']}
                  label="数据库名"
                  rules={[{ required: true }]}
                >
                  <Input />
                </Form.Item>
              </Col>
              <Col xs={24} md={12}>
                <Form.Item name={['database', 'tls']} label="TLS 模式">
                  <Select
                    options={[
                      { value: 'false', label: '关闭（仅可信内网）' },
                      { value: 'true', label: '校验证书' },
                      { value: 'preferred', label: '优先 TLS' },
                    ]}
                  />
                </Form.Item>
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} md={12}>
                <Form.Item
                  name={['database', 'migration_user']}
                  label="安装/迁移账号"
                  rules={[{ required: true }]}
                >
                  <Input autoComplete="username" />
                </Form.Item>
              </Col>
              <Col xs={24} md={12}>
                <Form.Item
                  name={['database', 'migration_password']}
                  label="安装/迁移密码"
                  rules={[{ required: true }]}
                >
                  <Input.Password autoComplete="current-password" />
                </Form.Item>
              </Col>
            </Row>
            <Typography.Paragraph type="secondary">
              必须提供独立的长期运行账号，并仅授予业务表所需的 SELECT、INSERT、UPDATE、DELETE
              权限。安装器会在不写入数据的语句中逐表验证权限。
            </Typography.Paragraph>
            <Row gutter={16}>
              <Col xs={24} md={12}>
                <Form.Item
                  name={['database', 'runtime_user']}
                  label="DML 运行账号"
                  dependencies={[['database', 'migration_user']]}
                  rules={[
                    { required: true },
                    ({ getFieldValue }) => ({
                      validator(_, value) {
                        return value && value === getFieldValue(['database', 'migration_user'])
                          ? Promise.reject(new Error('运行账号必须与迁移账号分离'))
                          : Promise.resolve();
                      },
                    }),
                  ]}
                >
                  <Input autoComplete="username" />
                </Form.Item>
              </Col>
              <Col xs={24} md={12}>
                <Form.Item
                  name={['database', 'runtime_password']}
                  label="DML 运行密码"
                  rules={[{ required: true }]}
                >
                  <Input.Password autoComplete="new-password" />
                </Form.Item>
              </Col>
            </Row>
            <Typography.Title level={4}>Redis</Typography.Title>
            <Row gutter={16}>
              <Col xs={24} md={10}>
                <Form.Item name={['redis', 'host']} label="主机" rules={[{ required: true }]}>
                  <Input />
                </Form.Item>
              </Col>
              <Col xs={12} md={5}>
                <Form.Item name={['redis', 'port']} label="端口" rules={[{ required: true }]}>
                  <InputNumber min={1} max={65535} className="full-width" />
                </Form.Item>
              </Col>
              <Col xs={12} md={5}>
                <Form.Item name={['redis', 'db']} label="DB" rules={[{ required: true }]}>
                  <InputNumber min={0} className="full-width" />
                </Form.Item>
              </Col>
              <Col xs={24} md={4}>
                <Form.Item name={['redis', 'password']} label="密码">
                  <Input.Password />
                </Form.Item>
              </Col>
            </Row>
            <Typography.Title level={4}>MQTT</Typography.Title>
            <Row gutter={16}>
              <Col xs={24} md={14}>
                <Form.Item name={['mqtt', 'broker']} label="Broker" rules={[{ required: true }]}>
                  <Input placeholder="mqtt://127.0.0.1:1883" />
                </Form.Item>
              </Col>
              <Col xs={24} md={10}>
                <Form.Item
                  name={['mqtt', 'auth_mode']}
                  label="认证方式"
                  rules={[{ required: true }]}
                >
                  <Select
                    options={[
                      { value: 'username', label: 'Username（推荐）' },
                      { value: 'clientid', label: 'ClientID（单实例）' },
                    ]}
                  />
                </Form.Item>
              </Col>
            </Row>
            {mqttAuthMode === 'username' ? (
              <Form.Item
                name={['mqtt', 'username']}
                label="MQTT 用户名"
                rules={[{ required: true }]}
              >
                <Input autoComplete="username" />
              </Form.Item>
            ) : (
              <>
                <Alert
                  type="warning"
                  showIcon
                  message="ClientID 模式要求每个服务使用不同的已注册 ClientID；扩容多副本时请改用 Username 模式。"
                />
                <Row gutter={16}>
                  <Col xs={24} md={12}>
                    <Form.Item
                      name={['mqtt', 'client_ids', 'device-server']}
                      label="设备服务 ClientID"
                      rules={[{ required: true }]}
                    >
                      <Input />
                    </Form.Item>
                  </Col>
                  <Col xs={24} md={12}>
                    <Form.Item
                      name={['mqtt', 'client_ids', 'user-server']}
                      label="用户服务 ClientID"
                      rules={[{ required: true }]}
                    >
                      <Input />
                    </Form.Item>
                  </Col>
                  {optionalServices.includes('voip-server') ? (
                    <Col xs={24} md={12}>
                      <Form.Item
                        name={['mqtt', 'client_ids', 'voip-server']}
                        label="VoIP 服务 ClientID"
                        rules={[{ required: true }]}
                      >
                        <Input />
                      </Form.Item>
                    </Col>
                  ) : null}
                  {optionalServices.includes('call-server') ? (
                    <Col xs={24} md={12}>
                      <Form.Item
                        name={['mqtt', 'client_ids', 'call-server']}
                        label="设备通话服务 ClientID"
                        rules={[{ required: true }]}
                      >
                        <Input />
                      </Form.Item>
                    </Col>
                  ) : null}
                </Row>
              </>
            )}
            <Row gutter={16}>
              <Col xs={24} md={12}>
                <Form.Item
                  name={['mqtt', 'password']}
                  label="MQTT 密码"
                  rules={[{ required: true }]}
                >
                  <Input.Password autoComplete="current-password" />
                </Form.Item>
              </Col>
            </Row>
            <Typography.Title level={4}>首个管理员与访问</Typography.Title>
            <Row gutter={16}>
              <Col xs={24} md={6}>
                <Form.Item
                  name={['admin', 'email']}
                  label="管理员邮箱"
                  rules={[{ required: true, type: 'email' }]}
                >
                  <Input autoComplete="username" />
                </Form.Item>
              </Col>
              <Col xs={24} md={6}>
                <Form.Item name={['admin', 'nick_name']} label="昵称">
                  <Input />
                </Form.Item>
              </Col>
              <Col xs={24} md={8}>
                <Form.Item
                  name={['admin', 'password']}
                  label="管理员密码"
                  rules={[
                    { required: true, message: '请输入管理员密码' },
                    { validator: validateAdminPassword },
                  ]}
                  extra={ADMIN_PASSWORD_POLICY_MESSAGE}
                >
                  <Input.Password autoComplete="new-password" />
                </Form.Item>
              </Col>
              <Col xs={24} md={6}>
                <Form.Item
                  name="admin_password_confirm"
                  label="确认管理员密码"
                  dependencies={[['admin', 'password']]}
                  rules={[
                    { required: true, message: '请再次输入管理员密码' },
                    ({ getFieldValue }) => ({
                      validator(_, value) {
                        return !value || getFieldValue(['admin', 'password']) === value
                          ? Promise.resolve()
                          : Promise.reject(new Error('两次输入的管理员密码不一致'));
                      },
                    }),
                  ]}
                >
                  <Input.Password autoComplete="new-password" />
                </Form.Item>
              </Col>
            </Row>
            <Form.Item
              name={['network', 'public_base_url']}
              label="统一对外访问地址（配置反向代理时填写，可选）"
            >
              <Input placeholder="http://example.com" />
            </Form.Item>
            <Form.Item
              name={['network', 'trusted_proxies']}
              label="可信代理 CIDR（可选，逗号分隔）"
            >
              <Input placeholder="127.0.0.1, 10.0.0.0/8" />
            </Form.Item>
            <Form.Item name={['network', 'cookie_secure']} valuePropName="checked">
              <Checkbox>管理后台通过 HTTPS 访问（生产环境推荐）</Checkbox>
            </Form.Item>
            <Button block type="primary" htmlType="submit" loading={busy}>
              连接检查并生成安装计划
            </Button>
          </Form>
        )}
      </Card>
    </div>
  );
}
