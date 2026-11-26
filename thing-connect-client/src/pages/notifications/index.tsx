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

const kinds = ['crying', 'quarrel'] as const;
type Kind = typeof kinds[number];
type Preference = { interval_seconds: number | null; enabled: boolean; template_id?: string; suggested_seconds?: number };
type Session = { session_id: string; replace: boolean; template_id: string };
const titles = { crying: '疑似哭声', quarrel: '疑似争吵声' };
const base = '/v1/push/user';
const path = (device: string, kind: Kind) => base + '/preferences/' + encodeURIComponent(device) + '/' + kind;

export default function Notifications() {
  const [device, setDevice] = useState('');
  const [prefs, setPrefs] = useState<Partial<Record<Kind, Preference>>>({});
  const [values, setValues] = useState({ crying: '60', quarrel: '60' });
  const [session, setSession] = useState<Session>();
  const [report, setReport] = useState<{ session: Session; decision: string }>();
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
      try { id = (await request<{ device_id: string }>(base + '/references/' + encodeURIComponent(ref))).device_id; }
      catch (e) {
        if (!(e instanceof Error && 'code' in e && e.code === 41000)) throw e;
        await Taro.switchTab({ url: '/pages/devices/index' });
        await Taro.showToast({ title: '该提醒已失效', icon: 'none' });
        return;
      }
    }
    if (!id) throw new ApiError(40000, '缺少设备编号');
    setDevice(id);
    const [crying, quarrel] = await Promise.all(kinds.map(kind => request<Preference>(path(id, kind))));
    setPrefs({ crying, quarrel });
    setValues({ crying: String(crying.interval_seconds ?? crying.suggested_seconds ?? 60), quarrel: String(quarrel.interval_seconds ?? quarrel.suggested_seconds ?? 60) });
  }); });

  async function save(kind: Kind, reset: boolean) {
    const seconds = Number(values[kind]);
    if (!reset && (!/^\d+$/.test(values[kind]) || !Number.isInteger(seconds) || seconds > 86400)) throw new ApiError(40000, '请输入 0 至 86400 的整数秒');
    const p = await request<Preference>(path(device, kind), reset ? 'DELETE' : 'PUT', reset ? undefined : { interval_seconds: seconds });
    setPrefs(old => ({ ...old, [kind]: p }));
    setValues(old => ({ ...old, [kind]: String(p.interval_seconds ?? p.suggested_seconds ?? 60) }));
    setMessage('提醒间隔已保存');
  }
  async function prepare(kind: Kind) {
    const login = await Taro.login({ timeout: 10000 });
    const prepared = await request<Session>(base + '/subscriptions', 'POST', {
      app_id: Taro.getAccountInfoSync().miniProgram.appId, code: login.code,
      event_type: kind,
    });
    if (prepared.replace) {
      const choice = await Taro.showModal({ title: '替换接收微信', content: '此微信或本账号已有接收关联。替换后，旧关联中等待发送的提醒将终止。' });
      if (!choice.confirm) return;
    }
    await request(base + '/subscriptions/' + prepared.session_id + '/confirm', 'POST', { replace: prepared.replace });
    setSession(prepared);
  }
  async function finish(s: Session, decision: string) {
    await request(base + '/subscriptions/' + s.session_id + '/result', 'POST', { decision });
    setReport(undefined);
    setMessage(decision === 'accept' ? '设备提醒已订阅' : '未开通设备提醒');
  }
  async function authorize(s: Session) {
    const result = await new Promise<Record<string, string>>((resolve, reject) =>
      wx.requestSubscribeMessage({ tmplIds: [s.template_id], success: resolve, fail: () => reject(new ApiError(-1, '微信订阅未完成，请重试')) })
    );
    const decision = result[s.template_id] === 'accept' ? 'accept' : result[s.template_id] === 'reject' || result[s.template_id] === 'ban' ? 'reject' : 'cancel';
    setSession(undefined); setReport({ session: s, decision });
    await finish(s, decision);
  }
  const sharedTemplate = prefs.crying?.enabled && prefs.quarrel?.enabled && prefs.crying.template_id === prefs.quarrel.template_id;
  const subscriptionKinds = kinds.filter(kind => prefs[kind]?.enabled && !(sharedTemplate && kind === 'quarrel'));
  return <View className='page'>
    <Notice error>{error}</Notice><Notice>{message}</Notice>
    {device && <Text>设备 {device}</Text>}
    {kinds.map(kind => prefs[kind] && <View className='card' key={kind}>
      <Text>{titles[kind]} · {prefs[kind]?.interval_seconds ? '每 ' + prefs[kind]?.interval_seconds + ' 秒最多提醒一次' : '每次提醒'}</Text>
      <Input type='number' maxlength={5} value={values[kind]} onInput={e => setValues(old => ({ ...old, [kind]: e.detail.value }))} />
      <Action disabled={busy} onClick={() => void run(() => save(kind, false))}>保存间隔（秒）</Action>
      <Action disabled={busy} onClick={() => void run(() => save(kind, true))}>恢复每次提醒</Action>
    </View>)}
    {session || report ? <Action primary disabled={busy} onClick={() => void run(() => report ? finish(report.session, report.decision) : authorize(session!))}>
      {report ? '重试保存订阅结果' : '确认订阅微信提醒'}
    </Action> : subscriptionKinds.map(kind => <Action key={kind} primary disabled={busy} onClick={() => void run(() => prepare(kind))}>
      {sharedTemplate ? '订阅设备提醒' : '订阅' + titles[kind] + '提醒'}
    </Action>)}
  </View>;
}
