import { useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import {
  StepUpFields,
  pageTitle as title,
  useLoad,
  type AnyRow,
  type PermissionDefinition,
} from '../../shared/admin-ui';
import { iconOptions, nameWithCode, routeNames } from '../../shared/admin-metadata';

export function RolesPage() {
  const [roles, loading, reload] = useLoad(
    () =>
      api<{
        items: AnyRow[];
        registered_permissions: string[];
        permission_definitions: PermissionDefinition[];
      }>('/roles'),
    [],
  );
  const [menus, , reloadMenus] = useLoad(() => api<{ items: AnyRow[] }>('/menus'), []);
  const [roleEdit, setRoleEdit] = useState<AnyRow | null>(null);
  const [permissionEdit, setPermissionEdit] = useState<AnyRow | null>(null);
  const [menuGrant, setMenuGrant] = useState<AnyRow | null>(null);
  const [menuEdit, setMenuEdit] = useState<AnyRow | null>(null);
  const [grantedMenuIDs, setGrantedMenuIDs] = useState<number[]>([]);
  const saveRole = async (v: AnyRow) => {
    try {
      const body = {
        code: v.code,
        name: v.name,
        parent_id: v.parent_id || 0,
        sort_no: v.sort_no || 0,
        status: v.enabled ? 1 : 0,
        remark: v.remark || '',
        reason: v.reason,
        current_mfa_code: v.current_mfa_code || '',
        current_recovery_code: v.current_recovery_code || '',
      };
      await api(
        roleEdit?.id ? `/roles/${roleEdit.id}` : '/roles',
        json(roleEdit?.id ? 'PUT' : 'POST', body),
      );
      message.success('角色已保存');
      setRoleEdit(null);
      reload();
    } catch (e) {
      reportError(e);
    }
  };
  const savePermissions = async (v: AnyRow) => {
    try {
      await api(
        `/roles/${permissionEdit!.id}/permissions`,
        json('PUT', {
          permissions: v.permissions || [],
          reason: v.reason,
          current_mfa_code: v.current_mfa_code,
          current_recovery_code: v.current_recovery_code,
        }),
      );
      message.success('权限已更新');
      setPermissionEdit(null);
      reload();
    } catch (e) {
      reportError(e);
    }
  };
  const openMenus = async (row: AnyRow) => {
    try {
      const result = await api<{ menu_ids: number[] }>(`/roles/${row.id}/menus`);
      setGrantedMenuIDs(result.menu_ids);
      setMenuGrant(row);
    } catch (e) {
      reportError(e);
    }
  };
  const saveMenus = async (v: AnyRow) => {
    try {
      await api(
        `/roles/${menuGrant!.id}/menus`,
        json('PUT', { menu_ids: v.menu_ids || [], reason: v.reason }),
      );
      message.success('菜单授权已更新');
      setMenuGrant(null);
    } catch (e) {
      reportError(e);
    }
  };
  const saveMenu = async (v: AnyRow) => {
    try {
      const body = {
        parent_id: v.parent_id || 0,
        menu_code: v.menu_code,
        name: v.name,
        icon: v.icon || '',
        path: v.menu_type === 1 ? '' : v.path || '',
        permission_code: v.permission_code || '',
        menu_type: v.menu_type,
        sort_no: v.sort_no || 0,
        visible: v.visible ? 1 : 0,
        status: v.enabled ? 1 : 0,
        reason: v.reason,
      };
      await api(
        menuEdit?.id ? `/menus/${menuEdit.id}` : '/menus',
        json(menuEdit?.id ? 'PUT' : 'POST', body),
      );
      message.success('菜单已保存');
      setMenuEdit(null);
      reloadMenus();
    } catch (e) {
      reportError(e);
    }
  };
  const permissionDefinitions = roles?.permission_definitions || [];
  const permissionName = (code: string) =>
    permissionDefinitions.find((item) => item.code === code)?.name || code;
  const permissionOptions = Array.from(
    new Set(permissionDefinitions.map((item) => item.group)),
  ).map((group) => ({
    label: group,
    options: permissionDefinitions
      .filter((item) => item.group === group)
      .map((item) => ({
        label: `${item.name}（${item.code}）`,
        title: item.description,
        value: item.code,
      })),
  }));
  const roleNameByID = (id: number) =>
    id ? roles?.items.find((role) => role.id === id)?.name || `角色 #${id}` : '无';
  const routeOptions = Object.entries(routeNames).map(([value, name]) => ({
    label: `${name}（${value}）`,
    value,
  }));
  return (
    <>
      {title(
        '权限与菜单',
        '按业务模块配置角色权限、继承关系与可见菜单',
        <Space>
          <Button
            icon={<ReloadOutlined />}
            onClick={() => {
              reload();
              reloadMenus();
            }}
          >
            刷新
          </Button>
          <Button type="primary" onClick={() => setRoleEdit({})}>
            新增角色
          </Button>
        </Space>,
      )}
      <Tabs
        items={[
          {
            key: 'roles',
            label: '角色权限',
            children: (
              <Card>
                <Table
                  rowKey="id"
                  loading={loading}
                  dataSource={roles?.items}
                  columns={[
                    {
                      title: '角色',
                      render: (_, r) => (
                        <>
                          <b>{r.name}</b>
                          <br />
                          <Typography.Text type="secondary">
                            角色标识：<code>{r.code}</code>
                          </Typography.Text>
                        </>
                      ),
                    },
                    { title: '继承自', dataIndex: 'parent_id', render: roleNameByID },
                    {
                      title: '已授予权限',
                      render: (_, r) => (
                        <>
                          <Space wrap>
                            {r.permissions?.slice(0, 4).map((x: string) => (
                              <Tag key={x} title={x}>
                                {permissionName(x)}
                              </Tag>
                            ))}
                          </Space>
                          <br />
                          <Typography.Text type="secondary">
                            直接授予 {r.permissions?.length || 0} 项，包含继承后共{' '}
                            {r.effective_permissions?.length || 0} 项
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
                        <Space>
                          <Button type="link" onClick={() => setRoleEdit(r)}>
                            编辑
                          </Button>
                          <Button type="link" onClick={() => setPermissionEdit(r)}>
                            配置权限
                          </Button>
                          <Button type="link" onClick={() => openMenus(r)}>
                            配置菜单
                          </Button>
                        </Space>
                      ),
                    },
                  ]}
                />
              </Card>
            ),
          },
          {
            key: 'menus',
            label: '菜单管理',
            children: (
              <>
                <Alert
                  className="form-alert"
                  type="info"
                  showIcon
                  message="菜单名称面向管理员展示；菜单标识和页面路径用于二次开发，日常授权只需配置可见菜单。"
                />
                <Card
                  title="菜单列表"
                  extra={
                    <Button type="primary" onClick={() => setMenuEdit({})}>
                      新增菜单
                    </Button>
                  }
                >
                  <Table
                    rowKey="id"
                    dataSource={menus?.items}
                    columns={[
                      {
                        title: '菜单',
                        render: (_, r) => (
                          <>
                            <b>{r.name}</b>
                            <br />
                            <Typography.Text type="secondary">
                              标识：<code>{r.menu_code}</code>
                            </Typography.Text>
                          </>
                        ),
                      },
                      {
                        title: '打开页面',
                        dataIndex: 'path',
                        render: (v: string) => (v ? nameWithCode(routeNames[v] || v, v) : '目录'),
                      },
                      {
                        title: '进入权限',
                        dataIndex: 'permission_code',
                        render: (v: string) =>
                          v ? nameWithCode(permissionName(v), v) : '无需额外权限',
                      },
                      { title: '排序', dataIndex: 'sort_no' },
                      {
                        title: '可见',
                        dataIndex: 'visible',
                        render: (v) => (v ? <Tag color="success">可见</Tag> : <Tag>隐藏</Tag>),
                      },
                      {
                        title: '操作',
                        render: (_, r) => (
                          <Button type="link" onClick={() => setMenuEdit(r)}>
                            编辑
                          </Button>
                        ),
                      },
                    ]}
                  />
                </Card>
              </>
            ),
          },
        ]}
      />
      <Modal
        open={roleEdit !== null}
        title={roleEdit?.id ? '编辑角色' : '新增角色'}
        footer={null}
        destroyOnClose
        onCancel={() => setRoleEdit(null)}
      >
        {roleEdit !== null && (
          <Form
            layout="vertical"
            onFinish={saveRole}
            initialValues={{
              code: roleEdit.code,
              name: roleEdit.name,
              parent_id: roleEdit.parent_id || 0,
              sort_no: roleEdit.sort_no || 0,
              enabled: roleEdit.id ? roleEdit.status === 1 : true,
              remark: roleEdit.remark,
            }}
          >
            <Form.Item name="name" label="角色名称" rules={[{ required: true }]}>
              <Input placeholder="例如：客服管理员" />
            </Form.Item>
            <Form.Item
              name="code"
              label="角色标识"
              rules={[{ required: true }]}
              extra="供接口鉴权和二次开发使用，创建后不可修改"
            >
              <Input disabled={!!roleEdit.id} placeholder="例如：customer_service" />
            </Form.Item>
            <Form.Item name="parent_id" label="继承角色">
              <Select
                options={[
                  { label: '不继承其他角色', value: 0 },
                  ...(roles?.items || [])
                    .filter((r) => r.id !== roleEdit.id)
                    .map((r) => ({ label: r.name, value: r.id })),
                ]}
              />
            </Form.Item>
            <Form.Item name="sort_no" label="显示顺序">
              <InputNumber />
            </Form.Item>
            <Form.Item name="enabled" label="启用角色" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="remark" label="备注">
              <Input />
            </Form.Item>
            {roleEdit.id && <StepUpFields />}
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input placeholder="说明新增或调整原因" />
            </Form.Item>
            <Button type="primary" htmlType="submit">
              保存
            </Button>
          </Form>
        )}
      </Modal>
      <Modal
        width={860}
        open={!!permissionEdit}
        title={`配置权限：${permissionEdit?.name || ''}`}
        footer={null}
        destroyOnClose
        onCancel={() => setPermissionEdit(null)}
      >
        {permissionEdit && (
          <Form
            layout="vertical"
            onFinish={savePermissions}
            initialValues={{ permissions: permissionEdit.permissions }}
          >
            <Alert
              className="form-alert"
              type="info"
              showIcon
              message="权限已按业务模块分组。中文名称用于辨识，括号中的权限码用于接口鉴权和二次开发。"
            />
            <Form.Item name="permissions" label="授予权限">
              <Select
                mode="multiple"
                showSearch
                optionFilterProp="label"
                placeholder="按中文名称或权限码搜索"
                options={permissionOptions}
              />
            </Form.Item>
            <StepUpFields />
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input placeholder="说明权限调整原因" />
            </Form.Item>
            <Button type="primary" htmlType="submit">
              保存权限
            </Button>
          </Form>
        )}
      </Modal>
      <Modal
        open={!!menuGrant}
        title={`配置菜单：${menuGrant?.name || ''}`}
        footer={null}
        destroyOnClose
        onCancel={() => setMenuGrant(null)}
      >
        {menuGrant && (
          <Form layout="vertical" onFinish={saveMenus} initialValues={{ menu_ids: grantedMenuIDs }}>
            <Form.Item name="menu_ids" label="可见菜单">
              <Select
                mode="multiple"
                options={menus?.items.map((x) => ({ label: x.name, value: x.id }))}
              />
            </Form.Item>
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Button type="primary" htmlType="submit">
              保存菜单授权
            </Button>
          </Form>
        )}
      </Modal>
      <Modal
        open={menuEdit !== null}
        title={menuEdit?.id ? '编辑菜单' : '新增菜单'}
        footer={null}
        destroyOnClose
        onCancel={() => setMenuEdit(null)}
      >
        {menuEdit !== null && (
          <Form
            layout="vertical"
            onFinish={saveMenu}
            initialValues={{
              parent_id: menuEdit.parent_id || 0,
              menu_code: menuEdit.menu_code,
              name: menuEdit.name,
              icon: menuEdit.icon,
              path: menuEdit.path,
              permission_code: menuEdit.permission_code,
              menu_type: menuEdit.menu_type || 2,
              sort_no: menuEdit.sort_no || 0,
              visible: menuEdit.id ? menuEdit.visible === 1 : true,
              enabled: menuEdit.id ? menuEdit.status === 1 : true,
            }}
          >
            <Form.Item name="name" label="菜单名称" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Form.Item
              name="menu_code"
              label="菜单标识"
              rules={[{ required: true }]}
              extra="供前端扩展和接口使用，创建后不可修改"
            >
              <Input disabled={!!menuEdit.id} />
            </Form.Item>
            <Form.Item name="parent_id" label="上级菜单">
              <Select
                options={[
                  { label: '无（顶级菜单）', value: 0 },
                  ...(menus?.items || [])
                    .filter((x) => x.id !== menuEdit.id)
                    .map((x) => ({ label: x.name, value: x.id })),
                ]}
              />
            </Form.Item>
            <Form.Item name="menu_type" label="菜单类型">
              <Select
                options={[
                  { label: '目录', value: 1 },
                  { label: '页面', value: 2 },
                ]}
              />
            </Form.Item>
            <Form.Item name="path" label="打开页面" extra="目录无需选择页面">
              <Select allowClear showSearch optionFilterProp="label" options={routeOptions} />
            </Form.Item>
            <Form.Item name="permission_code" label="进入权限">
              <Select
                allowClear
                showSearch
                optionFilterProp="label"
                placeholder="按中文名称或权限码搜索"
                options={permissionOptions}
              />
            </Form.Item>
            <Form.Item name="icon" label="菜单图标">
              <Select allowClear options={iconOptions} />
            </Form.Item>
            <Form.Item name="sort_no" label="显示顺序">
              <InputNumber />
            </Form.Item>
            <Form.Item name="visible" label="在侧边栏显示" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="enabled" label="启用菜单" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name="reason" label="操作原因" rules={[{ required: true }]}>
              <Input placeholder="说明菜单调整原因" />
            </Form.Item>
            <Button type="primary" htmlType="submit">
              保存菜单
            </Button>
          </Form>
        )}
      </Modal>
    </>
  );
}
