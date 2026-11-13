import { PageContainer } from '@ant-design/pro-components';
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type DependencyList,
  type ReactNode,
} from 'react';
import { Alert, Form, Input, Tag, Typography } from 'antd';
import dayjs from 'dayjs';
import { reportError } from '../error-feedback';

export type AnyRow = Record<string, any>;
export type PageData<T = AnyRow> = { items: T[]; page: number; page_size: number; total: number };
export type PermissionDefinition = {
  code: string;
  name: string;
  group: string;
  description: string;
};

export function useLoad<T>(
  loader: () => Promise<T>,
  deps: DependencyList = [],
): [T | undefined, boolean, () => void] {
  const [data, setData] = useState<T>();
  const [loading, setLoading] = useState(true);
  const generation = useRef(0);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      generation.current++;
    };
  }, []);
  const load = useCallback(() => {
    const current = ++generation.current;
    setLoading(true);
    setData(undefined);
    loader()
      .then((value) => {
        if (mounted.current && generation.current === current) setData(value);
      })
      .catch((error: unknown) => {
        if (mounted.current && generation.current === current) {
          reportError(error, '数据加载失败');
        }
      })
      .finally(() => {
        if (mounted.current && generation.current === current) setLoading(false);
      });
  }, deps);
  useEffect(load, [load]);
  return [data, loading, load];
}

export const pageTitle = (name: string, description: string, action?: ReactNode) => (
  <PageContainer
    className="admin-page-header"
    ghost
    title={name}
    content={description}
    extra={action}
  />
);

export const serviceStatusTag = (value: string) =>
  value === 'healthy' ? (
    <Tag color="success">健康</Tag>
  ) : value === 'degraded' ? (
    <Tag color="warning">降级</Tag>
  ) : value === 'offline' ? (
    <Tag>离线</Tag>
  ) : (
    <Tag>状态未知</Tag>
  );

export const formatTime = (value: unknown) => {
  if (!value) return '—';
  const parsed = dayjs(String(value));
  return !parsed.isValid() || parsed.year() < 2000 ? '—' : parsed.format('YYYY-MM-DD HH:mm:ss');
};

type StepUpFieldsProps = {
  alert?: boolean;
  alertType?: 'info' | 'warning';
  message?: ReactNode;
  description?: ReactNode;
};

export function StepUpFields({
  alert = false,
  alertType = 'warning',
  message = '高风险操作，需要验证当前操作者身份',
  description,
}: StepUpFieldsProps) {
  return (
    <>
      {alert && (
        <Alert
          className="form-alert"
          type={alertType}
          showIcon
          message={message}
          description={description}
        />
      )}
      <Form.Item name="current_mfa_code" label="当前管理员的身份验证器验证码（6 位）">
        <Input maxLength={6} inputMode="numeric" autoComplete="one-time-code" />
      </Form.Item>
      <Form.Item name="current_recovery_code" label="或使用当前管理员的恢复码">
        <Input autoComplete="one-time-code" />
      </Form.Item>
    </>
  );
}

export function ServiceVersion({ version, commit }: { version?: string; commit?: string }) {
  const legacyCommit = /^[a-f0-9]{7,40}(?:-dirty)?$/i.test(version || '');
  const dated =
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(version || '') && dayjs(version).isValid();
  const sourceCommit = commit || (legacyCommit ? version : '');
  return (
    <div>
      <div>
        {dated
          ? `${formatTime(version)}（构建）`
          : legacyCommit
            ? '历史构建（未记录时间）'
            : version || '未上报'}
      </div>
      {sourceCommit && (
        <Typography.Text type="secondary" title={sourceCommit}>
          提交：{sourceCommit}
        </Typography.Text>
      )}
    </div>
  );
}
