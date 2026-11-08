import { Button } from '../components/Button';
import { Input, Segmented, Alert, Collapse, Result } from 'antd';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Dialog } from '../components/Dialog';
import { Shell } from '../components/Shell';
import { clearDeviceSnapshots, errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';

export default function Bind({ onClose, onBound }: { onClose?(): void; onBound?(): void } = {}) {
  const { t } = useLanguage();
  const [mode, setMode] = useState<'code' | 'id'>('code');
  const [busy, setBusy] = useState(false);
  const [drafts,setDrafts] = useState({code:'',id:''});
  const content = <div className="bind-content">
    <Segmented block aria-label={t('绑定方式')} disabled={busy} value={mode} onChange={setMode} options={[{value:'code',label:t('使用绑定码'),title:''},{value:'id',label:t('设备 ID 绑定'),title:''}]}/>
    <BindForm key={mode} mode={mode} value={drafts[mode]} setValue={value=>setDrafts(current=>({...current,[mode]:value}))} busy={busy} setBusy={setBusy} onBound={onBound}/>
  </div>;
  return onClose ? <Dialog title="添加设备" onClose={onClose} closeDisabled={busy}>{content}</Dialog> : <Shell title="添加设备"><main className="narrow">{content}</main></Shell>;
}
function BindForm({ mode, value, setValue, busy, setBusy, onBound }: { value:string; setValue(value:string):void; mode:'code'|'id'; busy:boolean; setBusy(value:boolean):void; onBound?():void }) {
  const { t } = useLanguage();
  const [error, setError] = useState(''), [device, setDevice] = useState('');
  const submitting = useRef(false), alive = useRef(true);
  useEffect(()=>()=>{alive.current=false;},[]);
  async function submit(event: FormEvent) {
    event.preventDefault(); if (submitting.current || busy || (mode==='code' ? !/^[0-9]{6}$/.test(value) : !value.trim())) return;
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
    <div className="bind-intro"><p>{t(mode==='code' ? '输入绑定码，将设备添加到你的账号' : '输入设备的唯一 ID')}</p><span className="muted">{t(mode==='code' ? '设备联网后，会通过语音播报或屏幕显示绑定码。' : '设备需正常上电联网，且在当前账号的可绑定范围内。')}</span></div>
    <form className="bind-form" onSubmit={event=>void submit(event)}><div className="bind-field"><label id="bind-code-label">{t(mode==='code'?'设备绑定码（6 位数字）':'设备 ID')}</label>{mode==='code'?<div className="bind-code-otp" onKeyDownCapture={event=>{if(event.key!=='Backspace' || busy)return;const target=event.target;if(!(target instanceof HTMLInputElement) || target.value.trim())return;const inputs=Array.from(event.currentTarget.querySelectorAll('input'));const index=inputs.indexOf(target);if(index>0){event.preventDefault();event.stopPropagation();inputs[index-1].focus();inputs[index-1].select();}}} onPasteCapture={event=>{const digits=event.clipboardData.getData('text').normalize('NFKC').replace(/\D/g,'').slice(0,6);event.preventDefault();if(busy || !digits)return;setError('');setValue(digits);const inputs=event.currentTarget.querySelectorAll('input');inputs[Math.min(digits.length,5)]?.focus();}}><Input.OTP aria-labelledby="bind-code-label" aria-describedby="bind-expiry" length={6} autoFocus inputMode="numeric" disabled={busy} value={value} formatter={text=>text.normalize('NFKC').replace(/[^0-9 ]/g,'')} status={error?'error':undefined} onInput={cells=>{setError('');setValue(cells.map(cell=>cell || ' ').join(''));}}/></div>:<Input className="bind-id-input" aria-labelledby="bind-code-label" autoFocus autoComplete="off" placeholder={t('请输入设备 ID')} required disabled={busy} value={value} onChange={event=>{setError('');setValue(event.target.value.toUpperCase());}}/>}{mode==='code' && <p id="bind-expiry" className="bind-expiry muted">{t('绑定码默认有效期为 3 分钟，请尽快输入。过期后，请获取新的绑定码。')}</p>}</div>
    {error && <Alert className="bind-error" type="error" showIcon role="alert" message={t(error)}/>}<Button type="submit" className="primary wide" disabled={busy || (mode==='code'?!/^[0-9]{6}$/.test(value):!value.trim())}>{t(busy?'绑定中…':'绑定设备')}</Button></form>
    <Collapse className="bind-help" ghost items={[{key:'help',label:t(mode==='code'?'如何获取设备绑定码？':'在哪里查看设备 ID？'),children:<div className="bind-help-content">{mode==='code'?<><p>{t('确认设备已烧录小钛固件，然后接通电源。')}</p><p>{t('Wi-Fi 设备请先完成配网；4G 设备请插入可用的 SIM 卡，等待联网。')}</p><p>{t('听取设备播报或查看屏幕上的 6 位绑定码，输入上方即可绑定。')}</p></>:<><p>{t('可在设备标签、屏幕或设备端配置中查看设备 ID。')}</p><p>{t('设备 ID 需在您的账号配额内才有权限添加')}</p></>}</div>}]} />
  </>;
}
