export class ApiError extends Error {
  code: number;
  constructor(code: number, message: string) { super(message); this.code = code; }
}
export function decodeResponse<T>(status: number, body: unknown, successCode = 200): T {
  if (!body || typeof body !== 'object' || !('code' in body) || typeof body.code !== 'number') {
    throw new ApiError(status === 401 ? 401 : -1, status === 401 ? '登录状态已过期，请重新登录' : '服务返回异常，请稍后重试');
  }
  const result = body as { code: number; msg?: string; data?: T };
  if (status < 200 || status >= 300 || result.code !== successCode) {
    throw new ApiError(result.code, typeof result.msg === 'string' && result.msg ? result.msg : '操作失败，请稍后重试');
  }
  return result.data as T;
}
export function errorText(error: unknown): string {
  return error instanceof ApiError ? error.message : '网络连接失败，请检查网络后重试';
}
