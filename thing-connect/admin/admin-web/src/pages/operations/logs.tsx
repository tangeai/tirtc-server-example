import { useState } from 'react';
import { Button, Card, Input, Select, Space, Table, Tag } from 'antd';
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
  auditActionName,
  auditActionNames,
  loginMessageNames,
  nameWithCode,
  resourceName,
  resourceNames,
} from '../../shared/admin-metadata';

export function LogsPage({ kind }: { kind: 'login' | 'audit' }) {
  const path = kind === 'login' ? '/login-logs' : '/audit-logs';
  const [page, setPage] = useState(1);
  const pageSize = 20;
  const [draft, setDraft] = useState<AnyRow>({});
  const [filters, setFilters] = useState<AnyRow>({});
  const query = Object.entries(filters)
    .filter(([, value]) => value !== '' && value !== undefined)
    .map(([key, value]) => `${key}=${encodeURIComponent(String(value))}`)
    .join('&');
  const [data, loading, reload] = useLoad(
    () => api<PageData>(`${path}?page=${page}&page_size=${pageSize}&${query}`),
    [path, page, query],
  );
  const applyFilters = () => {
    setPage(1);
    setFilters({ ...draft });
  };
  const columns =
    kind === 'login'
      ? [
          { title: '时间', dataIndex: 'created_at', render: formatTime },
          { title: '管理员', dataIndex: 'email', render: (v: string) => v || '未知账号' },
          {
            title: '结果',
            dataIndex: 'status',
            render: (v: number) =>
              v ? <Tag color="success">成功</Tag> : <Tag color="error">失败</Tag>,
          },
          { title: '登录地址', dataIndex: 'client_ip' },
          {
            title: '说明',
            dataIndex: 'message',
            render: (v: string) => loginMessageNames[v] || v || '—',
          },
        ]
      : [
          { title: '时间', dataIndex: 'created_at', render: formatTime },
          {
            title: '管理员',
            render: (_: unknown, r: AnyRow) => r.email || `管理员 #${r.admin_user_id}`,
          },
          {
            title: '操作',
            dataIndex: 'action',
            render: (v: string) => nameWithCode(auditActionName(v), v),
          },
          {
            title: '操作对象',
            render: (_: unknown, r: AnyRow) =>
              nameWithCode(
                `${resourceName(r.resource_type)} ${r.resource_id}`,
                `${r.resource_type}/${r.resource_id}`,
              ),
          },
          { title: '操作原因', dataIndex: 'reason', render: (v: string) => v || '—' },
          { title: '请求标识', dataIndex: 'request_id', ellipsis: true },
        ];
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
        kind === 'login' ? '登录日志' : '操作日志',
        kind === 'login' ? '管理员登录与双重验证结果' : '所有管理写操作的脱敏审计记录',
        <Button icon={<ReloadOutlined />} onClick={reload}>
          刷新
        </Button>,
      )}
      <Card>
        <Space className="toolbar" wrap>
          {kind === 'login' ? (
            <>
              <Input
                placeholder="管理员邮箱"
                allowClear
                onChange={(e) => setDraft({ ...draft, email: e.target.value })}
              />
              <Select
                allowClear
                placeholder="登录结果"
                style={{ width: 130 }}
                options={[
                  { label: '成功', value: '1' },
                  { label: '失败', value: '0' },
                ]}
                onChange={(value) => setDraft({ ...draft, status: value ?? '' })}
              />
            </>
          ) : (
            <>
              <Input
                placeholder="管理员编号"
                allowClear
                onChange={(e) => setDraft({ ...draft, admin_user_id: e.target.value })}
              />
              <Select
                allowClear
                showSearch
                optionFilterProp="label"
                placeholder="操作类型"
                style={{ width: 200 }}
                options={Object.entries(auditActionNames).map(([value, label]) => ({
                  label,
                  value,
                }))}
                onChange={(value) => setDraft({ ...draft, action: value ?? '' })}
              />
              <Select
                allowClear
                placeholder="操作对象"
                style={{ width: 150 }}
                options={Object.entries(resourceNames).map(([value, label]) => ({ label, value }))}
                onChange={(value) => setDraft({ ...draft, resource_type: value ?? '' })}
              />
              <Input
                placeholder="对象编号或标识"
                allowClear
                onChange={(e) => setDraft({ ...draft, resource_id: e.target.value })}
              />
            </>
          )}
          <Input
            type="date"
            aria-label="开始日期"
            style={{ width: 150 }}
            onChange={(e) => setDraft({ ...draft, created_from: e.target.value })}
          />
          <span>至</span>
          <Input
            type="date"
            aria-label="结束日期"
            style={{ width: 150 }}
            onChange={(e) => setDraft({ ...draft, created_to: e.target.value })}
          />
          <Button type="primary" onClick={applyFilters}>
            查询
          </Button>
        </Space>
        <Table
          rowKey="id"
          loading={loading}
          dataSource={data?.items}
          columns={columns}
          pagination={pagination}
        />
      </Card>
    </>
  );
}
