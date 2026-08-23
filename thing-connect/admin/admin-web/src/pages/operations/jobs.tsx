import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Popconfirm,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { api, download, json } from '../../api';
import { reportError } from '../../error-feedback';
import {
  formatTime,
  pageTitle as title,
  useLoad,
  type AnyRow,
  type PageData,
} from '../../shared/admin-ui';
import { jobTypeNames } from '../../shared/admin-metadata';

export function JobsPage() {
  const [data, loading, reload] = useLoad(
    () => api<{ jobs: PageData; related_queues: AnyRow }>('/jobs?page=1&page_size=50'),
    [],
  );
  const [detail, setDetail] = useState<AnyRow>();
  const open = async (row: AnyRow) => {
    try {
      setDetail(await api(`/jobs/${row.id}?page=1&page_size=100`));
    } catch (e) {
      reportError(e);
    }
  };
  const retry = async (row: AnyRow) => {
    try {
      await api(`/jobs/${row.id}/retry`, json('POST', { reason: '后台人工重试' }));
      message.success('任务已重新排队');
      reload();
    } catch (e) {
      reportError(e);
    }
  };
  const getResult = async (row: AnyRow) => {
    try {
      await download(`/jobs/${row.id}/result`, `device-import-${row.id}-result.csv`);
    } catch (e) {
      reportError(e);
    }
  };
  const jobStatus = (value: number) =>
    ['待执行', '执行中', '成功', '部分成功', '失败'][value] || '未知';
  return (
    <>
      {title(
        '任务中心',
        '设备池导入、解绑清理与配置发布队列',
        <Button icon={<ReloadOutlined />} onClick={reload}>
          刷新
        </Button>,
      )}
      {data && (
        <Alert
          className="form-alert"
          message={`解绑清理待处理 ${data.related_queues.cleanup_pending} 项，配置发布待处理 ${data.related_queues.config_publish_pending} 项`}
        />
      )}
      <Card>
        <Table
          rowKey="id"
          loading={loading}
          dataSource={data?.jobs.items}
          columns={[
            {
              title: '任务',
              render: (_, r) => (
                <>
                  <b>{jobTypeNames[r.job_type] || r.job_type}</b>
                  <br />
                  <Typography.Text type="secondary">
                    任务编号 #{r.id} · {r.source_name}
                  </Typography.Text>
                </>
              ),
            },
            {
              title: '状态',
              dataIndex: 'status',
              render: (v) => (
                <Tag color={v === 2 ? 'success' : v >= 3 ? 'warning' : 'processing'}>
                  {jobStatus(v)}
                </Tag>
              ),
            },
            {
              title: '处理进度',
              render: (_, r) =>
                `成功 ${r.succeeded_count}/${r.total_count}，失败 ${r.failed_count}`,
            },
            { title: '执行次数', dataIndex: 'attempts' },
            { title: '更新时间', dataIndex: 'updated_at', render: formatTime },
            {
              title: '操作',
              render: (_, r) => (
                <Space>
                  <Button type="link" onClick={() => open(r)}>
                    详情
                  </Button>
                  {r.result_available ? (
                    <Button type="link" onClick={() => getResult(r)}>
                      下载结果
                    </Button>
                  ) : null}
                  {r.status >= 3 ? (
                    <Popconfirm title="确认重新执行失败项目？" onConfirm={() => retry(r)}>
                      <Button type="link">重试</Button>
                    </Popconfirm>
                  ) : null}
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <Drawer
        width={760}
        open={!!detail}
        title={`任务详情 #${detail?.job?.id || ''}`}
        onClose={() => setDetail(undefined)}
      >
        {detail && (
          <>
            <Space className="toolbar">
              {detail.job.result_available ? (
                <Button onClick={() => getResult(detail.job)}>下载导入结果</Button>
              ) : null}
            </Space>
            <Descriptions
              bordered
              size="small"
              column={2}
              items={[
                {
                  key: 'type',
                  label: '任务类型',
                  children: jobTypeNames[detail.job.job_type] || detail.job.job_type,
                },
                { key: 'status', label: '状态', children: jobStatus(detail.job.status) },
                {
                  key: 'progress',
                  label: '处理进度',
                  children: `成功 ${detail.job.succeeded_count}/${detail.job.total_count}`,
                },
                { key: 'attempts', label: '执行次数', children: detail.job.attempts },
                { key: 'time', label: '更新时间', children: formatTime(detail.job.updated_at) },
                {
                  key: 'error',
                  label: '最后错误',
                  children: detail.job.last_error || '—',
                  span: 2,
                },
              ]}
            />
            <Table<AnyRow>
              className="job-items"
              rowKey="id"
              size="small"
              pagination={false}
              dataSource={detail.items}
              columns={[
                { title: 'CSV 行号', dataIndex: 'row_no' },
                { title: '设备 ID', dataIndex: 'resource_id' },
                {
                  title: '结果',
                  dataIndex: 'status',
                  render: (v) =>
                    v === 1 ? <Tag color="success">成功</Tag> : <Tag color="error">失败</Tag>,
                },
                { title: '失败原因', render: (_, r) => r.error_message || '—' },
              ]}
            />
          </>
        )}
      </Drawer>
    </>
  );
}
