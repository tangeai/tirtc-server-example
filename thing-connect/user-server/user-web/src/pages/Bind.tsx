import { useRef, useState, type FormEvent } from 'react';
import { Shell } from '../components/Shell';
import { clearDeviceSnapshots, errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';

export default function Bind() {
  const { t } = useLanguage();
  const [mode, setMode] = useState<'code' | 'id'>('code');
  return <Shell title="添加设备"><main className="narrow"><nav className="tabs" aria-label={t('绑定方式')}>
    <button type="button" aria-pressed={mode === 'code'} onClick={() => setMode('code')}>{t('验证码绑定')}</button><button type="button" aria-pressed={mode === 'id'} onClick={() => setMode('id')}>{t('设备 ID 绑定')}</button>
  </nav><BindForm key={mode} mode={mode}/></main></Shell>;
}
function BindForm({ mode }: { mode: 'code' | 'id' }) {
  const { t } = useLanguage();
  const [value, setValue] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState(''), [device, setDevice] = useState('');
  const submitting = useRef(false);
  async function submit(event: FormEvent) {
    event.preventDefault(); if (submitting.current) return;
    submitting.current = true; setBusy(true); setError('');
    try {
      const data = await request<{ device_id: string }>(mode === 'code' ? '/v1/user/device/bind' : '/v1/user/device/bind-by-id', { method: 'POST', body: mode === 'code' ? { code: value } : { device_id: value.trim() } });
      clearDeviceSnapshots();
      setDevice(data.device_id);
    } catch (e) { setError(errorText(e)); } finally { submitting.current = false; setBusy(false); }
  }
  if (device) return <section className="card"><h1>{t('绑定成功！')}</h1><p>{device}</p><a className="button primary" href="/devices">{t('返回设备列表')}</a></section>;
  return <><section className="card"><h2>{t('操作步骤')}</h2>{mode === 'code' ? <ol className="steps"><li><strong>{t('开机并联网')}</strong><p>{t('Wi-Fi 设备请先配网。')}<br/>{t('4G 设备请确认 SIM 卡可用，等待联网。')}</p></li><li><strong>{t('获取 6 位验证码')}</strong><p>{t('留意设备的语音播报或屏幕提示。')}</p></li><li><strong>{t('完成绑定')}</strong><p>{t('在下方输入验证码，点击“绑定设备”。')}</p></li></ol> : <p className="muted">{t('设备正常上电联网，等待绑定')}<br/>{t('在下方输入12位的设备ID，进行绑定')}<br/>{t('设备 ID 需在您的账号配额内才有权限添加')}</p>}</section>
    <form className="card" onSubmit={event => void submit(event)}><label>{t(mode === 'code' ? '6 位数字验证码' : '设备 ID')}<input autoComplete={mode === 'code' ? 'one-time-code' : 'off'} inputMode={mode === 'code' ? 'numeric' : 'text'} pattern={mode === 'code' ? '[0-9]{6}' : undefined} required disabled={busy} value={value} onChange={event => { setError(''); setValue(mode === 'code' ? event.target.value.replace(/\D/g, '').slice(0, 6) : event.target.value.toUpperCase()); }}/></label>
    {error && <p role="alert" className="error">{t(error)}</p>}<button type="submit" className="primary wide" disabled={busy || (mode === 'code' ? value.length !== 6 : !value.trim())}>{t(busy ? '绑定中…' : '绑定设备')}</button></form>
  </>;
}
