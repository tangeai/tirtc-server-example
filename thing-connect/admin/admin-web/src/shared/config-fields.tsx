import { MinusCircleOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Form, Input, InputNumber, Select, Space, Switch, Tag } from 'antd';
import type { AnyRow } from './admin-ui';

export type ConfigField = {
  path: string[];
  label: string;
  description?: string;
  kind: 'text' | 'number' | 'boolean' | 'select' | 'tags' | 'password' | 'resource_refs';
  options?: { label: string; value: string }[];
  secret?: boolean;
  providers?: string[];
  required?: boolean;
  required_when_enabled?: boolean;
  blocking?: boolean;
  min?: number;
};

export const compactSecrets = (value: unknown): AnyRow | undefined => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const result: AnyRow = {};
  for (const [key, item] of Object.entries(value as AnyRow)) {
    if (item && typeof item === 'object' && !Array.isArray(item)) {
      const nested = compactSecrets(item);
      if (nested && Object.keys(nested).length) result[key] = nested;
    } else if (String(item ?? '').trim()) result[key] = item;
  }
  return Object.keys(result).length ? result : undefined;
};

const configFieldInput = (field: ConfigField) =>
  field.kind === 'boolean' ? (
    <Switch />
  ) : field.kind === 'number' ? (
    <InputNumber min={field.min} style={{ width: '100%' }} />
  ) : field.kind === 'select' ? (
    <Select allowClear options={field.options} />
  ) : field.kind === 'tags' ? (
    <Select mode="tags" tokenSeparators={[',']} placeholder="输入后按回车添加" />
  ) : field.kind === 'password' ? (
    <Input.Password placeholder="请输入密钥" />
  ) : (
    <Input />
  );

const captchaProviderDescriptions: Record<string, string> = {
  yidun: '仅显示网易易盾所需的 CaptchaID、Secret ID 和 Secret Key。',
  geetest: '仅显示极验 Web 参数；使用小程序验证时再填写小程序 ID 与密钥。',
  aliyun: '仅显示阿里云验证码 2.0 的 SceneId、Prefix、地域和 AccessKey。',
  tencent: '仅显示腾讯云 CaptchaAppId、云 API 密钥和应用密钥；小程序参数可选。',
};

function ResourceRefsField({ field }: { field: ConfigField }) {
  return (
    <Form.Item label={field.label} extra={field.description}>
      <Form.List name={['config', ...field.path]}>
        {(items, { add, remove }) => (
          <Space direction="vertical" style={{ width: '100%' }}>
            {items.map(({ key, name, ...restField }) => (
              <Space key={key} align="baseline" style={{ display: 'flex' }}>
                <Form.Item
                  {...restField}
                  name={[name, 'id']}
                  rules={[{ required: true, whitespace: true, message: '请填写资源 ID' }]}
                  style={{ marginBottom: 0 }}
                >
                  <Input placeholder="资源 ID" />
                </Form.Item>
                <Form.Item
                  {...restField}
                  name={[name, 'name']}
                  rules={[{ required: true, whitespace: true, message: '请填写资源名称' }]}
                  style={{ marginBottom: 0 }}
                >
                  <Input placeholder="资源名称" />
                </Form.Item>
                <Button
                  type="text"
                  danger
                  aria-label={`删除${field.label}`}
                  icon={<MinusCircleOutlined />}
                  onClick={() => remove(name)}
                />
              </Space>
            ))}
            <Button type="dashed" icon={<PlusOutlined />} onClick={() => add({ id: '', name: '' })}>
              添加{field.label}
            </Button>
          </Space>
        )}
      </Form.List>
    </Form.Item>
  );
}

export function FriendlyConfigFields({
  configKey,
  fields,
  secretConfigured,
  secretValuesAvailable,
}: {
  configKey: string;
  fields: ConfigField[];
  secretConfigured: boolean;
  secretValuesAvailable: boolean;
}) {
  const provider = Form.useWatch(['config', 'provider']) || 'yidun';
  const enabled = !!Form.useWatch(['config', 'enabled']);
  const visibleFields = fields.filter(
    (field) => !field.providers || field.providers.includes(provider),
  );
  return (
    <>
      {configKey === 'captcha' && (
        <Alert
          className="form-alert"
          type="info"
          showIcon
          message={captchaProviderDescriptions[provider] || '请选择验证服务商'}
          description="切换服务商后请填写新服务商对应的密钥；隐藏字段不会随表单提交。"
        />
      )}
      {visibleFields.map((field) => {
        if (field.kind === 'resource_refs') {
          return (
            <ResourceRefsField
              key={`config/${field.path.join('.')}/${field.providers?.join(',') || 'all'}`}
              field={field}
            />
          );
        }
        const required = !!field.required || (!!field.required_when_enabled && enabled);
        const enforceRequired =
          required && (!field.secret || !secretConfigured || secretValuesAvailable);
        return (
          <Form.Item
            key={`${field.secret ? 'secret' : 'config'}/${field.path.join('.')}/${field.providers?.join(',') || 'all'}`}
            name={[field.secret ? 'secrets' : 'config', ...field.path]}
            label={
              <>
                {field.label}
                {field.blocking && <Tag color="error">阻塞项</Tag>}
              </>
            }
            valuePropName={field.kind === 'boolean' ? 'checked' : 'value'}
            preserve={!field.providers}
            extra={field.description}
            required={required}
            rules={[
              ...(enforceRequired ? [{ required: true, message: `请填写${field.label}` }] : []),
              ...(field.kind === 'number' && field.min !== undefined
                ? [{ type: 'number' as const, min: field.min }]
                : []),
            ]}
          >
            {configFieldInput(field)}
          </Form.Item>
        );
      })}
    </>
  );
}
