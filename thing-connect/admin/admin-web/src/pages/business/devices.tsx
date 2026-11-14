import { QueryFilter, ProFormText, ProFormSelect } from '@ant-design/pro-components';
import { IPAddress, type IPLocation } from '../../shared/ip-address';
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
  Modal,
  Popconfirm,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  Upload,
  App as AntApp,
  type UploadFile,
} from 'antd';
import {
  DownloadOutlined,
  UploadOutlined,
  PlusOutlined,
  CloseOutlined,
  EyeOutlined,
  DisconnectOutlined,
} from '@ant-design/icons';
import { api, json } from '../../api';
import { DeviceReportedProfiles, type ReportedProfiles } from './device-profile';
import { reportError } from '../../error-feedback';
import {
  useCompactQueryLayout,
  queryActions,
  confirmationActions,
  RefreshButton,
  formatTime,
  pageTitle as title,
  useLoad,
  type PageData,
} from '../../shared/admin-ui';
import {
  assignmentName,
  devicePresence,
  downloadDeviceImportTemplate,
} from '../../shared/admin-metadata';

type ManagedDevice = {
  id: number;
  device_id: string;
  mac: string;
  assign: string;
  device_name: string;
  user_id: number;
  user_email: string;
  active_time?: string;
  bind_time?: string;
  online: boolean;
  presence_known: boolean;
  last_seen_at?: string;
  last_online_ip?: string;
  last_online_ip_location?: IPLocation;
  last_online_at?: string;
  hardware?: {
    chip_model?: string;
    board_model?: string;
  };
  firmware_version?: string;
  profiles?: ReportedProfiles;
};

type BindLog = {
  id: number;
  user_id: number;
  action: number;
  mac: string;
  assign: string;
  created_at: string;
};

type PoolDevice = {
  id: number;
  device_id: string;
  status: number;
  ever_bound: boolean;
  current_user_id: number;
  current_user_email: string;
  assign: string;
  import_job_id: number;
  import_source_name: string;
  created_at: string;
  updated_at: string;
};

export function DevicesPage() {
  const { message } = AntApp.useApp();
  const [importOpen, setImportOpen] = useState(false);
  const [importFile, setImportFile] = useState<UploadFile>();
  const [importing, setImporting] = useState(false);

  const closeImport = () => {
    setImportOpen(false);
    setImportFile(undefined);
  };
  const importDevices = async ({ reason }: { reason: string }) => {
    const file = importFile?.originFileObj;
    if (!file) {
      reportError('请先选择 CSV 文件');
      return;
    }
    if (file.size > 10 * 1024 * 1024) {
      reportError('CSV 文件不能超过 10 MB');
      return;
    }
    if (importing) return;
    setImporting(true);
    try {
      const content = await file.text();
      if (content.includes('\ufffd')) {
        reportError('CSV 文件必须使用 UTF-8 编码');
        return;
      }
      const lines = content.split(/\r?\n/);
      const header = (lines[0] || '')
        .replace(/^\ufeff/, '')
        .split(',')
        .map((value) => value.trim().toLowerCase());
      if (header[0] !== 'device_id' || header[1] !== 'device_key') {
        reportError('CSV 前两列表头必须是 device_id,device_key');
        return;
      }
      if (lines.length < 2 || !lines.slice(1).some((line) => line.trim())) {
        reportError('CSV 至少需要一行设备数据');
        return;
      }
      if (lines.length > 100001) {
        reportError('CSV 数据不能超过 10 万行');
        return;
      }
      const form = new FormData();
      form.append('file', file);
      form.append('reason', reason);
      await api('/device-pool/imports', { method: 'POST', body: form });
      message.success('导入任务已创建，可在任务中心查看进度');
      closeImport();
    } catch (error) {
      reportError(error);
    } finally {
      setImporting(false);
    }
  };

  return (
    <>
      {title(
        '设备管理',
        '分别查看用户设备和设备池，追踪在线状态、归属及导入来源',
        <Button type="primary" icon={<UploadOutlined />} onClick={() => setImportOpen(true)}>
          导入设备池
        </Button>,
      )}
      <Card>
        <Tabs
          destroyOnHidden
          items={[
            { key: 'devices', label: '用户设备', children: <UserDevicesPanel /> },
            { key: 'pool', label: '设备池', children: <DevicePoolPanel /> },
          ]}
        />
      </Card>
      <Modal
        width={720}
        open={importOpen}
        title="导入设备池"
        footer={null}
        destroyOnHidden
        onCancel={closeImport}
      >
        <Alert
          className="form-alert"
          type="info"
          showIcon
          message="CSV 文件格式"
          description={
            <div>
              <p>
                文件必须使用 UTF-8 编码，前两列表头固定为 <code>device_id,device_key</code>。
              </p>
              <pre>device_id,device_key{`\n`}TC-DEVICE-000001,replace-with-device-secret</pre>
              <p>
                设备 ID 和设备密钥均为必填，最长 64 个字符；每个设备 ID 只能导入一次。单个文件不超过
                10 MB、10 万行。设备密钥属于敏感信息，请勿使用示例值。
              </p>
              <Button icon={<DownloadOutlined />} onClick={downloadDeviceImportTemplate}>
                下载 CSV 模板
              </Button>
            </div>
          }
        />
        <Form name="devices-1" layout="vertical" onFinish={importDevices}>
          <Form.Item label="选择 CSV 文件" required>
            <Upload.Dragger
              accept=".csv,text/csv"
              maxCount={1}
              beforeUpload={(file) => {
                setImportFile({
                  uid: file.uid,
                  name: file.name,
                  status: 'done',
                  originFileObj: file,
                });
                return false;
              }}
              onRemove={() => {
                setImportFile(undefined);
                return true;
              }}
              fileList={importFile ? [importFile] : []}
            >
              <p className="ant-upload-drag-icon">
                <UploadOutlined />
              </p>
              <p>点击或拖拽 CSV 文件到这里</p>
            </Upload.Dragger>
          </Form.Item>
          <Form.Item
            name="reason"
            label="导入原因"
            rules={[{ required: true, message: '请填写导入原因' }]}
          >
            <Input placeholder="例如：导入生产批次设备" />
          </Form.Item>
          <Space wrap>
            <Button icon={<PlusOutlined />} type="primary" htmlType="submit" loading={importing}>
              创建导入任务
            </Button>
            <Button icon={<CloseOutlined />} onClick={closeImport}>
              取消
            </Button>
          </Space>
        </Form>
      </Modal>
    </>
  );
}

