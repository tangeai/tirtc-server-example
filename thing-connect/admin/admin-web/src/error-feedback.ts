export type AdminProblem = {
  message: string;
  code?: number | string;
  status?: number;
  suggestions: string[];
};

export type ErrorFeedbackItem = {
  id: number;
  problem: AdminProblem;
};

function normalizeAdminProblem(value: unknown): AdminProblem | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const candidate = value as Partial<AdminProblem>;
  if (typeof candidate.message !== 'string' || !candidate.message.trim()) return undefined;
  const code =
    typeof candidate.code === 'number' || typeof candidate.code === 'string'
      ? candidate.code
      : undefined;
  const status = typeof candidate.status === 'number' ? candidate.status : undefined;
  return {
    message: candidate.message,
    ...(code !== undefined ? { code } : {}),
    ...(status !== undefined ? { status } : {}),
    suggestions: Array.isArray(candidate.suggestions)
      ? candidate.suggestions.filter(
          (suggestion): suggestion is string =>
            typeof suggestion === 'string' && suggestion.trim() !== '',
        )
      : [],
  };
}

function problemFromUnknown(value: unknown, fallback: string): AdminProblem {
  if (value && typeof value === 'object' && 'problem' in value) {
    const problem = normalizeAdminProblem((value as { problem?: unknown }).problem);
    if (problem) return problem;
  }
  if (value instanceof TypeError) return problemFromRequestFailure('network', value);
  if (value instanceof Error && value.message.trim()) {
    return { message: value.message, suggestions: [] };
  }
  if (typeof value === 'string' && value.trim()) {
    return { message: value, suggestions: [] };
  }
  return { message: fallback, suggestions: [] };
}

function sameProblem(left: AdminProblem, right: AdminProblem) {
  return (
    left.message === right.message &&
    left.code === right.code &&
    left.status === right.status &&
    left.suggestions.length === right.suggestions.length &&
    left.suggestions.every((suggestion, index) => suggestion === right.suggestions[index])
  );
}

export class ErrorFeedbackStore {
  private items: ErrorFeedbackItem[] = [];
  private nextID = 1;
  private readonly listeners = new Set<() => void>();
  private readonly reported = new WeakSet<object>();

  readonly getSnapshot = () => this.items;

  readonly subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  report(value: unknown, fallback = '操作失败') {
    if (value !== null && (typeof value === 'object' || typeof value === 'function')) {
      if (this.reported.has(value as object)) return;
      this.reported.add(value as object);
    }
    const problem = problemFromUnknown(value, fallback);
    if (this.items.some((item) => sameProblem(item.problem, problem))) return;
    this.items = [...this.items, { id: this.nextID++, problem }].slice(-3);
    this.emit();
  }

  dismiss(id: number) {
    this.items = this.items.filter((item) => item.id !== id);
    this.emit();
  }

  private emit() {
    for (const listener of this.listeners) listener();
  }
}

export const errorFeedback = new ErrorFeedbackStore();
export const reportError = (value: unknown, fallback?: string) =>
  errorFeedback.report(value, fallback);

type APIResponseBody = {
  code?: unknown;
  msg?: unknown;
  data?: unknown;
};

function serverSuggestions(data: unknown): string[] {
  if (!data || typeof data !== 'object' || !('suggestions' in data)) return [];
  const suggestions = (data as { suggestions?: unknown }).suggestions;
  if (!Array.isArray(suggestions)) return [];
  return suggestions.filter(
    (suggestion): suggestion is string =>
      typeof suggestion === 'string' && suggestion.trim() !== '',
  );
}

export function problemFromAPIResponse(
  path: string,
  status: number,
  body: APIResponseBody,
): AdminProblem {
  const code = typeof body.code === 'number' ? body.code : undefined;
  const message =
    typeof body.msg === 'string' && body.msg.trim() ? body.msg : `请求失败（HTTP ${status}）`;
  const suggestions = serverSuggestions(body.data);
  if (path === '/auth/login' && status === 401) {
    suggestions.push(
      '确认使用管理员邮箱登录，普通用户账号不能登录管理后台',
      '重新输入密码；已启用 MFA 时，同时检查验证码或恢复码',
    );
  } else if (status === 400 || status === 422) {
    suggestions.push('检查表单中的必填项、格式和取值范围后重试');
  } else if (status === 401) {
    suggestions.push('登录状态已失效，请重新登录');
  } else if (status === 403) {
    suggestions.push('联系超级管理员确认当前账号的角色和权限');
  } else if (status === 404) {
    suggestions.push('刷新页面确认对象仍然存在，或返回列表重新选择');
  } else if (status === 409) {
    suggestions.push('刷新页面获取最新数据后重试，避免覆盖他人修改');
  } else if (status === 413) {
    suggestions.push('缩小上传文件后重试');
  } else if (status === 429) {
    suggestions.push('等待片刻后重试，避免连续提交');
  } else if (status >= 500) {
    suggestions.push('检查 Admin Server 及其必需依赖的就绪状态，然后重试');
  }
  return { message, code, status, suggestions };
}

export function problemFromRequestFailure(
  kind: 'network' | 'invalid-response',
  _cause?: unknown,
  status?: number,
): AdminProblem {
  if (kind === 'network') {
    return {
      message: '无法连接 Admin Server',
      suggestions: ['检查网络连接和 Admin Server 是否正在运行，然后重试'],
    };
  }
  return {
    message: status
      ? `Admin Server 返回了无法识别的响应（HTTP ${status}）`
      : 'Admin Server 返回了无法识别的响应',
    status,
    suggestions: ['检查反向代理和 Admin Server 日志，然后重试'],
  };
}
