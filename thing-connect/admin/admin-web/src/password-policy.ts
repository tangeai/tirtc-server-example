export const ADMIN_PASSWORD_POLICY_MESSAGE =
  '至少 8 个字符，且必须包含英文大写字母、英文小写字母和数字';

export function isValidAdminPassword(password: string): boolean {
  return (
    Array.from(password).length >= 8 &&
    /[A-Z]/.test(password) &&
    /[a-z]/.test(password) &&
    /[0-9]/.test(password)
  );
}

export function validateAdminPassword(_: unknown, password?: string): Promise<void> {
  return !password || isValidAdminPassword(password)
    ? Promise.resolve()
    : Promise.reject(new Error(ADMIN_PASSWORD_POLICY_MESSAGE));
}
