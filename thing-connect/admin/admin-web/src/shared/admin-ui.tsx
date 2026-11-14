import { type QueryFilterProps, PageContainer } from '@ant-design/pro-components';
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type DependencyList,
  type ReactNode,
} from 'react';
import { Button, Grid, Tag, Typography } from 'antd';
import { ReloadOutlined, SearchOutlined, CheckOutlined, CloseOutlined } from '@ant-design/icons';
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

const queryFieldWidths = {
  text: 244,
  wide: 280,
  status: 188,
  language: 160,
  number: 216,
  date: 332,
};

// Widths include the label and column gap; controls wrap instead of stretching.
export function useCompactQueryLayout(fields: (keyof typeof queryFieldWidths)[]) {
  const screens = Grid.useBreakpoint();
  const style: CSSProperties = {
    padding: 0,
    ...Object.fromEntries(
      fields.map((field, index) => [`--query-width-${index + 1}`, `${queryFieldWidths[field]}px`]),
    ),
  };
  return {
    className: 'compact-query',
    layout: screens.sm ? 'horizontal' : 'vertical',
    labelWidth: 'auto',
    span: 6,
    searchGutter: 12,
    defaultCollapsed: false,
    collapseRender: false,
    style,
  } satisfies QueryFilterProps;
}

export const queryActions = (loading = false) => ({
  searchConfig: { submitText: '查询', resetText: '重置' },
  submitButtonProps: { icon: <SearchOutlined />, loading, disabled: loading },
  resetButtonProps: { icon: <ReloadOutlined />, disabled: loading },
});

export const localPagination = {
  defaultPageSize: 20,
  showSizeChanger: false,
  showTotal: (total: number) => `共 ${total} 条`,
};

export const confirmationActions = {
  okText: '确认',
  cancelText: '取消',
  okButtonProps: { icon: <CheckOutlined /> },
  cancelButtonProps: { icon: <CloseOutlined /> },
};

// Page actions belong in the page header; independent panels own their refresh action.
// Refresh keeps the current query, sorting and pagination. Loading prevents repeat clicks.
export function RefreshButton({
  loading,
  onClick,
  disabled = false,
  label = '刷新',
}: {
  loading: boolean;
  onClick: () => void;
  disabled?: boolean;
  label?: string;
}) {
  return (
    <Button
      icon={<ReloadOutlined />}
      loading={loading}
      disabled={disabled || loading}
      aria-label={label}
      onClick={onClick}
    >
      刷新
    </Button>
  );
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
