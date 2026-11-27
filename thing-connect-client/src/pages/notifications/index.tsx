import Taro, { useLoad } from '@tarojs/taro';
import { Input, Text, View } from '@tarojs/components';
import { useRef, useState } from 'react';
import { Action, Notice } from '../../components/ui';
import { ApiError, request, errorText, token } from '../../services/api';

declare const wx: {
  requestSubscribeMessage(options: {
    tmplIds: string[];
    success(result: Record<string, string>): void;
    fail(error: { errMsg: string }): void;
  }): void;
};

type Preference = { interval_seconds: number | null; enabled: boolean; template_id?: string; suggested_seconds?: number };
const decisionMessages = {
  accept: '设备提醒已订阅',
  reject: '已拒绝设备提醒',
  cancel: '已取消本次订阅操作，原有提醒状态不变',
};
type Decision = keyof typeof decisionMessages;
type Session = { session_id: string; replace: boolean; template_id: string };
const base = '/v1/push/user';
const path = (device: string) => base + '/preferences/' + encodeURIComponent(device) + '/alarm';

export default function Notifications() {
  const [device, setDevice] = useState('');
  const [pref, setPref] = useState<Preference>();
  const [labelText,setLabelText]=useState<string>();
  const [value,setValue]=useState('60');
  const [session, setSession] = useState<Session>();
  const [report, setReport] = useState<{ session: Session; decision: Decision }>();
  const [message, setMessage] = useState(''), [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);

  async function run(action: () => Promise<void>) {
    if (lock.current) return;
    lock.current = true; setBusy(true); setError(''); setMessage('');
    try { await action(); } catch (e) { setError(errorText(e)); }
    finally { lock.current = false; setBusy(false); }
  }

  useLoad(options => { void run(async () => {
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
    const p=await request<Preference>(path(id));
    setPref(p);
    setValue(String(p.interval_seconds ?? p.suggested_seconds ?? 60));
  }); });

  async function save(reset: boolean) {
    const seconds = Number(value);
    if (!reset && (!/^\d+$/.test(value) || !Number.isInteger(seconds) || seconds > 86400)) throw new ApiError(40000, '请输入 0 至 86400 的整数秒');
    const p = await request<Preference>(path(device), reset ? 'DELETE' : 'PUT', reset ? undefined : { interval_seconds: seconds });
    setPref(p);
    setValue(String(p.interval_seconds ?? p.suggested_seconds ?? 60));
    setMessage('提醒间隔已保存');
  }
  async function prepare() {
    const login = await Taro.login({ timeout: 10000 });
    const prepared = await request<Session>(base + '/subscriptions', 'POST', {
      app_id: Taro.getAccountInfoSync().miniProgram.appId, code: login.code,
      event_type: "alarm",
    });
    if (prepared.replace) {
      const choice = await Taro.showModal({ title: '替换接收微信', content: '此微信或本账号已有接收关联。替换后，旧关联中等待发送的提醒将终止。' });
      if (!choice.confirm) return;
    }
    await request(base + '/subscriptions/' + prepared.session_id + '/confirm', 'POST', { replace: prepared.replace });
    setSession(prepared);
  }
  async function finish(s: Session, decision: Decision) {
    try {
      await request(base + '/subscriptions/' + s.session_id + '/result', 'POST', { decision });
    } catch (e) {
      if (e instanceof ApiError && (e.code === 41000 || e.code === 40901)) {
        setReport(undefined);
        setSession(undefined);
        throw new ApiError(e.code, '订阅会话已失效，请重新订阅');
      }
      throw e;
    }
    setReport(undefined);
    setMessage(decisionMessages[decision]);
  }
  async function authorize(s: Session) {
    const decision = await new Promise<Decision>((resolve, reject) =>
      wx.requestSubscribeMessage({
        tmplIds: [s.template_id],
        success(result) {
          const value = result[s.template_id];
          if (value === 'accept') resolve('accept');
          else if (value === 'reject' || value === 'ban') resolve('reject');
          else resolve('cancel');
        },
        fail(error) {
          if (/^requestSubscribeMessage:fail (?:user )?cancel$/i.test(error.errMsg.trim())) resolve('cancel');
          else reject(new ApiError(-1, '微信订阅未完成，请重试'));
        },
      })
    );
    setSession(undefined); setReport({ session: s, decision });
    await finish(s, decision);
  }
  return <View className='page'>
    <Notice error>{error}</Notice><Notice>{message}</Notice>
    {device && <Text>设备 {device}</Text>}
    {labelText !== undefined && <View className='card'><Text>AI 异常提醒 · 识别标签：{labelText}</Text></View>}
    {pref && <View className='card'>
      <Text>AI 异常提醒 · {pref.interval_seconds ? '每 ' + pref.interval_seconds + ' 秒最多提醒一次' : '每次提醒'}</Text>
      <Input type='number' maxlength={5} value={value} onInput={e => setValue(e.detail.value)} />
      <Action disabled={busy} onClick={() => void run(() => save(false))}>保存间隔（秒）</Action>
      <Action disabled={busy} onClick={() => void run(() => save(true))}>恢复每次提醒</Action>
    </View>}
    {session || report ? <Action primary disabled={busy} onClick={() => void run(() => report ? finish(report.session, report.decision) : authorize(session!))}>
      {report ? '重试保存订阅结果' : '确认订阅微信提醒'}
    </Action> : pref?.enabled && <Action primary disabled={busy} onClick={() => void run(() => prepare())}>
      订阅 AI 异常提醒
    </Action>}
  </View>;
}
