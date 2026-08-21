import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Form,
  Input,
  Modal,
  Row,
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
import { pageTitle as title, useLoad, type AnyRow } from '../../shared/admin-ui';
import { ConfigPage, ServicePanel, type ConfigEntry, type Definition } from './config-page';

const templateExamples: Record<string, string> = {
  code: '123456',
  expires_in_minutes: '5',
  product_name: 'ThingConnect',
  support_email: 'support@example.com',
};
const renderTemplate = (source: string) =>
  Object.entries(templateExamples).reduce(
    (result, [key, value]) => result.replace(new RegExp(`\\{\\{\\s*${key}\\s*\\}\\}`, 'g'), value),
    source || '',
  );

function EmailTemplatesPage() {
  const [defs] = useLoad(
    () => api<{ items: Definition[] }>('/config-definitions?namespace=user-server'),
    [],
  );
  const [entries, loading, reload] = useLoad(
    () => api<{ items: ConfigEntry[] }>('/configs?namespace=user-server'),
    [],
  );
  const [editing, setEditing] = useState<{ definition: Definition; entry: ConfigEntry } | null>(
    null,
  );
  const [testing, setTesting] = useState<{ definition: Definition; entry: ConfigEntry } | null>(
    null,
  );
  const [draft, setDraft] = useState<AnyRow>({});
  const rows: Array<{ definition: Definition; entry: ConfigEntry }> = (defs?.items || [])
    .filter((x) => x.group === 'email_template')
    .map((definition) => ({
      definition,
      entry: entries?.items.find((x) => x.config_key === definition.config_key) || {
        namespace: 'user-server',
        config_key: definition.config_key,
        value: definition.default,
        secret_configured: false,
        using_default: true,
        revision: 0,
        status: 1,
      },
    }));
  const open = (row: { definition: Definition; entry: ConfigEntry }) => {
    setEditing(row);
    setDraft(row.entry.value as AnyRow);
  };
  const save = async (v: AnyRow) => {
    if (!editing) return;
    try {
      const value = {
        enabled: !!v.enabled,
        subject: v.subject,
        html_body: v.html_body,
        text_body: v.text_body || '',
      };
      await api(
        `/configs/user-server/${editing.definition.config_key}`,
        json('PUT', {
          value,
          status: 1,
          expected_revision: editing.entry.revision,
          reason: v.reason,
          confirm: true,
        }),
      );
      message.success('邮件模板已发布');
      setEditing(null);
      reload();
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  const sendTest = async (v: AnyRow) => {
    if (!testing) return;
    try {
      await api(
        `/configs/user-server/${testing.definition.config_key}/test`,
        json('POST', {
          value: testing.entry.value,
          test_recipient: v.test_recipient,
          reason: v.reason,
        }),
      );
      message.success('模板测试邮件已发送');
      setTesting(null);
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  return (
    <>
      <Alert
        className="form-alert"
        type="info"
        showIcon
        message="支持变量：{{code}}、{{expires_in_minutes}}、{{product_name}}、{{support_email}}；保存时后端会校验未知变量和邮件头注入。"
      />
      <Card
        title="邮件模板"
        extra={
          <Button icon={<ReloadOutlined />} onClick={reload}>
            刷新
          </Button>
        }
      >
        <Table
          rowKey={(r) => r.definition.config_key}
          loading={loading}
          dataSource={rows}
          pagination={false}
          columns={[
            {
              title: '用途',
              render: (_, r) => (
                <>
                  <b>{r.definition.name}</b>
                  <br />
                  <code>{r.definition.config_key}</code>
                </>
              ),
            },
            { title: '邮件主题', render: (_, r) => (r.entry.value as AnyRow).subject },
            {
              title: '模板状态',
              render: (_, r) =>
                (r.entry.value as AnyRow).enabled ? (
                  <Tag color="success">启用</Tag>
                ) : (
                  <Tag>停用</Tag>
                ),
            },
            { title: '修订', render: (_, r) => r.entry.revision || '未发布' },
            {
              title: '操作',
              render: (_, r) => (
                <Space>
                  <Button type="link" onClick={() => open(r)}>
                    编辑与预览
                  </Button>
                  <Button type="link" disabled={!r.entry.id} onClick={() => setTesting(r)}>
                    发送测试
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <Modal
        width={1080}
        open={!!editing}
        title={editing?.definition.name}
        footer={null}
        destroyOnClose
        onCancel={() => setEditing(null)}
      >
        {editing && (
          <Row gutter={20}>
            <Col span={13}>
              <Form
                layout="vertical"
                onFinish={save}
                initialValues={editing.entry.value as AnyRow}
                onValuesChange={(_, all) => setDraft(all)}
              >
                <Form.Item name="enabled" label="启用模板" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="subject" label="邮件主题" rules={[{ required: true, max: 200 }]}>
                  <Input />
                </Form.Item>
                <Form.Item name="html_body" label="HTML 正文" rules={[{ required: true }]}>
                  <Input.TextArea className="code" rows={10} />
                </Form.Item>
                <Form.Item name="text_body" label="纯文本正文">
                  <Input.TextArea className="code" rows={5} />
                </Form.Item>
                <Form.Item name="reason" label="发布原因" rules={[{ required: true }]}>
                  <Input />
                </Form.Item>
                <Button type="primary" htmlType="submit">
                  校验并发布
                </Button>
              </Form>
            </Col>
            <Col span={11}>
              <Card size="small" title="模拟数据预览">
                <Typography.Text strong>{renderTemplate(draft.subject)}</Typography.Text>
                <iframe
                  className="email-preview"
                  sandbox=""
                  title="邮件 HTML 预览"
                  srcDoc={renderTemplate(draft.html_body)}
                />
                <Typography.Paragraph type="secondary" className="email-text-preview">
                  {renderTemplate(draft.text_body)}
                </Typography.Paragraph>
              </Card>
            </Col>
          </Row>
        )}
      </Modal>
      <Modal
        open={!!testing}
        title={`发送模板测试：${testing?.definition.name || ''}`}
        footer={null}
        destroyOnClose
        onCancel={() => setTesting(null)}
      >
        {testing && (
          <Form layout="vertical" onFinish={sendTest}>
            <Form.Item
              name="test_recipient"
              label="测试收件邮箱"
              rules={[{ required: true, type: 'email' }]}
            >
              <Input />
            </Form.Item>
            <Form.Item name="reason" label="测试原因" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Button type="primary" htmlType="submit">
              发送测试邮件
            </Button>
          </Form>
        )}
      </Modal>
    </>
  );
}

function UserConfigsPage() {
  const [entries] = useLoad(
    () => api<{ items: ConfigEntry[] }>('/configs?namespace=user-server'),
    [],
  );
  const smtp = entries?.items.find((x) => x.config_key === 'smtp');
  const test = async (v: AnyRow) => {
    if (!smtp) return;
    try {
      await api(
        '/configs/user-server/smtp/test',
        json('POST', { value: smtp.value, test_recipient: v.test_recipient, reason: v.reason }),
      );
      message.success('SMTP 测试邮件已发送');
    } catch (e) {
      message.error((e as Error).message);
    }
  };
  return (
    <>
      <Card size="small" title="邮件服务连通性测试（SMTP）" className="form-alert">
        <Form layout="inline" onFinish={test}>
          <Form.Item name="test_recipient" rules={[{ required: true, type: 'email' }]}>
            <Input placeholder="测试收件邮箱" />
          </Form.Item>
          <Form.Item name="reason" rules={[{ required: true }]}>
            <Input placeholder="测试原因" />
          </Form.Item>
          <Button htmlType="submit" disabled={!smtp?.id}>
            发送测试邮件
          </Button>
        </Form>
      </Card>
      <ConfigPage namespace="user-server" embedded excludeGroups={['email_template']} />
    </>
  );
}
export function UserServicePage() {
  return (
    <>
      {title('用户服务', 'SMTP、人机验证、邮件模板与用户策略')}
      <ServicePanel service="user-server" />
      <Tabs
        items={[
          { key: 'configs', label: '配置项', children: <UserConfigsPage /> },
          { key: 'templates', label: '邮件模板', children: <EmailTemplatesPage /> },
        ]}
      />
    </>
  );
}
