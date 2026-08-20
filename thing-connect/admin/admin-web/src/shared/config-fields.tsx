import { Alert, Form, Input, InputNumber, Select, Switch } from 'antd';
import type { AnyRow } from './admin-ui';

export type ConfigField = {
  path: string[];
  label: string;
  description?: string;
  kind: 'text' | 'number' | 'boolean' | 'select' | 'tags' | 'password';
  options?: { label: string; value: string }[];
  secret?: boolean;
  providers?: string[];
  required?: boolean;
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
    <Input.Password placeholder="留空保留当前密钥" />
  ) : (
    <Input />
  );

const captchaProviderDescriptions: Record<string, string> = {
  yidun: '仅显示网易易盾所需的 CaptchaID、Secret ID 和 Secret Key。',
  geetest: '仅显示极验 Web 参数；使用小程序验证时再填写小程序 ID 与密钥。',
  aliyun: '仅显示阿里云验证码 2.0 的 SceneId、Prefix、地域和 AccessKey。',
  tencent: '仅显示腾讯云 CaptchaAppId、云 API 密钥和应用密钥；小程序参数可选。',
};

export function FriendlyConfigFields({
  configKey,
  fields,
}: {
  configKey: string;
  fields: ConfigField[];
}) {
  const provider = Form.useWatch(['config', 'provider']) || 'yidun';
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
      {visibleFields.map((field) => (
        <Form.Item
          key={`${field.secret ? 'secret' : 'config'}/${field.path.join('.')}/${field.providers?.join(',') || 'all'}`}
          name={[field.secret ? 'secrets' : 'config', ...field.path]}
          label={field.label}
          valuePropName={field.kind === 'boolean' ? 'checked' : 'value'}
          preserve={!field.providers}
          extra={field.description}
          required={!field.secret && field.required}
          rules={[
            ...(!field.secret && field.required
              ? [{ required: true, message: `请填写${field.label}` }]
              : []),
            ...(field.kind === 'number' && field.min !== undefined
              ? [{ type: 'number' as const, min: field.min }]
              : []),
          ]}
        >
          {configFieldInput(field)}
        </Form.Item>
      ))}
    </>
  );
}
