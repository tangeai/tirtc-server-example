import { Input, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { DeviceAlarm } from './alarm-subscription';
import { Action, Notice } from './ui';
import { request, errorText, token } from '../services/api';

type Preference = { interval_seconds: number | null };

export function RecognitionReminders({ device, enabled }: { device: string; enabled: boolean }) {
  const path = '/v1/push/user/preferences/' + encodeURIComponent(device) + '/alarm';
  const owner = token();
  const [value, setValue] = useState<string>();
  const [error, setError] = useState('');
  const draft = useRef(''), saved = useRef<number>(), writing = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const mounted = useRef(false);

  async function load() {
    setError('');
    try {
      const p = await request<Preference>(path);
      if (!mounted.current) return;
      saved.current = p.interval_seconds ?? 0;
      draft.current = String(saved.current);
      setValue(draft.current);
    } catch (e) { if (mounted.current) setError(errorText(e)); }
  }

  async function flush() {
    clearTimeout(timer.current);
    if (writing.current || token() !== owner) return;
    const text = draft.current;
    if (!/^\d{1,5}$/.test(text) || Number(text) > 86400) {
      if (mounted.current) setError('请输入 0 至 86400 的整数秒');
      return;
    }
    if (Number(text) === saved.current) return;
    writing.current = true;
    if (mounted.current) setError('');
    try {
      const p = await request<Preference>(path, 'PUT', { interval_seconds: Number(text) });
      saved.current = p.interval_seconds ?? 0;
      if (mounted.current && draft.current === text) {
        draft.current = String(saved.current);
        setValue(draft.current);
      }
    } catch (e) {
      saved.current = undefined; // A lost acknowledgement leaves the server value unknown.
      if (mounted.current && draft.current === text) setError(errorText(e));
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

  function edit(text: string) {
    draft.current = text;
    setValue(text); setError('');
    clearTimeout(timer.current);
    timer.current = setTimeout(() => void flush(), 700);
  }

  return <View className='reminder-page'><View className='card reminder-settings'>
    <DeviceAlarm device={device} enabled={enabled} />
    {value !== undefined && <View className='reminder-interval-row reminder-line'>
      <View className='reminder-copy'><Text className='reminder-heading'>提醒间隔</Text>
        <Text className='reminder-caption'>填 0 表示每次异常都提醒</Text></View>
      <Input className='reminder-seconds-input' ariaLabel='提醒间隔（秒）' type='number' maxlength={5}
        value={value} onInput={e => edit(e.detail.value)} onBlur={() => void flush()} onConfirm={() => void flush()} />
      <Text>秒</Text>
    </View>}
    {value === undefined && !error && <Notice>正在加载提醒间隔…</Notice>}
    <Notice error>{error}</Notice>
    {error && value === undefined && <Action onClick={() => void load()}>重新加载提醒间隔</Action>}
    {error && value !== undefined && /^\d{1,5}$/.test(value) && Number(value) <= 86400 &&
      <Action onClick={() => void flush()}>重试保存间隔</Action>}
  </View></View>;
}
