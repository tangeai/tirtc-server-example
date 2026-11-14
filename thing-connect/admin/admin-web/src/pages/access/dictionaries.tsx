import { useState } from 'react';
import {
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
  App as AntApp,
} from 'antd';
import { PlusOutlined, EditOutlined, SaveOutlined } from '@ant-design/icons';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import {
  localPagination,
  RefreshButton,
  pageTitle as title,
  useLoad,
  type AnyRow,
} from '../../shared/admin-ui';

export function DictionariesPage() {
  const { message } = AntApp.useApp();
  const [saving, setSaving] = useState(false);
  const [types, loading, reload] = useLoad(() => api<{ items: AnyRow[] }>('/dict-types'), []);
  const [selected, setSelected] = useState<AnyRow>();
  const [typeEdit, setTypeEdit] = useState<AnyRow | null>(null);
  const [itemEdit, setItemEdit] = useState<AnyRow | null>(null);
  const [itemData, itemsLoading, reloadItems] = useLoad<{ items: AnyRow[] }>(
    () =>
      selected
        ? api(`/dict-types/${encodeURIComponent(selected.code)}/items`)
        : Promise.resolve({ items: [] }),
    [selected?.code],
  );
  const loadItems = (row: AnyRow) => setSelected(row);
  const saveType = async (v: AnyRow) => {
    if (saving) return;
    setSaving(true);
    try {
      const body = {
        code: v.code,
        name: v.name,
        status: v.enabled ? 1 : 0,
        remark: v.remark || '',
        reason: v.reason,
      };
      await api(
        typeEdit?.id ? `/dict-types/${typeEdit.id}` : '/dict-types',
        json(typeEdit?.id ? 'PUT' : 'POST', body),
      );
      message.success('字典类型已保存');
      setTypeEdit(null);
      reload();
    } catch (e) {
      reportError(e);
    } finally {
      setSaving(false);
    }
  };
  const saveItem = async (v: AnyRow) => {
    if (!selected) return;
    if (saving) return;
    setSaving(true);
    try {
      let extra = {};
      if (v.extra?.trim()) extra = JSON.parse(v.extra);
      const body = {
        label: v.label,
        value: v.value,
        sort_no: v.sort_no || 0,
        is_default: v.is_default ? 1 : 0,
        status: v.enabled ? 1 : 0,
        extra,
        remark: v.remark || '',
        reason: v.reason,
      };
      await api(
        itemEdit?.id ? `/dict-items/${itemEdit.id}` : `/dict-types/${selected.code}/items`,
        json(itemEdit?.id ? 'PUT' : 'POST', body),
      );
      message.success('字典项已保存');
      setItemEdit(null);
      reloadItems();
    } catch (e) {
      reportError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <>
      {title(
        '数据字典',
        '维护下拉选项和展示文案；密钥与业务开关应放在服务配置中',
        <Space wrap>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setTypeEdit({})}>
            新增字典类型
          </Button>
        </Space>,
      )}
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={9}>
          <Card title="字典类型" extra={<RefreshButton loading={loading} onClick={reload} />}>
            <Table
              pagination={localPagination}
              scroll={{ x: 400 }}
              rowKey="id"
              loading={loading}
              dataSource={types?.items}
              rowSelection={{
                type: 'radio',
                selectedRowKeys: selected ? [selected.id] : [],
                onChange: (_, rows) => loadItems(rows[0]),
              }}
              onRow={(r) => ({ onClick: () => loadItems(r) })}
              columns={[
                {
                  title: '字典',
                  render: (_, r) => (
                    <>
                      <b>{r.name}</b>
                      <br />
                      <Typography.Text type="secondary">
                        标识：<code>{r.code}</code>
                      </Typography.Text>
                    </>
                  ),
                },
                {
                  title: '状态',
                  dataIndex: 'status',
                  render: (v) => (v ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>),
                },
                {
                  title: '操作',
                  render: (_, r) => (
                    <Button
                      icon={<EditOutlined />}
                      type="link"
                      onClick={(e) => {
                        e.stopPropagation();
                        setTypeEdit(r);
                      }}
                    >
                      编辑
                    </Button>
                  ),
                },
              ]}
            />
          </Card>
        </Col>
        <Col xs={24} xl={15}>
          <Card
            title={selected ? `${selected.name} 字典项` : '请选择左侧字典'}
            extra={
              <Space wrap>
                <RefreshButton disabled={!selected} onClick={reloadItems} loading={itemsLoading} />
                <Button
                  icon={<PlusOutlined />}
                  type="primary"
                  disabled={!selected}
                  onClick={() => setItemEdit({})}
                >
                  新增字典项
                </Button>
              </Space>
            }
          >
            <Table
              pagination={localPagination}
              scroll={{ x: 900 }}
              rowKey="id"
              dataSource={itemData?.items}
              loading={itemsLoading}
              columns={[
                { title: '显示名称', dataIndex: 'label' },
                { title: '接口取值', dataIndex: 'value' },
                { title: '显示顺序', dataIndex: 'sort_no' },
                {
                  title: '默认',
                  dataIndex: 'is_default',
                  render: (v) => (v ? <Tag color="blue">默认</Tag> : '—'),
                },
                {
                  title: '状态',
                  dataIndex: 'status',
                  render: (v) => (v ? <Tag color="success">启用</Tag> : <Tag>停用</Tag>),
                },
                {
                  title: '操作',
                  render: (_, r) => (
                    <Button icon={<EditOutlined />} type="link" onClick={() => setItemEdit(r)}>
                      编辑
                    </Button>
                  ),
                },
              ]}
            />
          </Card>
        </Col>
      </Row>
      <Modal
        open={typeEdit !== null}
        title={typeEdit?.id ? '编辑字典类型' : '新增字典类型'}
        footer={null}
        destroyOnHidden
        onCancel={() => setTypeEdit(null)}
      >
        {typeEdit !== null && (
          <Form
            name="dictionaries-1"
            layout="vertical"
            onFinish={saveType}
            initialValues={{
              code: typeEdit.code,
              name: typeEdit.name,
              enabled: typeEdit.id ? typeEdit.status === 1 : true,
              remark: typeEdit.remark,
            }}
          >
            <Form.Item name="name" label="字典名称" rules={[{ required: true }]}>
              <Input placeholder="例如：设备状态" />
            </Form.Item>
            <Form.Item
              name="code"
              label="字典标识"
              rules={[{ required: true }]}
              extra="供接口和二次开发使用，创建后不可修改"
            >
              <Input disabled={!!typeEdit.id} placeholder="例如：device_status" />
            </Form.Item>
            <Form.Item name="enabled" label="启用字典" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="remark" label="用途说明">
              <Input />
            </Form.Item>
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input placeholder="说明新增或调整原因" />
            </Form.Item>
            <Button icon={<SaveOutlined />} type="primary" htmlType="submit" loading={saving}>
              保存
            </Button>
          </Form>
        )}
      </Modal>
      <Modal
        open={itemEdit !== null}
        title={itemEdit?.id ? '编辑字典项' : '新增字典项'}
        footer={null}
        destroyOnHidden
        onCancel={() => setItemEdit(null)}
      >
        {itemEdit !== null && (
          <Form
            name="dictionaries-2"
            layout="vertical"
            onFinish={saveItem}
            initialValues={{
              label: itemEdit.label,
              value: itemEdit.value,
              sort_no: itemEdit.sort_no || 0,
              is_default: itemEdit.is_default === 1,
              enabled: itemEdit.id ? itemEdit.status === 1 : true,
              extra: itemEdit.extra || '{}',
              remark: itemEdit.remark,
            }}
          >
            <Form.Item name="label" label="显示名称" rules={[{ required: true }]}>
              <Input placeholder="例如：在线" />
            </Form.Item>
            <Form.Item
              name="value"
              label="接口取值"
              rules={[{ required: true }]}
              extra="供接口和数据存储使用，创建后不可修改"
            >
              <Input disabled={!!itemEdit.id} placeholder="例如：online" />
            </Form.Item>
            <Form.Item name="sort_no" label="显示顺序">
              <InputNumber />
            </Form.Item>
            <Form.Item name="is_default" label="设为默认项" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="enabled" label="启用字典项" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item
              name="extra"
              label="扩展信息（JSON，可选）"
              extra="仅供需要附加结构化信息的二次开发场景使用"
            >
              <Input.TextArea className="code" rows={3} />
            </Form.Item>
            <Form.Item name="remark" label="备注">
              <Input />
            </Form.Item>
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input placeholder="说明新增或调整原因" />
            </Form.Item>
            <Button icon={<SaveOutlined />} type="primary" htmlType="submit" loading={saving}>
              保存
            </Button>
          </Form>
        )}
      </Modal>
    </>
  );
}