function UserDevicesPanel() {
  const queryLayout = useCompactQueryLayout(['wide', 'status']);
  const { message } = AntApp.useApp();
  const screens = Grid.useBreakpoint();
  const [query, setQuery] = useState('');
  const [bound, setBound] = useState('');
  const [sortBy, setSortBy] = useState('');
  const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('desc');
  const [page, setPage] = useState(1);
  const [queryVersion, setQueryVersion] = useState(0);
  const [detail, setDetail] = useState<{ device: ManagedDevice; logs: BindLog[] }>();
  const pageSize = 20;
  const [data, loading, reload] = useLoad(
    () =>
      api<PageData<ManagedDevice>>(
        `/devices?page=${page}&page_size=${pageSize}&keyword=${encodeURIComponent(query)}&bound=${bound}&sort_by=${sortBy}&sort_order=${sortOrder}`,
      ),
    [query, bound, sortBy, sortOrder, page, queryVersion],
  );
  const unbind = async (row: ManagedDevice) => {
    try {
      await api(
        `/devices/${encodeURIComponent(row.device_id)}/force-unbind`,
        json('POST', {
          expected_user_id: row.user_id,
          reason: '后台强制解绑',
          confirm: true,
        }),
      );
      message.success('已解绑并创建清理任务');
      reload();
    } catch (error) {
      reportError(error);
    }
  };
  const openDetail = async (row: ManagedDevice) => {
    try {
      const id = encodeURIComponent(row.device_id);
      const [device, logs] = await Promise.all([
        api<ManagedDevice>(`/devices/${id}`),
        api<PageData<BindLog>>(`/devices/${id}/bind-logs?page=1&page_size=100`),
      ]);
      setDetail({ device, logs: logs.items });
    } catch (error) {
      reportError(error);
    }
  };
  return (
    <>
      <div className="panel-actions">
        <Typography.Text strong>用户设备</Typography.Text>
        <RefreshButton loading={loading} onClick={reload} />
      </div>
      <QueryFilter
        {...queryLayout}
        submitter={queryActions(loading)}
        name="devices-query-1"
        onFinish={async (values) => {
          setQueryVersion((value) => value + 1);
          setPage(1);
          setQuery(values.keyword?.trim() || '');
          setBound(values.bound || '');
        }}
        onReset={() => {
          setQueryVersion((value) => value + 1);
          setPage(1);
          setQuery('');
          setBound('');
        }}
      >
        <ProFormText name="keyword" label="设备 / 用户" placeholder="设备 ID、MAC 或用户邮箱" />
        <ProFormSelect
          width={112}
          name="bound"
          label="绑定状态"
          options={[
            { label: '已绑定', value: 'true' },
            { label: '未绑定', value: 'false' },
          ]}
        />
      </QueryFilter>
      <Table<ManagedDevice>
        rowKey="id"
        loading={loading}
        dataSource={data?.items}
        onChange={(_, __, sorter, extra) => {
          if (extra.action !== 'sort') return;
          const selected = Array.isArray(sorter) ? sorter[0] : sorter;
          setPage(1);
          if (
            selected?.order &&
            (selected.field === 'active_time' || selected.field === 'bind_time')
          ) {
            setSortBy(String(selected.field));
            setSortOrder(selected.order === 'ascend' ? 'asc' : 'desc');
          } else {
            setSortBy('');
            setSortOrder('desc');
          }
        }}
        pagination={{
          current: page,
          pageSize,
          total: data?.total || 0,
          showSizeChanger: false,
          showTotal: (total) => `共 ${total} 条`,
          onChange: setPage,
        }}
        scroll={{ x: 1690 }}
        columns={[
          {
            title: '设备',
            render: (_, row) => (
              <>
                <b>{row.device_id}</b>
                <br />
                {row.mac || '—'}
              </>
            ),
          },
          {
            title: '在线状态',
            render: (_, row) => (
              <>
                {devicePresence(row)}
                <br />
                <Typography.Text type="secondary">
                  {row.online
                    ? row.last_seen_at
                      ? `最近心跳 ${formatTime(row.last_seen_at)}`
                      : '当前在线'
                    : row.presence_known
                      ? '当前无在线心跳'
                      : '无法读取在线状态'}
                </Typography.Text>
              </>
            ),
          },
          {
            title: '最近上线 IP / 归属地',
            width: 240,
            render: (_, row) => (
              <IPAddress compact ip={row.last_online_ip} location={row.last_online_ip_location} />
            ),
          },
          {
            title: '所属用户',
            width: 220,
            dataIndex: 'user_email',
            ellipsis: true,
            render: (value) => value || '未绑定',
          },
          { title: '设备名称', dataIndex: 'device_name', render: (value) => value || '—' },
          { title: '设备来源', dataIndex: 'assign', render: assignmentName },
          {
            title: '首次活跃',
            dataIndex: 'active_time',
            render: formatTime,
            sorter: true,
            sortOrder:
              sortBy === 'active_time' ? (sortOrder === 'asc' ? 'ascend' : 'descend') : null,
            sortDirections: ['descend', 'ascend'],
          },
          {
            title: '绑定时间',
            dataIndex: 'bind_time',
            render: formatTime,
            sorter: true,
            sortOrder: sortBy === 'bind_time' ? (sortOrder === 'asc' ? 'ascend' : 'descend') : null,
            sortDirections: ['descend', 'ascend'],
          },
          {
            title: '操作',
            fixed: screens.md ? 'right' : undefined,
            width: 210,
            render: (_, row) => (
              <Space wrap size={0}>
                <Button icon={<EyeOutlined />} type="link" onClick={() => openDetail(row)}>
                  详情与上报属性
                </Button>
                {row.user_id > 0 ? (
                  <Popconfirm
                    {...confirmationActions}
                    title="解绑会返还额度，并创建 AI、VoIP 和呼叫数据清理任务"
                    onConfirm={() => unbind(row)}
                  >
                    <Button icon={<DisconnectOutlined />} danger type="link">
                      强制解绑
                    </Button>
                  </Popconfirm>
                ) : null}
              </Space>
            ),
          },
        ]}
      />
      <Drawer
        width="min(800px, 100vw)"
        open={!!detail}
        title={`设备详情：${detail?.device.device_id || ''}`}
        onClose={() => setDetail(undefined)}
      >
        {detail && (
          <>
            <Descriptions
              bordered
              column={{ xs: 1, sm: 2 }}
              items={[
                { key: 'name', label: '设备名称', children: detail.device.device_name || '—' },
                { key: 'presence', label: '在线状态', children: devicePresence(detail.device) },
                {
                  key: 'seen',
                  label: '最近心跳',
                  children: formatTime(detail.device.last_seen_at),
                },
                {
                  key: 'last_online_ip',
                  label: '最近上线 IP',
                  children: (
                    <IPAddress
                      ip={detail.device.last_online_ip}
                      location={detail.device.last_online_ip_location}
                    />
                  ),
                },
                {
                  key: 'last_online_at',
                  label: '最近上线时间',
                  children: formatTime(detail.device.last_online_at),
                },
                { key: 'mac', label: 'MAC 地址', children: detail.device.mac || '—' },
                {
                  key: 'chip_model',
                  label: '芯片型号',
                  children: detail.device.hardware?.chip_model || '未上报',
                },
                {
                  key: 'board_model',
                  label: '开发板型号',
                  children: detail.device.hardware?.board_model || '未上报',
                },
                {
                  key: 'firmware_version',
                  label: '固件版本',
                  children: detail.device.firmware_version || '未上报',
                },
                {
                  key: 'owner',
                  label: '当前用户',
                  children: detail.device.user_email || '未绑定',
                },
                {
                  key: 'assign',
                  label: '设备来源',
                  children: assignmentName(detail.device.assign),
                },
                {
                  key: 'active',
                  label: '首次活跃',
                  children: formatTime(detail.device.active_time),
                },
                {
                  key: 'bind',
                  label: '绑定时间',
                  children: formatTime(detail.device.bind_time),
                },
              ]}
            />
            <Typography.Title level={5}>设备上报属性</Typography.Title>
            <DeviceReportedProfiles
              key={detail.device.device_id}
              profiles={detail.device.profiles}
            />
            <Typography.Title level={5}>绑定历史</Typography.Title>
            <Table<BindLog>
              scroll={{ x: 900 }}
              className="job-items"
              rowKey="id"
              size="small"
              pagination={false}
              dataSource={detail.logs}
              columns={[
                { title: '时间', dataIndex: 'created_at', render: formatTime },
                {
                  title: '操作',
                  dataIndex: 'action',
                  render: (value) =>
                    value === 1 ? <Tag color="success">绑定</Tag> : <Tag>解绑</Tag>,
                },
                { title: '用户编号', dataIndex: 'user_id', render: (value) => `#${value}` },
                { title: 'MAC 地址', dataIndex: 'mac', render: (value) => value || '—' },
                { title: '设备来源', dataIndex: 'assign', render: assignmentName },
              ]}
            />
          </>
        )}
      </Drawer>
    </>
  );
}

