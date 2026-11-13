import {
  QueryFilter,
  ProFormText,
  ProFormSelect,
  ProFormDateRangePicker,
} from '@ant-design/pro-components';
import { useState } from 'react';
import { Button, Card, Table, Tag } from 'antd';
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
  const [filters, setFilters] = useState<AnyRow>({});
  const query = Object.entries(filters)
    .filter(([, value]) => value !== '' && value !== undefined)
    .map(([key, value]) => `${key}=${encodeURIComponent(String(value))}`)
    .join('&');
  const [data, loading, reload] = useLoad(
    () => api<PageData>(`${path}?page=${page}&page_size=${pageSize}&${query}`),
    [path, page, query],
  );
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
        <QueryFilter
          name="logs-query-1"
          defaultCollapsed={false}
          onFinish={async ({ created, ...values }) => {
            setPage(1);
            setFilters({
              ...values,
              created_from: created?.[0] || '',
              created_to: created?.[1] || '',
            });
          }}
          onReset={() => {
            setPage(1);
            setFilters({});
          }}
        >
          {kind === 'login' && (
            <ProFormText name="email" label="管理员邮箱" placeholder="管理员邮箱" />
          )}
          {kind === 'login' && (
            <ProFormSelect
              name="status"
              label="登录结果"
              options={[
                { label: '成功', value: '1' },
                { label: '失败', value: '0' },
              ]}
            />
          )}
          {kind === 'audit' && <ProFormText name="admin_user_id" label="管理员编号" />}
          {kind === 'audit' && (
            <ProFormSelect
              name="action"
              label="操作类型"
              showSearch
              options={Object.entries(auditActionNames).map(([value, label]) => ({ value, label }))}
            />
          )}
          {kind === 'audit' && (
            <ProFormSelect
              name="resource_type"
              label="操作对象"
              options={Object.entries(resourceNames).map(([value, label]) => ({ value, label }))}
            />
          )}
          {kind === 'audit' && <ProFormText name="resource_id" label="对象编号" />}
          <ProFormDateRangePicker name="created" label="发生日期" />
        </QueryFilter>
        <Table
          scroll={{ x: 900 }}
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
