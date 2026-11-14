import { useRef, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Drawer,
  Form,
  Input,
  Modal,
  TreeSelect,
  Space,
  Switch,
  Table,
  Tag,
  App as AntApp,
} from 'antd';
import {
  PlusOutlined,
  EditOutlined,
  EyeOutlined,
  StopOutlined,
  ReloadOutlined,
  SaveOutlined,
  CheckOutlined,
} from '@ant-design/icons';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import { ADMIN_PASSWORD_POLICY_MESSAGE, validateAdminPassword } from '../../password-policy';
import {
  RefreshButton,
  formatTime,
  pageTitle as title,
  useLoad,
  type AnyRow,
  type PageData,
} from '../../shared/admin-ui';

import { hierarchy, type HierarchyRow } from './hierarchy';

export function AdminUsersPage() {
  const { message } = AntApp.useApp();
  const [saving, setSaving] = useState(false);
  const [page, setPage] = useState(1);
  const sessionRequest = useRef(0);
  const [data, loading, reload] = useLoad(
    () => api<PageData>(`/admin-users?page=${page}&page_size=20`),
    [page],
  );
  const [roleData] = useLoad(() => api<{ items: (AnyRow & HierarchyRow)[] }>('/roles'), []);
  const [editing, setEditing] = useState<AnyRow | null>(null);
  const [sensitive, setSensitive] = useState<{ type: 'sessions' | 'mfa'; row: AnyRow } | null>(
    null,
  );
  const [sessionOwner, setSessionOwner] = useState<AnyRow>();
  const [sessions, setSessions] = useState<AnyRow[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const roleIDs = (row: AnyRow) =>
    roleData?.items.filter((role) => row.roles?.includes(role.code)).map((role) => role.id) || [];
  const roleName = (code: string) =>
    roleData?.items.find((role) => role.code === code)?.name || code;
  const save = async (v: AnyRow) => {
    if (saving) return;
    setSaving(true);
    try {
      const body = {
        email: v.email,
        nick_name: v.nick_name,
        password: v.password || '',
        status: v.enabled ? 1 : 0,
        must_change_password: !!v.must_change_password,
        remark: v.remark || '',
        role_ids: v.role_ids || [],
        reason: v.reason,
      };
      if (editing?.id) await api(`/admin-users/${editing.id}`, json('PUT', body));
      else await api('/admin-users', json('POST', body));
      message.success('管理员已保存');
      setEditing(null);
      reload();
    } catch (e) {
      reportError(e);
    } finally {
      setSaving(false);
    }
  };
  const runSensitive = async (v: AnyRow) => {
    if (!sensitive) return;
    if (saving) return;
    setSaving(true);
    try {
      const path =
        sensitive.type === 'sessions'
          ? `/admin-users/${sensitive.row.id}/sessions/revoke`
          : `/admin-users/${sensitive.row.id}/mfa/reset`;
      await api(
        path,
        json('POST', {
          reason: v.reason,
        }),
      );
      message.success(sensitive.type === 'sessions' ? '会话已撤销' : '双重验证已重置');
      setSensitive(null);
      reload();
    } catch (e) {
      reportError(e);
    } finally {
      setSaving(false);
    }
  };
  const openSessions = async (row: AnyRow) => {
    const request = ++sessionRequest.current;
    setSessions([]);
    setSessionOwner(row);
    setSessionsLoading(true);
    try {
      const result = await api<{ items: AnyRow[] }>(`/admin-users/${row.id}/sessions`);
      if (request === sessionRequest.current) setSessions(result.items);
    } catch (e) {
      reportError(e);
    } finally {
      if (request === sessionRequest.current) setSessionsLoading(false);
    }
  };
  return (
    <>
      {title(
        '管理员',
        '管理员账号、角色、双重验证与登录会话',
        <Space wrap>
          <RefreshButton loading={loading} onClick={reload} />
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setEditing({})}>
            新增管理员
          </Button>
        </Space>,
      )}
      <Card>
        <Table
          scroll={{ x: 900 }}
          rowKey="id"
          loading={loading}
          dataSource={data?.items}
          pagination={{
            current: page,
            pageSize: 20,
            total: data?.total || 0,
            showSizeChanger: false,
            showTotal: (total) => `共 ${total} 位管理员`,
            onChange: setPage,
          }}
          columns={[
            {
              title: '管理员',
              render: (_, r) => (
                <>
                  <b>{r.nick_name}</b>
                  <br />
                  {r.email}
                </>
              ),
            },
            {
              title: '角色',
              dataIndex: 'roles',
              render: (v: string[]) =>
                v?.map((x) => (
                  <Tag key={x} title={x}>
                    {roleName(x)}
                  </Tag>
                )),
            },
            {
              title: '双重验证',
              dataIndex: 'mfa_enabled',
              render: (v) =>
                v ? <Tag color="success">已绑定</Tag> : <Tag color="warning">待绑定</Tag>,
            },
            {
              title: '状态',
              dataIndex: 'status',
              render: (v) => (v ? <Tag color="success">正常</Tag> : <Tag>禁用</Tag>),
            },
            { title: '最近登录', dataIndex: 'last_login_at', render: formatTime },
            {
              title: '操作',
              render: (_, r) => (
                <Space wrap>
                  <Button icon={<EditOutlined />} type="link" onClick={() => setEditing(r)}>
                    编辑
                  </Button>
                  <Button icon={<EyeOutlined />} type="link" onClick={() => openSessions(r)}>
                    查看会话
                  </Button>
                  <Button
                    icon={<StopOutlined />}
                    type="link"
                    onClick={() => setSensitive({ type: 'sessions', row: r })}
                  >
                    撤销会话
                  </Button>
                  {r.mfa_enabled ? (
                    <Button
                      icon={<ReloadOutlined />}
                      danger
                      type="link"
                      onClick={() => setSensitive({ type: 'mfa', row: r })}
                    >
                      重置双重验证
                    </Button>
                  ) : null}
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        open={editing !== null}
        title={editing?.id ? '编辑管理员' : '新增管理员'}
        footer={null}
        destroyOnHidden
        onCancel={() => setEditing(null)}
      >
        {editing !== null && (
          <Form
            name="admin-users-1"
            layout="vertical"
            onFinish={save}
            initialValues={{
              email: editing.email,
              nick_name: editing.nick_name,
              enabled: editing.id ? editing.status === 1 : true,
              must_change_password: editing.must_change_password === 1,
              remark: editing.remark,
              role_ids: editing.id ? roleIDs(editing) : [],
            }}
          >
            <Alert
              className="form-alert"
              type="info"
              showIcon
              message="保存管理员属于高风险操作"
              description="系统启用双重验证时，请填写当前登录管理员自己的身份验证器验证码或恢复码。"
            />
            <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email' }]}>
              <Input />
            </Form.Item>
            <Form.Item name="nick_name" label="昵称" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Form.Item
              name="password"
              label={editing.id ? '新密码（留空不修改）' : '初始密码'}
              rules={[
                ...(editing.id ? [] : [{ required: true, message: '请输入初始密码' }]),
                { validator: validateAdminPassword },
              ]}
              extra={ADMIN_PASSWORD_POLICY_MESSAGE}
            >
              <Input.Password />
            </Form.Item>
            <Form.Item
              name="role_ids"
              label="角色"
              extra="层级表示角色继承。选择子角色会继承启用的上级角色权限与菜单，不会自动分配其他子角色。"
              getValueProps={(ids: number[] = []) => ({
                value: ids.map((value) => ({
                  value,
                  label: roleData?.items.find((row) => row.id === value)?.name || `角色 #${value}`,
                })),
              })}
              getValueFromEvent={(values: { value: number }[]) => values.map((item) => item.value)}
              rules={[{ required: true, type: 'array', min: 1, message: '请至少选择一个角色' }]}
            >
              <TreeSelect
                treeCheckable
                treeCheckStrictly
                treeDefaultExpandAll
                showSearch
                treeNodeFilterProp="title"
                treeData={hierarchy(roleData?.items || []).map(function option(row): any {
                  return {
                    value: row.id,
                    title: `${row.name}${row.status === 1 ? '' : '（停用）'}`,
                    disabled: row.status !== 1,
                    children: row.children?.map(option),
                  };
                })}
              />
            </Form.Item>
            <Form.Item name="enabled" label="启用账号" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item
              name="must_change_password"
              label="下次登录时修改密码"
              valuePropName="checked"
            >
              <Switch />
            </Form.Item>
            <Form.Item name="remark" label="备注">
              <Input />
            </Form.Item>
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input placeholder="说明新增或修改原因" />
            </Form.Item>
            <Button icon={<SaveOutlined />} type="primary" htmlType="submit" loading={saving}>
              保存
            </Button>
          </Form>
        )}
      </Modal>
      <Modal
        open={!!sensitive}
        title={sensitive?.type === 'sessions' ? '撤销全部会话' : '重置管理员双重验证'}
        footer={null}
        destroyOnHidden
        onCancel={() => setSensitive(null)}
      >
        {sensitive && (
          <Form name="admin-users-2" layout="vertical" onFinish={runSensitive}>
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input placeholder="说明执行原因" />
            </Form.Item>
            <Button
              icon={<CheckOutlined />}
              danger
              type="primary"
              htmlType="submit"
              loading={saving}
            >
              确认执行
            </Button>
          </Form>
        )}
      </Modal>
      <Drawer
        width="min(760px, 100vw)"
        open={!!sessionOwner}
        title={`管理员会话：${sessionOwner?.email || ''}`}
        onClose={() => {
          sessionRequest.current++;
          setSessionOwner(undefined);
        }}
      >
        <Table
          scroll={{ x: 900 }}
          rowKey="id"
          loading={sessionsLoading}
          dataSource={sessions}
          pagination={false}
          columns={[
            { title: '会话标识', dataIndex: 'family_id', ellipsis: true },
            {
              title: '状态',
              render: (_, r) =>
                r.revoked_at ? (
                  <Tag>已撤销</Tag>
                ) : new Date(r.expires_at) <= new Date() ? (
                  <Tag>已过期</Tag>
                ) : (
                  <Tag color="success">有效</Tag>
                ),
            },
            { title: '登录时间', dataIndex: 'created_at', render: formatTime },
            { title: '有效期至', dataIndex: 'expires_at', render: formatTime },
            { title: '撤销原因', dataIndex: 'revoked_reason', render: (v) => v || '—' },
          ]}
        />
      </Drawer>
    </>
  );
}
