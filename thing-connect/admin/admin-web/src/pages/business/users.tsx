import {
  QueryFilter,
  ProFormText,
  ProFormSelect,
  ProFormDateRangePicker,
} from '@ant-design/pro-components';
import { IPAddress } from '../../shared/ip-address';
import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Form,
  Grid,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  App as AntApp,
} from 'antd';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import {
  formatTime,
  pageTitle as title,
  useLoad,
  type AnyRow,
  type PageData,
} from '../../shared/admin-ui';
import { assignmentName } from '../../shared/admin-metadata';

export function UsersPage() {
  const { message } = AntApp.useApp();
  const [saving, setSaving] = useState(false);
  const screens = Grid.useBreakpoint();
  const [query, setQuery] = useState('');
  const [status, setStatus] = useState('');
  const [createdFrom, setCreatedFrom] = useState('');
  const [createdTo, setCreatedTo] = useState('');
  const [createdOrder, setCreatedOrder] = useState<'asc' | 'desc'>('desc');
  const [page, setPage] = useState(1);
  const pageSize = 20;
  const [data, loading, reload] = useLoad(
    () =>
      api<PageData>(
        `/users?page=${page}&page_size=${pageSize}&keyword=${encodeURIComponent(query)}&status=${status}&created_from=${createdFrom}&created_to=${createdTo}&sort_by=created_at&sort_order=${createdOrder}`,
      ),
    [query, status, createdFrom, createdTo, createdOrder, page],
  );
  const [quota, setQuota] = useState<AnyRow>();
  const [detail, setDetail] = useState<AnyRow>();
  const [resetUser, setResetUser] = useState<AnyRow>();
  const saveQuota = async (v: AnyRow) => {
    if (saving) return;
    setSaving(true);
    try {
      await api(
        `/users/${quota!.id}/bind-quota`,
        json('PUT', {
          bind_quota: v.bind_quota,
          expected_quota: quota!.bind_quota,
          reason: v.reason,
        }),
      );
      setQuota(undefined);
      reload();
    } catch (e) {
      reportError(e);
    } finally {
      setSaving(false);
    }
  };
  const openDetail = async (row: AnyRow) => {
    try {
      setDetail(await api(`/users/${row.id}`));
    } catch (e) {
      reportError(e);
    }
  };
  const sendReset = async (v: AnyRow) => {
    if (!resetUser) return;
    if (saving) return;
    setSaving(true);
    try {
      await api(`/users/${resetUser.id}/password-reset-email`, json('POST', { reason: v.reason }));
      message.success('密码重置邮件已进入发送队列');
      setResetUser(undefined);
    } catch (e) {
      reportError(e);
    } finally {
      setSaving(false);
    }
  };
  const pagination = {
    current: page,
    pageSize,
    total: data?.total || 0,
    showSizeChanger: false,
    showTotal: (total: number) => `共 ${total} 条`,
    onChange: setPage,
  };
  return (
    <>
      {title(
        '用户管理',
        '账号状态、设备归属与绑定额度',
        <Button onClick={reload} loading={loading}>
          刷新
        </Button>,
      )}
      <Card>
        <QueryFilter
          name="users-query-1"
          defaultCollapsed={false}
          onFinish={async (values) => {
            setPage(1);
            setQuery(values.keyword?.trim() || '');
            setStatus(values.status || '');
            setCreatedFrom(values.created?.[0] || '');
            setCreatedTo(values.created?.[1] || '');
          }}
          onReset={() => {
            setPage(1);
            setQuery('');
            setStatus('');
            setCreatedFrom('');
            setCreatedTo('');
          }}
        >
          <ProFormText name="keyword" label="用户" placeholder="用户 ID 或邮箱" />
          <ProFormSelect
            name="status"
            label="账号状态"
            placeholder="全部状态"
            options={[
              { label: '正常', value: '1' },
              { label: '禁用', value: '0' },
            ]}
          />
          <ProFormDateRangePicker name="created" label="注册日期" />
        </QueryFilter>
        <Table
          rowKey="id"
          loading={loading}
          dataSource={data?.items}
          scroll={{ x: 1500 }}
          tableLayout="fixed"
          pagination={pagination}
          onChange={(_, __, sorter, extra) => {
            if (extra.action !== 'sort') return;
            const order = Array.isArray(sorter) ? sorter[0]?.order : sorter.order;
            setPage(1);
            setCreatedOrder(order === 'ascend' ? 'asc' : 'desc');
          }}
          columns={[
            {
              title: '用户',
              width: 240,
              render: (_, r) => (
                <>
                  <Typography.Text strong ellipsis={{ tooltip: r.email }}>
                    {r.email}
                  </Typography.Text>
                  <br />
                  <Typography.Text type="secondary">用户编号 #{r.id}</Typography.Text>
                </>
              ),
            },
            {
              title: '状态',
              width: 80,
              dataIndex: 'status',
              render: (v) => (v === 1 ? <Tag color="success">正常</Tag> : <Tag>禁用</Tag>),
            },
            {
              title: '注册 IP / 归属地',
              width: 240,
              dataIndex: 'registration_ip',
              render: (v, row) => (
                <IPAddress compact ip={v} location={row.registration_ip_location} />
              ),
            },
            {
              title: '最近登录 IP / 归属地',
              width: 240,
              dataIndex: 'last_login_ip',
              render: (v, row) => (
                <IPAddress compact ip={v} location={row.last_login_ip_location} />
              ),
            },
            { title: '已绑定设备', width: 112, dataIndex: 'device_count' },
            { title: '剩余绑定额度', width: 120, dataIndex: 'bind_quota' },
            {
              title: '注册时间',
              width: 180,
              dataIndex: 'created_at',
              render: formatTime,
              sorter: true,
              sortOrder: createdOrder === 'asc' ? 'ascend' : 'descend',
              sortDirections: ['descend', 'ascend'],
            },
            {
              title: '操作',
              width: 200,
              fixed: screens.md ? 'right' : undefined,
              render: (_, r) => (
                <Space wrap size={0}>
                  <Button type="link" onClick={() => openDetail(r)}>
                    详情
                  </Button>
                  <Button type="link" onClick={() => setQuota(r)}>
                    调整额度
                  </Button>
                  <Button type="link" onClick={() => setResetUser(r)}>
                    发送重置邮件
                  </Button>
                  <Popconfirm
                    title={r.status === 1 ? '确认禁用用户？' : '确认启用用户？'}
                    onConfirm={async () => {
                      await api(
                        `/users/${r.id}/status`,
                        json('PUT', {
                          status: r.status === 1 ? 0 : 1,
                          expected_status: r.status,
                          reason: '后台账号状态管理',
                        }),
                      );
                      reload();
                    }}
                  >
                    <Button type="link" danger={r.status === 1}>
                      {r.status === 1 ? '禁用' : '启用'}
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        destroyOnHidden
        open={!!quota}
        title="调整绑定额度"
        footer={null}
        onCancel={() => setQuota(undefined)}
      >
        <Form
          name="users-1"
          layout="vertical"
          onFinish={saveQuota}
          key={quota?.id}
          initialValues={{ bind_quota: quota?.bind_quota }}
        >
          <Form.Item name="bind_quota" label="剩余绑定额度" rules={[{ required: true }]}>
            <InputNumber min={0} />
          </Form.Item>
          <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
            <Input placeholder="例如：客户套餐升级" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={saving}>
            保存
          </Button>
        </Form>
      </Modal>
      <Modal
        open={!!resetUser}
        title="发送密码重置邮件"
        footer={null}
        destroyOnHidden
        onCancel={() => setResetUser(undefined)}
      >
        <Form name="users-2" layout="vertical" onFinish={sendReset}>
          <Alert className="form-alert" type="info" message={`收件人：${resetUser?.email || ''}`} />
          <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
            <Input placeholder="例如：用户请求协助找回密码" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={saving}>
            确认发送
          </Button>
        </Form>
      </Modal>
      <Drawer
        width={screens.sm ? 860 : '100%'}
        open={!!detail}
        title={`用户详情：${detail?.user?.email || ''}`}
        onClose={() => setDetail(undefined)}
      >
        {detail && (
          <>
            <Descriptions
              bordered
              column={{ xs: 1, sm: 2 }}
              items={[
                { key: 'id', label: '用户编号', children: `#${detail.user.id}` },
                { key: 'email', label: '邮箱', children: detail.user.email },
                {
                  key: 'registration_ip',
                  label: '注册 IP',
                  children: (
                    <IPAddress
                      ip={detail.user.registration_ip}
                      location={detail.user.registration_ip_location}
                    />
                  ),
                },
                {
                  key: 'last_login_ip',
                  label: '最近成功登录 IP',
                  children: (
                    <IPAddress
                      ip={detail.user.last_login_ip}
                      location={detail.user.last_login_ip_location}
                    />
                  ),
                },
                {
                  key: 'last_login_at',
                  label: '最近成功登录时间',
                  children: formatTime(detail.user.last_login_at),
                },
                {
                  key: 'status',
                  label: '状态',
                  children: detail.user.status === 1 ? '正常' : '禁用',
                },
                { key: 'quota', label: '剩余绑定额度', children: detail.user.bind_quota },
                { key: 'ai_role', label: 'AI 角色数', children: detail.ai.role_count },
                { key: 'ai_resource', label: 'AI 资源数', children: detail.ai.resource_count },
              ]}
            />
            <Typography.Title level={5}>当前设备</Typography.Title>
            <Table<AnyRow>
              rowKey="id"
              size="small"
              pagination={false}
              scroll={{ x: 650 }}
              dataSource={detail.devices}
              columns={[
                { title: '设备 ID', dataIndex: 'device_id' },
                { title: '名称', dataIndex: 'device_name', render: (v) => v || '—' },
                { title: 'MAC 地址', dataIndex: 'mac', render: (v) => v || '—' },
                { title: '绑定时间', dataIndex: 'bind_time', render: formatTime },
              ]}
            />
            <Typography.Title level={5}>最近绑定历史</Typography.Title>
            <Table<AnyRow>
              rowKey="id"
              size="small"
              pagination={false}
              scroll={{ x: 650 }}
              dataSource={detail.bind_logs}
              columns={[
                { title: '时间', dataIndex: 'created_at', render: formatTime },
                { title: '设备 ID', dataIndex: 'device_id' },
                {
                  title: '操作',
                  dataIndex: 'action',
                  render: (v) => (v === 1 ? <Tag color="success">绑定</Tag> : <Tag>解绑</Tag>),
                },
                { title: 'MAC 地址', dataIndex: 'mac', render: (v) => v || '—' },
                { title: '设备来源', dataIndex: 'assign', render: assignmentName },
              ]}
            />
          </>
        )}
      </Drawer>
    </>
  );
}
