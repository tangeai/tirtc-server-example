import { Button, Card, Col, Row, Space, Statistic, Table } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { api } from '../api';
import {
  formatTime,
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
        '业务指标与五个服务的跨主机实例状态',
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
          <Col xs={12} md={6} xl={3} key={String(label)}>
            <Card loading={loading}>
              <Statistic title={label} value={value ?? 0} />
            </Card>
          </Col>
        ))}
      </Row>
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
              <div className="instances">
                {service.instances?.map((x: AnyRow) => (
                  <div key={x.instance_id}>
                    <b title="实例标识">{x.instance_id}</b>
                    <span>
                      {x.node}
                      {x.zone ? ` / ${x.zone}` : ''}
                    </span>
                    <span>
                      {x.version || '版本未知'} · 配置版本 r
                      {Math.max(0, ...Object.values(x.config_revision || {}).map(Number))}
                    </span>
                  </div>
                ))}
              </div>
            </Card>
          </Col>
        ))}
      </Row>
      <Card title="最近管理操作" className="recent-audits">
        <Table
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
