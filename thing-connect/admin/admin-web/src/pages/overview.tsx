import {
  Button,
  Card,
  Col,
  Collapse,
  Descriptions,
  Empty,
  Row,
  Space,
  Statistic,
  Table,
} from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { api } from '../api';
import {
  formatTime,
  ServiceVersion,
  pageTitle as title,
  serviceStatusTag as statusTag,
  useLoad,
  type AnyRow,
} from '../shared/admin-ui';
import { auditActionName, resourceName, serviceName } from '../shared/admin-metadata';

export function OverviewPage() {
  const [data, loading, reload] = useLoad(
    () => api<{ services: AnyRow[]; metrics: AnyRow; recent_audits: AnyRow[] }>('/services/status'),
    [],
  );
  const metrics = data?.metrics || {};
  return (
    <>
      {title(
        '数据概览',
        '业务指标与各服务的跨主机实例状态',
        <Button icon={<ReloadOutlined />} onClick={reload}>
          刷新
        </Button>,
      )}
      <Row gutter={[16, 16]} className="form-alert">
        {[
          ['用户总数', metrics.user_count],
          ['已绑定设备', metrics.bound_devices],
          ['可用设备池', metrics.available_pool],
          ['24 小时绑定', metrics.binds_24h],
          ['24 小时解绑', metrics.unbinds_24h],
          ['待清理任务', metrics.cleanup_pending],
          ['异常任务', metrics.failed_admin_jobs],
        ].map(([label, value]) => (
          <Col xs={12} md={6} xl={6} key={String(label)}>
            <Card loading={loading}>
              <Statistic title={label} value={value ?? '—'} />
            </Card>
          </Col>
        ))}
      </Row>
      {!loading && !data && <Empty description="概览数据暂不可用，请刷新重试" />}
      <Row gutter={[16, 16]}>
        {(data?.services || []).map((service) => (
          <Col xs={24} md={12} xl={8} key={service.service}>
            <Card
              loading={loading}
              title={serviceName(service.service)}
              extra={statusTag(service.status)}
            >
              <Space size="large">
                <Statistic title="实例数" value={service.instance_count} />
                <Statistic title="健康实例" value={service.healthy_count} />
              </Space>
              <Collapse
                ghost
                items={(service.instances || []).map((x: AnyRow) => ({
                  key: x.instance_id,
                  label: x.instance_id,
                  children: (
                    <Descriptions
                      size="small"
                      column={1}
                      items={[
                        {
                          key: 'node',
                          label: '节点 / 区域',
                          children: [x.node, x.zone].filter(Boolean).join(' / ') || '—',
                        },
                        {
                          key: 'version',
                          label: '程序版本',
                          children: <ServiceVersion version={x.version} commit={x.commit} />,
                        },
                        {
                          key: 'revision',
                          label: '各项配置版本',
                          children:
                            Object.entries(x.config_revision || {})
                              .map(([key, value]) => `${key}: r${value}`)
                              .join('；') || '未上报',
                        },
                      ]}
                    />
                  ),
                }))}
              />
            </Card>
          </Col>
        ))}
      </Row>
      <Card title="最近管理操作" className="recent-audits">
        <Table
          scroll={{ x: 900 }}
          rowKey="id"
          size="small"
          pagination={false}
          dataSource={data?.recent_audits}
          columns={[
            { title: '时间', dataIndex: 'created_at', render: formatTime },
            { title: '管理员', render: (_, r) => r.email || `#${r.admin_user_id || '—'}` },
            { title: '操作', dataIndex: 'action', render: (v: string) => auditActionName(v) },
            {
              title: '对象',
              render: (_, r) => `${resourceName(r.resource_type)} ${r.resource_id}`,
            },
          ]}
        />
      </Card>
    </>
  );
}
