import { tr } from '../platform/translate';
import { Button } from '../platform/button';
import { Input, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { DeviceAlarm } from './alarm-subscription';
import { Action, Notice } from './ui';
import { request, errorText, token } from '../services/api';

type Preference = { interval_seconds: number | null };

function validInterval(value: string) {
  return /^\d{1,5}$/.test(value) && Number(value) >= 60 && Number(value) <= 86400;
}

export function RecognitionReminders({ device, enabled }: { device: string; enabled: boolean }) {
  const path = '/v1/push/user/preferences/' + encodeURIComponent(device) + '/alarm';
  const owner = token();
  const [value, setValue] = useState<string>();
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const draft = useRef(''), saved = useRef<number>(), writing = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const mounted = useRef(false);

  async function load() {
    setError('');
    try {
      const p = await request<Preference>(path);
      if (!mounted.current) return;
      saved.current = Math.max(60, p.interval_seconds ?? 60);
      draft.current = String(saved.current);
      setValue(draft.current);
    } catch (e) { if (mounted.current) setError(errorText(e)); }
  }

  async function flush() {
    clearTimeout(timer.current);
    if (writing.current || token() !== owner) return;
    const text = draft.current;
    if (!validInterval(text)) {
      if (mounted.current) setError(tr("请输入 60 至 86400 的整数秒"));
      return;
    }
    if (Number(text) === saved.current) return;
    writing.current = true;
    if (mounted.current) { setError(''); setStatus(tr("保存中…")); }
    try {
      const p = await request<Preference>(path, 'PUT', { interval_seconds: Number(text) });
      saved.current = Math.max(60, p.interval_seconds ?? 60);
      if (mounted.current && draft.current === text) {
        draft.current = String(saved.current);
        setValue(draft.current); setStatus(tr("已保存"));
      }
    } catch (e) {
      saved.current = undefined; // A lost acknowledgement leaves the server value unknown.
      if (mounted.current && draft.current === text) { setStatus(''); setError(errorText(e)); }
    } finally {
      writing.current = false;
      if (draft.current !== text) void flush();
    }
  }

  useEffect(() => {
    mounted.current = true;
    void load();
    return () => {
      mounted.current = false;
      if (timer.current !== undefined) void flush();
    };
  }, [device]);

  function edit(text: string, immediate = false) {
    draft.current = text;
    setValue(text); setError(''); setStatus('');
    clearTimeout(timer.current);
    if (immediate) { void flush(); return; }
    timer.current = setTimeout(() => void flush(), 700);
  }

  return <View className='reminder-page'><View className='card reminder-settings'>
    {process.env.TARO_ENV === 'weapp' ? <DeviceAlarm device={device} enabled={enabled} /> : <Text className='muted'>{tr("请在微信小程序中授权接收此设备的异常提醒。")}</Text>}
    {value !== undefined && <View className='reminder-interval-row reminder-line'>
      <View className='reminder-copy'><Text className='reminder-heading'>{tr("提醒间隔")}<Text className='interval-status'>{status}</Text></Text>
        <Text className='reminder-caption'>{tr("同类提醒至少间隔 60 秒，最长 24 小时。")}{!enabled && tr("开启 AI 识别后生效。")}</Text></View>
      <Input className='reminder-seconds-input' ariaLabel={tr("提醒间隔（秒）")} type='number'
        value={value} onInput={e => edit(e.detail.value)} onBlur={() => void flush()} onConfirm={() => void flush()} />
      <Text>{tr("秒")}</Text>
    </View>}
    {value !== undefined && <View className='interval-presets'>
      {([['1 分钟', 60], ['5 分钟', 300], ['10 分钟', 600]] as const).map(([label, seconds]) =>
        <Button key={seconds} className={'interval-preset' + (value !== '' && Number(value) === seconds ? ' active' : '')}
          onClick={() => edit(String(seconds), true)}>{tr(label)}</Button>)}
    </View>}
    {value === undefined && !error && <Notice>{tr("正在加载提醒间隔…")}</Notice>}
    <Notice error>{error}</Notice>
    {error && value === undefined && <Action onClick={() => void load()}>{tr("重新加载提醒间隔")}</Action>}
    {error && value !== undefined && validInterval(value) &&
      <Action onClick={() => void flush()}>{tr("重试保存间隔")}</Action>}
  </View></View>;
}
