import { CodeInput } from '../../components/code-input';
import Taro from '@tarojs/taro';
import { Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Field, Notice } from '../../components/ui';
import { request, errorText } from '../../services/api';
import { devicesChanged } from '../../services/device-events';
export default function Bind() {
  const [mode, setMode] = useState('code'), [value, setValue] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState('');
  const lock = useRef(false), alive = useRef(true);
  useEffect(() => () => { alive.current = false; }, []);
  async function submit() {
    if (lock.current) return;
    const input = value.trim().toUpperCase();
    if (mode === 'code' ? !/^\d{6}$/.test(input) : !input) { setError(mode === 'code' ? '请输入 6 位数字验证码' : '请输入设备 ID'); return; }
    lock.current = true; setBusy(true); setError('');
    try {
      await request(mode === 'code' ? '/v1/user/device/bind' : '/v1/user/device/bind-by-id', 'POST', mode === 'code' ? { code: input } : { device_id: input });
      devicesChanged();
      if (alive.current) { await Taro.showToast({ title: '绑定成功', icon: 'success' }); await Taro.navigateBack(); }
    } catch (e) { if (alive.current) setError(errorText(e)); }
    finally { lock.current = false; if (alive.current) setBusy(false); }
  }
  async function scan() {
    try {
      const result = await Taro.scanCode({ scanType: ['qrCode'] });
      const raw = result.result.trim();
      const query = raw.match(/[?&](?:device_id|deviceid|sn|id)=([^&#]+)/i);
      let id = query ? decodeURIComponent(query[1]) : raw;
      if (!id || /[/?=&]/.test(id)) { setError('未识别到设备 ID，请手动输入'); return; }
      if (alive.current) setValue(id.toUpperCase());
    } catch { /* User cancellation leaves the input unchanged. */ }
  }
  return <View className='page content-page'><View className='tabs'>{[['code', '验证码绑定'], ['id', '设备 ID 绑定']].map(([key, label]) => <Action key={key} primary={mode === key} disabled={busy} onClick={() => { setMode(key); setValue(''); setError(''); }}>{label}</Action>)}</View>
    <View className='card'><Text className='title'>绑定你的设备</Text><Text className='muted'>{mode === 'code' ? '设备开机联网后，输入屏幕显示或语音播报的 6 位验证码。' : '输入设备 ID，或扫描设备二维码。'}</Text>{mode === 'code' ? <View className='field'><Text className='label'>6 位验证码</Text><CodeInput value={value} disabled={busy} onChange={setValue} label='6 位设备绑定码' /></View> : <Field label='设备 ID' value={value} disabled={busy} onChange={v => setValue(v.toUpperCase())} />}
    {mode === 'id' && process.env.TARO_ENV === 'weapp' && <Action disabled={busy} onClick={() => void scan()}>扫码识别</Action>}<Notice error>{error}</Notice><View className='wide'><Action primary disabled={busy} onClick={() => void submit()}>{busy ? '绑定中…' : '绑定设备'}</Action></View></View></View>;
}
