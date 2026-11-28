import Taro, { useLoad } from '@tarojs/taro';
import { Input, Text, View } from '@tarojs/components';
import { useRef, useState } from 'react';
import { DeviceAlarm } from '../../components/alarm-subscription';
import { Action, Notice, RobotIcon } from '../../components/ui';
import { ApiError, request, errorText, token } from '../../services/api';

type Preference = { interval_seconds: number | null; enabled: boolean; suggested_seconds?: number };
const base = '/v1/push/user';
const path = (device: string) => base + '/preferences/' + encodeURIComponent(device) + '/alarm';

export default function Notifications() {
  const [device, setDevice] = useState('');
  const [pref, setPref] = useState<Preference>();
  const [labelText,setLabelText]=useState<string>();
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

  const entry = useRef<{ ref?: string; device_id?: string; name?: string }>({});
  useLoad(options => {
    entry.current = { ...options };
    if (options.name) {
      try { entry.current.name = decodeURIComponent(options.name); }
      catch { entry.current.name = undefined; }
    }
    void run(loadPreferences);
  });

  async function loadPreferences() {
    const options = entry.current;
    const ref = options.ref;
    if (!token()) {
      await Taro.reLaunch({ url: '/pages/login/index' + (ref ? '?notification=' + encodeURIComponent(ref) : '') });
      return;
    }
    let id = options.device_id || '';
    if (ref) {
      try {
        const alert = await request<{ device_id: string; label_text?: string }>(base + '/references/' + encodeURIComponent(ref));
        id = alert.device_id;
        setLabelText(alert.label_text || '未提供标签');
      }
      catch (e) {
        if (!(e instanceof Error && 'code' in e && e.code === 41000)) throw e;
        await Taro.switchTab({ url: '/pages/devices/index' });
        await Taro.showToast({ title: '该提醒已失效', icon: 'none' });
        return;
      }
    }
    if (!id) throw new ApiError(40000, '缺少设备编号');
    setDevice(id);
    const p = await request<Preference>(path(id));
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
  const deviceName = entry.current.ref ? '' : entry.current.name;
  return <View className='page reminder-page'>
    <Notice error>{error}</Notice><Notice>{message}</Notice>
    {device && <View className='reminder-device'>
      <View className='reminder-avatar' ariaHidden><RobotIcon /></View>
      <View className='grow'><Text className='reminder-device-name'>{deviceName || device}</Text>
        <Text className='reminder-caption'>{deviceName ? '设备 ID · ' + device : '设备 ID'}</Text>
      </View>
    </View>}
    {labelText !== undefined && <View className='card'><Text>AI 异常提醒 · 识别标签：{labelText}</Text></View>}
    {!pref && busy && <Notice>正在加载设备提醒…</Notice>}
    {!pref && error && <Action disabled={busy} onClick={() => void run(loadPreferences)}>重新加载设备提醒</Action>}
    {pref && <View className='card reminder-settings'>
      <DeviceAlarm key={device} device={device} />
      <View className='reminder-section'>
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
      </View>
    </View>}
  </View>;
}
