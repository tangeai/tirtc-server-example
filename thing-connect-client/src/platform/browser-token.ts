export function browserToken(): string | null {
  const raw = localStorage.getItem('token');
  if (!raw || !raw.trimStart().startsWith('{')) return raw;
  try {
    const value = JSON.parse(raw).data;
    if (typeof value === 'string' && value.length > 0) {
      localStorage.setItem('token', value);
      return value;
    }
  } catch { /* Invalid legacy wrapper requires a new browser session. */ }
  localStorage.removeItem('token');
  return null;
}
