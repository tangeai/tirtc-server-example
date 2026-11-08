import { Button } from '../components/Button';
import { Input, Segmented, Alert, Result } from 'antd';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Dialog } from '../components/Dialog';
import { Shell } from '../components/Shell';
import { clearDeviceSnapshots, errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';

export default function Bind({ onClose, onBound }: { onClose?(): void; onBound?(): void } = {}) {
  const { t } = useLanguage();
  const [mode, setMode] = useState<'code' | 'id'>('code');
  const [busy, setBusy] = useState(false);
  const content = <div className="bind-content">
    <Segmented block aria-label={t('绑定方式')} disabled={busy} value={mode} onChange={setMode} options={[{value:'code',label:t('验证码绑定'),title:''},{value:'id',label:t('设备 ID 绑定'),title:''}]}/>
    <BindForm key={mode} mode={mode} busy={busy} setBusy={setBusy} onBound={onBound}/>
  </div>;
  return onClose ? <Dialog title="添加设备" onClose={onClose} closeDisabled={busy}>{content}</Dialog> : <Shell title="添加设备"><main className="narrow">{content}</main></Shell>;
}
function BindForm({ mode, busy, setBusy, onBound }: { mode:'code'|'id'; busy:boolean; setBusy(value:boolean):void; onBound?():void }) {
  const { t } = useLanguage();
  const [value, setValue] = useState(''), [error, setError] = useState(''), [device, setDevice] = useState('');
  const submitting = useRef(false), alive = useRef(true);
  useEffect(()=>()=>{alive.current=false;},[]);
  async function submit(event: FormEvent) {
    event.preventDefault(); if (submitting.current) return;
    submitting.current = true; setBusy(true); setError('');
    try {
      const data = await request<{ device_id: string }>(mode === 'code' ? '/v1/user/device/bind' : '/v1/user/device/bind-by-id', { method:'POST',body:mode === 'code' ? {code:value} : {device_id:value.trim()} });
      clearDeviceSnapshots();
      if (alive.current) { if (onBound) onBound(); else setDevice(data.device_id); }
    } catch(e) { if(alive.current) setError(errorText(e)); }
    finally { submitting.current=false; if(alive.current) setBusy(false); }
  }
  if(device) return <Result status="success" title={<h2>{t('绑定成功！')}</h2>} subTitle={device} extra={<Button type="button" className="primary" onClick={()=>location.assign('/devices')}>{t('返回设备列表')}</Button>}/>;
  return <>
    <Alert type="info" showIcon message={t(mode==='code' ? '获取 6 位验证码' : '设备正常上电联网，等待绑定')} description={mode==='code' ? <>{t('Wi-Fi 设备请先配网。')} {t('4G 设备请确认 SIM 卡可用，等待联网。')} {t('留意设备的语音播报或屏幕提示。')}</> : <>{t('在下方输入12位的设备ID，进行绑定')}。{t('设备 ID 需在您的账号配额内才有权限添加')}</>}/>
    <form onSubmit={event=>void submit(event)}><label>{t(mode==='code'?'6 位数字验证码':'设备 ID')}<Input autoFocus autoComplete={mode==='code'?'one-time-code':'off'} inputMode={mode==='code'?'numeric':'text'} pattern={mode==='code'?'[0-9]{6}':undefined} required disabled={busy} value={value} onChange={event=>{setError('');setValue(mode==='code'?event.target.value.replace(/\D/g,'').slice(0,6):event.target.value.toUpperCase());}}/></label>
    {error && <p role="alert" className="error">{t(error)}</p>}<Button type="submit" className="primary wide" disabled={busy || (mode==='code'?value.length!==6:!value.trim())}>{t(busy?'绑定中…':'绑定设备')}</Button></form>
  </>;
}
