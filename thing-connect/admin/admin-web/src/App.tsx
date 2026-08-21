import { lazy, Suspense, useEffect, useMemo, useState } from 'react';
import {
  App as AntApp,
  Avatar,
  Button,
  Drawer,
  Form,
  Input,
  Layout,
  Menu,
  Modal,
  QRCode,
  Result,
  Space,
  Spin,
  Typography,
  message,
} from 'antd';
import {
  ApiOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  LogoutOutlined,
  MailOutlined,
  MenuOutlined,
  PhoneOutlined,
  RobotOutlined,
  SafetyOutlined,
  SettingOutlined,
  TeamOutlined,
  UnorderedListOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { BrowserRouter, useLocation, useNavigate } from 'react-router-dom';
import { api, json, restoreSession, setAccessToken } from './api';
import { StepUpFields } from './shared/admin-ui';
import { loadSetupStatus, SetupPage, type SetupSnapshot } from './setup';

const OverviewPage = lazy(() =>
  import('./pages/overview').then((module) => ({ default: module.OverviewPage })),
);
const UsersPage = lazy(() =>
  import('./pages/business/users').then((module) => ({ default: module.UsersPage })),
);
const DevicesPage = lazy(() =>
  import('./pages/business/devices').then((module) => ({ default: module.DevicesPage })),
);
const ConfigPage = lazy(() =>
  import('./pages/configuration/config-page').then((module) => ({ default: module.ConfigPage })),
);
const UserServicePage = lazy(() =>
  import('./pages/configuration/user-service').then((module) => ({
    default: module.UserServicePage,
  })),
);
const VoIPPage = lazy(() =>
  import('./pages/configuration/voip').then((module) => ({ default: module.VoIPPage })),
);
const AdminUsersPage = lazy(() =>
  import('./pages/access/admin-users').then((module) => ({ default: module.AdminUsersPage })),
);
const RolesPage = lazy(() =>
  import('./pages/access/roles').then((module) => ({ default: module.RolesPage })),
);
const DictionariesPage = lazy(() =>
  import('./pages/access/dictionaries').then((module) => ({ default: module.DictionariesPage })),
);
const JobsPage = lazy(() =>
  import('./pages/operations/jobs').then((module) => ({ default: module.JobsPage })),
);
const LogsPage = lazy(() =>
  import('./pages/operations/logs').then((module) => ({ default: module.LogsPage })),
);
type AuthResult = {
  stage: string;
  access_token?: string;
  mfa_challenge_token?: string;
  mfa_setup_token?: string;
};
type MenuRow = {
  id: number;
  parent_id: number;
  menu_code: string;
  name: string;
  path: string;
  icon: string;
  menu_type: number;
  sort_no: number;
};
type AdminProfile = { id: number; email: string; nick_name: string; must_change_password: number };
type AnyPasswordForm = {
  current_password: string;
  new_password: string;
  current_mfa_code?: string;
  current_recovery_code?: string;
};
type NavItem = { key: string; label: string; icon?: React.ReactNode; children?: NavItem[] };
const icons: Record<string, React.ReactNode> = {
  overview: <DashboardOutlined />,
  users: <TeamOutlined />,
  devices: <ApiOutlined />,
  business: <TeamOutlined />,
  'service-config': <SettingOutlined />,
  'system-management': <SafetyOutlined />,
  operations: <UnorderedListOutlined />,
  'device-config': <SettingOutlined />,
  'user-config': <MailOutlined />,
  'voip-config': <PhoneOutlined />,
  'ai-config': <RobotOutlined />,
  'call-config': <PhoneOutlined />,
  'common-config': <DatabaseOutlined />,
  'system-config': <SafetyOutlined />,
};
const registeredIcons: Record<string, React.ReactNode> = {
  DashboardOutlined: <DashboardOutlined />,
  TeamOutlined: <TeamOutlined />,
  ApiOutlined: <ApiOutlined />,
  SettingOutlined: <SettingOutlined />,
  SafetyOutlined: <SafetyOutlined />,
  UnorderedListOutlined: <UnorderedListOutlined />,
  MailOutlined: <MailOutlined />,
  PhoneOutlined: <PhoneOutlined />,
  RobotOutlined: <RobotOutlined />,
  DatabaseOutlined: <DatabaseOutlined />,
};
function navigationItems(menus: MenuRow[], parentID = 0): NavItem[] {
  const result: NavItem[] = [];
  for (const menu of menus
    .filter((item) => item.parent_id === parentID)
    .sort((a, b) => a.sort_no - b.sort_no || a.id - b.id)) {
    const children = navigationItems(menus, menu.id);
    const icon = registeredIcons[menu.icon] || icons[menu.menu_code];
    if (menu.menu_type === 1 || children.length) {
      if (children.length)
        result.push({ key: `directory:${menu.id}`, label: menu.name, icon, children });
    } else if (menu.path)
      result.push({ key: menu.path, label: menu.name, icon: icon || <UnorderedListOutlined /> });
  }
  return result;
}
function Login({ onDone }: { onDone: () => void }) {
  const [stage, setStage] = useState<AuthResult>();
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string }>();
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const submit = async (v: {
    email: string;
    password: string;
    code?: string;
    recovery_code?: string;
  }) => {
    try {
      let result: AuthResult;
      if (!stage) result = await api('/auth/login', json('POST', v));
      else if (stage.stage === 'mfa_required')
        result = await api(
          '/auth/mfa/verify',
          json('POST', {
            mfa_challenge_token: stage.mfa_challenge_token,
            code: v.code,
            recovery_code: v.recovery_code,
          }),
        );
      else {
        if (!setup) {
          const enrolled = await api<{ secret: string; otpauth_uri: string }>(
            '/me/mfa/totp/enroll',
            { method: 'POST', headers: { Authorization: `Bearer ${stage.mfa_setup_token}` } },
          );
          setSetup(enrolled);
          return;
        }
        const confirmed = await api<{ auth: AuthResult; recovery_codes: string[] }>(
          '/me/mfa/totp/confirm',
          {
            method: 'POST',
            headers: {
              Authorization: `Bearer ${stage.mfa_setup_token}`,
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({ code: v.code }),
          },
        );
        result = confirmed.auth;
        if (result.access_token) {
          setAccessToken(result.access_token);
          setRecoveryCodes(confirmed.recovery_codes);
          return;
        }
      }
      if (result.stage === 'authenticated' && result.access_token) {
        setAccessToken(result.access_token);
        onDone();
      } else setStage(result);
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  return (
    <div className="login">
      <div className="login-card">
        <Typography.Title level={2}>ThingConnect 管理后台</Typography.Title>
        <Typography.Paragraph type="secondary">
          管理员账号与普通用户账号相互独立
        </Typography.Paragraph>
        {recoveryCodes.length ? (
          <>
            <Typography.Paragraph type="warning">
              恢复码只显示这一次，请复制并保存到安全位置。
            </Typography.Paragraph>
            <Typography.Paragraph
              className="recovery-codes"
              copyable={{ text: recoveryCodes.join('\n') }}
            >
              {recoveryCodes.map((x) => (
                <code key={x}>{x}</code>
              ))}
            </Typography.Paragraph>
            <Button block type="primary" onClick={onDone}>
              我已安全保存
            </Button>
          </>
        ) : (
          <>
            {setup && (
              <div className="mfa-setup">
                <Typography.Paragraph>
                  使用支持 TOTP 的身份验证器扫描二维码，例如 Microsoft Authenticator、Google
                  Authenticator 或 1Password。
                </Typography.Paragraph>
                <QRCode value={setup.otpauth_uri} />
                <Typography.Paragraph copyable={{ text: setup.secret }}>
                  无法扫码时，手动输入密钥：<code>{setup.secret}</code>
                </Typography.Paragraph>
              </div>
            )}
            <Form layout="vertical" onFinish={submit}>
              {!stage && (
                <>
                  <Form.Item
                    name="email"
                    label="管理员邮箱"
                    rules={[{ required: true, type: 'email' }]}
                  >
                    <Input autoFocus autoComplete="username" />
                  </Form.Item>
                  <Form.Item name="password" label="密码" rules={[{ required: true }]}>
                    <Input.Password autoComplete="current-password" />
                  </Form.Item>
                </>
              )}
              {stage && (
                <>
                  <Form.Item
                    name="code"
                    label="身份验证器中的 6 位验证码"
                    rules={
                      stage.stage === 'mfa_setup_required' && setup ? [{ required: true }] : []
                    }
                  >
                    <Input
                      inputMode="numeric"
                      maxLength={6}
                      autoFocus
                      autoComplete="one-time-code"
                    />
                  </Form.Item>
                  {stage.stage === 'mfa_required' && (
                    <Form.Item name="recovery_code" label="或使用一次性恢复码">
                      <Input autoComplete="one-time-code" />
                    </Form.Item>
                  )}
                </>
              )}
              <Button block type="primary" htmlType="submit">
                {!stage
                  ? '登录'
                  : stage.stage === 'mfa_required'
                    ? '验证并登录'
                    : setup
                      ? '确认绑定'
                      : '开始绑定身份验证器'}
              </Button>
            </Form>
          </>
        )}
      </div>
    </div>
  );
}
function Shell({ onLogout }: { onLogout: () => void }) {
  const nav = useNavigate(),
    location = useLocation();
  const [ready, setReady] = useState(false);
  const [menus, setMenus] = useState<MenuRow[]>([]);
  const [currentUser, setCurrentUser] = useState<AdminProfile>();
  const [mustChangePassword, setMustChangePassword] = useState(false);
  const [navigationOpen, setNavigationOpen] = useState(false);
  useEffect(() => {
    api<{ user: AdminProfile }>('/me')
      .then((result) => {
        setCurrentUser(result.user);
        setMustChangePassword(result.user.must_change_password === 1);
        return api<{ menus: MenuRow[] }>('/me/navigation');
      })
      .then((m) => {
        setMenus(m.menus);
        setReady(true);
      })
      .catch((e) => {
        message.error(e.message);
        setReady(true);
      });
  }, []);
  const items = useMemo(() => navigationItems(menus), [menus]);
  const pagePaths = useMemo(
    () => menus.filter((menu) => menu.menu_type === 2 && menu.path).map((menu) => menu.path),
    [menus],
  );
  const path = location.pathname;
  useEffect(() => {
    if (ready && path === '/' && pagePaths.length) nav(pagePaths[0], { replace: true });
  }, [nav, pagePaths, path, ready]);
  const changePassword = async (v: AnyPasswordForm) => {
    try {
      await api('/me/password', json('PUT', v));
      message.success('密码已修改，请重新登录');
      setMustChangePassword(false);
      setAccessToken('');
      onLogout();
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  if (!ready) return <Spin fullscreen />;
  let page: React.ReactNode;
  if (path === '/') page = <Spin size="large" />;
  else if (!pagePaths.includes(path))
    page = (
      <Result
        status="403"
        title="无权访问"
        subTitle="当前角色没有此页面的访问权限，请从左侧菜单选择可用功能。"
        extra={
          pagePaths[0] ? (
            <Button type="primary" onClick={() => nav(pagePaths[0])}>
              前往可用页面
            </Button>
          ) : null
        }
      />
    );
  else if (path === '/overview') page = <OverviewPage />;
  else if (path === '/users') page = <UsersPage />;
  else if (path === '/devices') page = <DevicesPage />;
  else if (path === '/configs/user-server') page = <UserServicePage />;
  else if (path === '/configs/voip-server') page = <VoIPPage />;
  else if (path.startsWith('/configs/')) page = <ConfigPage namespace={path.split('/')[2]} />;
  else if (path === '/admin-users') page = <AdminUsersPage />;
  else if (path === '/access') page = <RolesPage />;
  else if (path === '/dictionaries') page = <DictionariesPage />;
  else if (path === '/jobs') page = <JobsPage />;
  else if (path === '/login-logs') page = <LogsPage kind="login" />;
  else if (path === '/audit-logs') page = <LogsPage kind="audit" />;
  else page = <Result status="404" title="页面未注册" subTitle="请检查后台菜单的组件路径。" />;
  const menu = (
    <Menu
      theme="dark"
      mode="inline"
      selectedKeys={[path]}
      items={items}
      onClick={({ key }) => {
        if (!key.startsWith('directory:')) {
          nav(key);
          setNavigationOpen(false);
        }
      }}
    />
  );
  return (
    <>
      <Layout className="shell">
        <Layout.Sider width={238} theme="dark" className="desktop-navigation">
          <div className="brand">
            TC <span>ThingConnect</span>
          </div>
          {menu}
        </Layout.Sider>
        <Layout className="shell-main">
          <Layout.Header className="top">
            <Space>
              <Button
                className="mobile-navigation-button"
                type="text"
                aria-label="打开导航菜单"
                icon={<MenuOutlined />}
                onClick={() => setNavigationOpen(true)}
              />
              <Typography.Text strong>
                {menus.find((x) => x.path === path)?.name || '管理后台'}
              </Typography.Text>
            </Space>
            <Space>
              <Avatar icon={<UserOutlined />} />
              <span>{currentUser?.nick_name || currentUser?.email}</span>
              <Button
                type="text"
                icon={<LogoutOutlined />}
                onClick={async () => {
                  await api('/auth/logout', { method: 'POST' }).catch(() => {});
                  setAccessToken('');
                  onLogout();
                }}
              >
                退出
              </Button>
            </Space>
          </Layout.Header>
          <Layout.Content className="content">
            <Suspense fallback={<Spin size="large" />}>{page}</Suspense>
          </Layout.Content>
        </Layout>
      </Layout>
      <Drawer
        className="mobile-navigation"
        width={280}
        placement="left"
        open={navigationOpen}
        title="ThingConnect"
        styles={{ body: { padding: 0, background: '#001529' } }}
        onClose={() => setNavigationOpen(false)}
      >
        {menu}
      </Drawer>
      <Modal
        open={mustChangePassword}
        title="首次登录请修改初始密码"
        footer={null}
        closable={false}
        maskClosable={false}
      >
        <Form layout="vertical" onFinish={changePassword}>
          <Form.Item name="current_password" label="当前密码" rules={[{ required: true }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[{ required: true, min: 12 }]}
            extra="至少 12 个字符"
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <StepUpFields />
          <Button block type="primary" htmlType="submit">
            修改并重新登录
          </Button>
        </Form>
      </Modal>
    </>
  );
}
function Root() {
  const [logged, setLogged] = useState<boolean>();
  const [setup, setSetup] = useState<SetupSnapshot>();
  const [setupChecked, setSetupChecked] = useState(false);
  useEffect(() => {
    const unauthorized = () => setLogged(false);
    window.addEventListener('admin:unauthorized', unauthorized);
    loadSetupStatus()
      .then((status) => {
        if (status && (status.mode === 'fresh' || status.mode === 'recovery')) {
          setSetup(status);
          return;
        }
        return restoreSession().then(setLogged);
      })
      .catch(() => restoreSession().then(setLogged))
      .finally(() => setSetupChecked(true));
    return () => window.removeEventListener('admin:unauthorized', unauthorized);
  }, []);
  if (!setupChecked) return <Spin fullscreen />;
  if (setup) return <SetupPage initial={setup} />;
  if (logged === undefined) return <Spin fullscreen />;
  return logged ? (
    <BrowserRouter basename="/admin">
      <Shell onLogout={() => setLogged(false)} />
    </BrowserRouter>
  ) : (
    <Login onDone={() => setLogged(true)} />
  );
}
export default function App() {
  return (
    <AntApp>
      <Root />
    </AntApp>
  );
}
