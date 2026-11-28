import { Input, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { DeviceAlarm } from './alarm-subscription';
import { Action, Notice } from './ui';
import { ApiError, request, errorText } from '../services/api';

type Preference = { interval_seconds: number | null; enabled: boolean; suggested_seconds?: number };
const base = '/v1/push/user';
const path = (device: string) => base + '/preferences/' + encodeURIComponent(device) + '/alarm';

export function RecognitionReminders({ device, enabled }: { device: string; enabled: boolean }) {
  const [pref, setPref] = useState<Preference>();
  const [value,setValue]=useState('60');
  const [editing, setEditing] = useState(false);
  const [message, setMessage] = useState(''), [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);

  async function run(action: () => Promise<void>) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError(''); setMessage('');
    try { await action(); } catch (e) { setError(errorText(e)); }
    finally { lock.current = false; setBusy(false); }
  }

  useEffect(() => { void run(loadPreferences); }, [device]);

  async function loadPreferences() {
    const p = await request<Preference>(path(device));
    setPref(p);
    setValue(String(p.interval_seconds ?? p.suggested_seconds ?? 60));
  }

  async function save(reset: boolean) {
    const seconds = Number(value);
    if (!reset && (!/^\d+$/.test(value) || !Number.isInteger(seconds) || seconds > 86400)) throw new ApiError(40000, '请输入 0 至 86400 的整数秒');
    const p = await request<Preference>(path(device), reset ? 'DELETE' : 'PUT', reset ? undefined : { interval_seconds: seconds });
    setPref(p);
    setValue(String(p.interval_seconds ?? p.suggested_seconds ?? 60));
    setEditing(false); setMessage('提醒频率已保存');
  }
  return <View className='reminder-page'>
    <View className='card reminder-settings'>
      <DeviceAlarm device={device} enabled={enabled} />
      <Notice error>{error}</Notice><Notice>{message}</Notice>
      {!pref && busy && <Notice>正在加载提醒间隔…</Notice>}
      {!pref && error && <Action disabled={busy} onClick={() => void run(loadPreferences)}>重新加载提醒间隔</Action>}
      {pref && <View className='reminder-section'>
        <Action ariaLabel={'设置提醒间隔，当前 ' + (pref.interval_seconds ?? 0) + ' 秒'} disabled={busy} onClick={() => {
          if (editing) setEditing(false);
          else { setValue(String(pref.interval_seconds ?? pref.suggested_seconds ?? 60)); setEditing(true); }
        }}>
          <View className='reminder-line'><View className='reminder-copy'><Text className='reminder-heading'>提醒间隔</Text>
            <Text className='reminder-caption'>{pref.interval_seconds ? '间隔内的异常只提醒一次' : '每次异常都提醒'}</Text></View>
            <Text className='reminder-interval-value'>{(pref.interval_seconds ?? 0) + ' 秒'}</Text><Text className='reminder-chevron' ariaHidden>›</Text>
          </View>
        </Action>
        {editing && <View className='reminder-editor'>
          <Text className='label'>最短间隔（秒）</Text>
          <Input className='input' type='number' maxlength={5} value={value} onInput={e => setValue(e.detail.value)} />
          <View className='reminder-editor-actions'><Action primary disabled={busy} onClick={() => void run(() => save(false))}>保存间隔</Action>
          <Action disabled={busy} onClick={() => setEditing(false)}>取消</Action></View>

          {!!pref.interval_seconds && <Action disabled={busy} onClick={() => void run(() => save(true))}>恢复每次提醒</Action>}
        </View>}
      </View>}
    </View>
  </View>;
}