function DevicePoolPanel() {
  const queryLayout = useCompactQueryLayout(['text', 'status']);
  const [query, setQuery] = useState('');
  const [state, setState] = useState('');
  const [page, setPage] = useState(1);
  const [queryVersion, setQueryVersion] = useState(0);
  const pageSize = 20;
  const [data, loading, reload] = useLoad(
    () =>
      api<PageData<PoolDevice>>(
        `/device-pool?page=${page}&page_size=${pageSize}&keyword=${encodeURIComponent(query)}&state=${state}`,
      ),
    [query, state, page, queryVersion],
  );
  const lifecycle = (row: PoolDevice) => {
    if (row.status === 1) return <Tag color="processing">已分配</Tag>;
    if (row.ever_bound) return <Tag>已解绑保留</Tag>;
    return <Tag color="success">可分配</Tag>;
  };
  return (
    <>
      <Alert
        className="form-alert"
        type="info"
        showIcon
        message="设备池不会显示设备密钥"
        description="已解绑设备保留原设备身份，不会再次分配给其他用户。导入来源可跳转到任务中心核对逐行结果。"
      />
      <div className="panel-actions">
        <Typography.Text strong>设备池</Typography.Text>
        <RefreshButton loading={loading} onClick={reload} />
      </div>
      <QueryFilter
        {...queryLayout}
        submitter={queryActions(loading)}
        name="devices-query-2"
        onFinish={async (values) => {
          setQueryVersion((value) => value + 1);
          setPage(1);
          setQuery(values.keyword?.trim() || '');
          setState(values.state || '');
        }}
        onReset={() => {
          setQueryVersion((value) => value + 1);
          setPage(1);
          setQuery('');
          setState('');
        }}
      >
        <ProFormText name="keyword" label="设备 ID" placeholder="输入设备 ID" />
        <ProFormSelect
          width={112}
          name="state"
          label="设备池状态"
          options={[
            { label: '可分配', value: 'available' },
            { label: '已分配', value: 'allocated' },
            { label: '已解绑保留', value: 'released' },
          ]}
        />
      </QueryFilter>
      <Table<PoolDevice>
        rowKey="id"
        loading={loading}
        dataSource={data?.items}
        pagination={{
          current: page,
          pageSize,
          total: data?.total || 0,
          showSizeChanger: false,
          showTotal: (total) => `共 ${total} 条`,
          onChange: setPage,
        }}
        scroll={{ x: 1000 }}
        columns={[
          { title: '设备 ID', dataIndex: 'device_id' },
          { title: '状态', render: (_, row) => lifecycle(row) },
          {
            title: '当前归属',
            render: (_, row) =>
              row.current_user_email || (row.ever_bound ? '当前未绑定' : '从未绑定'),
          },
          { title: '分配方式', dataIndex: 'assign', render: assignmentName },
          {
            title: '导入来源',
            render: (_, row) =>
              row.import_job_id ? (
                <>
                  <Typography.Link href="/admin/jobs">任务 #{row.import_job_id}</Typography.Link>
                  <br />
                  <Typography.Text type="secondary">
                    {row.import_source_name || 'CSV 导入'}
                  </Typography.Text>
                </>
              ) : (
                '初始化或外部写入'
              ),
          },
          { title: '进入设备池', dataIndex: 'created_at', render: formatTime },
          { title: '最近状态变化', dataIndex: 'updated_at', render: formatTime },
        ]}
      />
    </>
  );
}
