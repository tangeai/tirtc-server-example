import { useState } from 'react';
import type { UploadProps } from 'antd';
import {
  Button,
  Card,
  Col,
  Form,
  Image,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  Upload,
  message,
} from 'antd';
import {
  CopyOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import { formatTime, pageTitle, useLoad } from '../../shared/admin-ui';

type Board = {
  id: string;
  vendor: string;
  name: string;
  model: string;
  chip: string;
  summary: string;
  capabilities: string[];
  adaptation_status: 'ready' | 'adapting' | 'planned';
  image_url: string;
  purchase_url?: string;
  repository_url?: string;
  firmware_url?: string;
  flashing_guide_url?: string;
  effect_video_url?: string;
  detail_slug: string;
  sort_order: number;
  publish_status: 'draft' | 'published' | 'offline';
  revision: number;
  created_at?: string;
  updated_at?: string;
};
type CatalogResponse = { items: Board[] };
type BoardForm = Omit<Board, 'capabilities'> & { capabilities?: string };

const adaptationNames = { ready: '已适配', adapting: '适配中', planned: '计划中' };
const publishNames = { draft: '草稿', published: '已上架', offline: '已下架' };
const httpsRule = {
  validator: (_: unknown, value?: string) =>
    !value || /^https:\/\/[^\s]+$/i.test(value)
      ? Promise.resolve()
      : Promise.reject(new Error('请输入 https:// 地址')),
};
const imageRule = {
  validator: (_: unknown, value?: string) =>
    !value ||
    /^https:\/\/[^\s]+$/i.test(value) ||
    /^\/v1\/board-images\/[a-f0-9]{64}\.(jpg|png|webp)$/.test(value)
      ? Promise.resolve()
      : Promise.reject(new Error('请上传图片或输入 https:// 地址')),
};
const slug = (value: string) => {
  const normalized = value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
  return normalized || `board-${crypto.randomUUID().slice(0, 8)}`;
};

export function BoardsPage() {
  const [data, loading, reload] = useLoad(() => api<CatalogResponse>('/boards'), []);
  const [editing, setEditing] = useState<Board>();
  const [form] = Form.useForm<BoardForm>();
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);
  const watchedImageURL = Form.useWatch('image_url', form);
  const boards = data?.items || [];
  const uploadImage: UploadProps['beforeUpload'] = (file) => {
    if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) {
      message.error('请选择 JPG、PNG 或 WebP 图片');
      return Upload.LIST_IGNORE;
    }
    if (file.size > 5 * 1024 * 1024) {
      message.error('图片不能超过 5 MB');
      return Upload.LIST_IGNORE;
    }
    void (async () => {
      setUploading(true);
      try {
        const body = new FormData();
        body.append('file', file);
        const uploaded = await api<{ url: string }>('/boards/images', { method: 'POST', body });
        form.setFieldValue('image_url', uploaded.url);
        await form.validateFields(['image_url']);
        message.success('图片已上传');
      } catch (error) {
        reportError(error);
      } finally {
        setUploading(false);
      }
    })();
    return Upload.LIST_IGNORE;
  };

  const mutate = async (
    path: string,
    method: 'POST' | 'PUT' | 'DELETE',
    body: unknown,
    success: string,
  ) => {
    setSaving(true);
    try {
      await api(path, json(method, body));
      message.success(success);
      setEditing(undefined);
      reload();
    } catch (error) {
      reportError(error);
    } finally {
      setSaving(false);
    }
  };
  const openEditor = (board?: Board) => {
    const draft =
      board ||
      ({
        id: '',
        adaptation_status: 'planned',
        publish_status: 'draft',
        revision: 0,
        sort_order: boards.length ? Math.max(...boards.map((item) => item.sort_order)) + 10 : 10,
        image_url: '',
      } as Board);
    setEditing(draft);
    form.setFieldsValue({ ...draft, capabilities: draft.capabilities?.join('、') || '' });
  };
  const saveEditor = async (values: BoardForm) => {
    if (!editing) return;
    const now = new Date().toISOString();
    const board: Board = {
      ...editing,
      ...values,
      id: editing.id,
      capabilities: (values.capabilities || '')
        .split(/[、,，]/)
        .map((x) => x.trim())
        .filter(Boolean),
      detail_slug: values.detail_slug?.trim() || slug(values.model),
      created_at: editing.created_at || now,
      updated_at: now,
    };
    const exists = Boolean(editing.id);
    await mutate(
      exists ? `/boards/${editing.id}` : '/boards',
      exists ? 'PUT' : 'POST',
      {
        board,
        expected_revision: editing.revision,
        reason: `${exists ? '编辑' : '新增'}开发板：${board.model}`,
      },
      exists ? '开发板资料已保存' : '开发板已创建为草稿',
    );
  };
  const updateStatus = (board: Board, status: Board['publish_status']) =>
    mutate(
      `/boards/${board.id}/status`,
      'PUT',
      {
        status,
        expected_revision: board.revision,
        reason: `${status === 'published' ? '上架' : '下架'}开发板：${board.model}`,
      },
      status === 'published' ? '开发板已上架' : '开发板已下架',
    );
  const copy = (board: Board) => {
    const suffix = Date.now().toString().slice(-6);
    const duplicate: Board = {
      ...board,
      id: '',
      name: `${board.name} 副本`,
      model: `${board.model}-COPY-${suffix}`,
      detail_slug: `${slug(board.detail_slug)}-copy-${suffix}`,
      publish_status: 'draft',
      revision: 0,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    return mutate(
      '/boards',
      'POST',
      { board: duplicate, reason: `复制开发板：${board.model}` },
      '开发板副本已创建为草稿',
    );
  };

  return (
    <>
      {pageTitle(
        '开发板管理',
        '配置官网开发板目录、详情及固件等相关资源',
        <Space>
          <Button icon={<ReloadOutlined />} onClick={reload}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => openEditor()}>
            新增开发板
          </Button>
        </Space>,
      )}
      <Card>
        <Space wrap style={{ marginBottom: 16 }}>
          <Tag color="green">
            已上架 {boards.filter((x) => x.publish_status === 'published').length}
          </Tag>
          <Tag>草稿 {boards.filter((x) => x.publish_status === 'draft').length}</Tag>
          <Typography.Text type="secondary">按展示顺序排列，前 20 款显示图片卡片。</Typography.Text>
        </Space>
        <Table
          rowKey="id"
          loading={loading || saving}
          dataSource={[...boards].sort((a, b) => a.sort_order - b.sort_order)}
          scroll={{ x: 1050 }}
          pagination={false}
          columns={[
            {
              title: '名称',
              dataIndex: 'name',
              width: 190,
              render: (value, row) => (
                <>
                  <b>{value}</b>
                  <br />
                  <Typography.Text type="secondary">{row.vendor}</Typography.Text>
                </>
              ),
            },
            { title: '完整型号', dataIndex: 'model', width: 190 },
            { title: '芯片', dataIndex: 'chip', width: 120 },
            {
              title: '适配状态',
              dataIndex: 'adaptation_status',
              width: 100,
              render: (value) => (
                <Tag
                  color={value === 'ready' ? 'green' : value === 'adapting' ? 'blue' : 'default'}
                >
                  {adaptationNames[value as keyof typeof adaptationNames]}
                </Tag>
              ),
            },
            { title: '顺序', dataIndex: 'sort_order', width: 80 },
            {
              title: '上架状态',
              dataIndex: 'publish_status',
              width: 100,
              render: (value) => (
                <Tag color={value === 'published' ? 'green' : 'default'}>
                  {publishNames[value as keyof typeof publishNames]}
                </Tag>
              ),
            },
            { title: '更新时间', dataIndex: 'updated_at', width: 170, render: formatTime },
            {
              title: '操作',
              fixed: 'right',
              width: 260,
              render: (_, row) => (
                <Space size="small">
                  <Button size="small" icon={<EditOutlined />} onClick={() => openEditor(row)}>
                    编辑
                  </Button>
                  <Button size="small" icon={<CopyOutlined />} onClick={() => copy(row)}>
                    复制
                  </Button>
                  {row.publish_status === 'published' ? (
                    <Button size="small" onClick={() => updateStatus(row, 'offline')}>
                      下架
                    </Button>
                  ) : (
                    <Button
                      size="small"
                      type="primary"
                      onClick={() => updateStatus(row, 'published')}
                    >
                      上架
                    </Button>
                  )}
                  <Popconfirm
                    title="删除后无法恢复，确定删除？"
                    onConfirm={() =>
                      mutate(
                        `/boards/${row.id}`,
                        'DELETE',
                        { expected_revision: row.revision, reason: `删除开发板：${row.model}` },
                        '开发板已删除',
                      )
                    }
                  >
                    <Button size="small" danger>
                      删除
                    </Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        width={860}
        open={!!editing}
        title={editing?.id ? '编辑开发板' : '新增开发板'}
        footer={null}
        destroyOnClose
        onCancel={() => setEditing(undefined)}
      >
        <Form form={form} layout="vertical" onFinish={saveEditor}>
          <Row gutter={16}>
            <Col xs={24} md={12}>
              <Form.Item name="name" label="开发板名称" rules={[{ required: true }]}>
                <Input maxLength={80} />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item name="model" label="完整型号" rules={[{ required: true }]}>
                <Input maxLength={120} />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item name="vendor" label="厂商" rules={[{ required: true }]}>
                <Input maxLength={80} />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item name="chip" label="芯片平台" rules={[{ required: true }]}>
                <Input placeholder="例如 ESP32-S3" maxLength={80} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="summary"
                label="简单介绍"
                rules={[{ required: true }]}
                extra="建议 20–80 个字符，最多 160 个字符"
              >
                <Input.TextArea rows={3} maxLength={160} showCount />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item
                name="capabilities"
                label="能力标签"
                extra="使用顿号或逗号分隔，最多 10 个"
              >
                <Input placeholder="实时音视频、触摸屏、AI 对话" />
              </Form.Item>
            </Col>
            <Col xs={24} md={6}>
              <Form.Item name="adaptation_status" label="适配状态" rules={[{ required: true }]}>
                <Select
                  options={[
                    { value: 'ready', label: '已适配' },
                    { value: 'adapting', label: '适配中' },
                    { value: 'planned', label: '计划中' },
                  ]}
                />
              </Form.Item>
            </Col>
            <Col xs={24} md={6}>
              <Form.Item name="sort_order" label="展示顺序" rules={[{ required: true }]}>
                <InputNumber min={0} precision={0} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="image_url"
                label="产品图片"
                rules={[{ required: true, message: '请上传产品图片或输入图片地址' }, imageRule]}
                extra={
                  <Space style={{ marginTop: 8 }}>
                    <Upload
                      accept="image/jpeg,image/png,image/webp"
                      showUploadList={false}
                      beforeUpload={uploadImage}
                    >
                      <Button icon={<UploadOutlined />} loading={uploading}>
                        上传图片
                      </Button>
                    </Upload>
                    <Typography.Text type="secondary">
                      JPG、PNG 或 WebP，不超过 5 MB
                    </Typography.Text>
                  </Space>
                }
              >
                <Input placeholder="上传图片，或输入 https:// 图片地址" />
              </Form.Item>
            </Col>
            {watchedImageURL && (
              <Col span={24}>
                <Image
                  src={watchedImageURL}
                  width={180}
                  height={120}
                  style={{ objectFit: 'contain' }}
                  fallback="data:image/gif;base64,R0lGODlhAQABAAD/ACwAAAAAAQABAAACADs="
                />
              </Col>
            )}
            <Col xs={24} md={12}>
              <Form.Item name="purchase_url" label="购买链接" rules={[httpsRule]}>
                <Input placeholder="https://..." />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item name="repository_url" label="仓库链接" rules={[httpsRule]}>
                <Input placeholder="https://..." />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item name="firmware_url" label="固件链接" rules={[httpsRule]}>
                <Input placeholder="https://..." />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item name="flashing_guide_url" label="烧录指南" rules={[httpsRule]}>
                <Input placeholder="https://..." />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item name="effect_video_url" label="效果视频链接" rules={[httpsRule]}>
                <Input placeholder="https://..." />
              </Form.Item>
            </Col>
            <Col xs={24} md={12}>
              <Form.Item
                name="detail_slug"
                label="详情地址标识"
                extra="留空时根据完整型号生成"
                rules={[
                  {
                    pattern: /^[a-z0-9]+(?:-[a-z0-9]+)*$/,
                    message: '只允许小写字母、数字和连字符',
                  },
                ]}
              >
                <Input maxLength={80} placeholder="esp32-s3-board" />
              </Form.Item>
            </Col>
          </Row>
          <Space>
            <Button type="primary" htmlType="submit" loading={saving}>
              保存
            </Button>
            <Button onClick={() => setEditing(undefined)}>取消</Button>
          </Space>
        </Form>
      </Modal>
    </>
  );
}
