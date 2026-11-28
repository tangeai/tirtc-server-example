import Taro from '@tarojs/taro';
import { useEffect, useRef, useState } from 'react';
import { Text, View } from '@tarojs/components';
import { Action, Notice, Sheet } from './ui';
import { ApiError, errorText, request, token } from '../services/api';

const base = '/v1/push/user';
type Capability = { alarm: boolean; alarm_template_id?: string; alarm_subscribed?: boolean };
type Decision = 'accept' | 'reject' | 'cancel';
type Session = { session_id: string; replace: boolean; template_id: string };
type Pending = { owner: string; decision: Decision; session: Session; confirmed?: boolean; replaceApproved?: boolean };
declare const wx: {
  requestSubscribeMessage(options: {
    tmplIds: string[];
    success(result: Record<string, string>): void;
    fail(error: { errMsg: string; errCode?: number }): void;
  }): void;
};

function prompt(template: string): Promise<Decision> {
  return new Promise((resolve, reject) => wx.requestSubscribeMessage({
    tmplIds: [template],
    success(result) {
      const choice = result[template];
      resolve(choice === 'accept' ? 'accept' : choice === 'reject' || choice === 'ban' ? 'reject' : 'cancel');
    },
    fail(error) {
      if (/^requestSubscribeMessage:fail (?:user )?cancel$/i.test(error.errMsg.trim())) resolve('cancel');
      else {
        const code = error.errCode;
        const hint = code === 20004 ? '请在小程序设置中打开订阅消息，再重新授权' :
          code === 20001 ? '请联系管理员核对本小程序的订阅模板' : '微信未能打开订阅，请重新点击授权';
        reject(new ApiError(-1, typeof code === 'number' ? hint + '（微信错误码 ' + code + '）' : hint));
      }
    },
  }));
}

// A v1.3.0 server can confirm a decision but cannot read it back on page navigation.
let lastConfirmed: { owner: string; template: string; granted: boolean } | undefined;

export function useAlarmSubscription() {
  const [channel, setChannel] = useState<Capability>();
  const [notice, setNotice] = useState('');
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState(false);
  const [unavailable, setUnavailable] = useState(false);
  const pending = useRef<Pending>();
  const prepared = useRef<{ owner: string; session: Session; until: number }>();
  const locked = useRef(false);
  const revision = useRef(0);
  const owner = token();

  async function prepareSession() {
    const login = await Taro.login({ timeout: 10000 });
    if (token() !== owner) throw new ApiError(40901, '账号已切换，请重新打开页面');
    return request<Session>(base + '/subscriptions', 'POST', {
      app_id: Taro.getAccountInfoSync().miniProgram.appId, code: login.code, event_type: 'alarm',
    });
  }

  async function refresh(forAuthorization = false) {
    const version = ++revision.current;
    prepared.current = undefined;
    setChecking(true);
    try {
      const cap = await request<Capability>(base + '/capabilities');
      if (cap.alarm && (forAuthorization || !cap.alarm_subscribed || !cap.alarm_template_id)) {
        // The session must exist before WeChat accepts the gesture. Otherwise
        // a slow login could assign an older decision a newer sequence number.
        const start = Date.now();
        const session = await prepareSession();
        if (version !== revision.current || token() !== owner) return;
        if (cap.alarm_template_id && session.template_id !== cap.alarm_template_id) {
          throw new ApiError(40901, '提醒模板已更新，请重新检查');
        }
        prepared.current = { owner, session, until: start + 240000 };
        cap.alarm_template_id = session.template_id;
      }
      if (version === revision.current && token() === owner) {
        if (cap.alarm_subscribed === undefined && lastConfirmed?.owner === owner && lastConfirmed.template === cap.alarm_template_id) {
          cap.alarm_subscribed = lastConfirmed.granted;
        }
        setChannel(cap); setUnavailable(false);
      }
    } catch {
      if (version === revision.current && token() === owner) {
        setChannel(undefined); setUnavailable(true);
      }
    } finally {
      if (version === revision.current && token() === owner) setChecking(false);
    }
  }
  useEffect(() => {
    setChannel(undefined); setUnavailable(false); setNotice('');
    if (pending.current?.owner !== owner) pending.current = undefined;
    if (lastConfirmed?.owner !== owner) lastConfirmed = undefined;
    if (owner) void refresh();
    return () => { revision.current++; };
  }, [owner]);

  async function activate() {
    if (locked.current) return;
    locked.current = true; setBusy(true); setNotice(''); setFailed(false);
    try {
      let work = pending.current;
      if (!work || work.owner !== owner) {
        const attempt = prepared.current;
        if (!attempt || attempt.owner !== owner || attempt.until <= Date.now()) {
          await refresh(true);
          setNotice(prepared.current ? '请点击订阅按钮确认微信授权' : '提醒暂未就绪，请重新检查');
          return;
        }
        // No awaits before the native prompt. Retries may only report this
        // decision to the session that preceded the gesture.
        const decision = await prompt(attempt.session.template_id);
        prepared.current = undefined;
        work = { owner, decision, session: attempt.session };
        pending.current = work;
      }
      const checkOwner = () => {
        if (token() !== owner) throw new ApiError(40901, '账号已切换，请重新打开页面');
      };
      checkOwner();
      const session = work.session;
      if (session.replace && !work.replaceApproved) {
        if (work.decision !== 'accept') {
          pending.current = undefined; setNotice('保留原接收微信'); return;
        }
        const choice = await Taro.showModal({ title: '替换接收微信', content: '当前微信将用于接收本账号的提醒。它与其他账号的接收关联，以及本账号原有的接收关联，都将解除。原关联中等待发送的提醒将停止。' });
        if (!choice.confirm) { pending.current = undefined; setNotice('已保留原接收微信'); return; }
        work.replaceApproved = true;
      }
      checkOwner();
      const path = base + '/subscriptions/' + session.session_id;
      if (!work.confirmed) {
        await request(path + '/confirm', 'POST', { replace: session.replace });
        work.confirmed = true;
      }
      checkOwner();
      await request(path + '/result', 'POST', { decision: work.decision });
      pending.current = undefined;
      if (work.decision !== 'cancel') {
        lastConfirmed = { owner, template: session.template_id, granted: work.decision === 'accept' };
      }
      // A successful replay acknowledges this session even when a newer
      // session has since changed the account's grant.
      await refresh();
      if (work.decision !== 'accept') setNotice(work.decision === 'reject' ? '已拒绝设备提醒' : '已取消，保留原订阅状态');
    } catch (error) {
      if (error instanceof ApiError && (error.code === 41000 || error.code === 40901)) {
        pending.current = undefined;
        await refresh();
      }
      setFailed(true); setNotice(errorText(error));
    } finally { locked.current = false; setBusy(false); }
  }
  return { channel, busy: busy || checking, notice, failed, unavailable, pending: !!pending.current, activate, refresh };
}

export function AccountAlarm({ open, onOpenChange }: { open: boolean; onOpenChange(value: boolean): void }) {
  const alarm = useAlarmSubscription();
  const ready = alarm.channel?.alarm && !!alarm.channel.alarm_template_id;
  const subscribed = ready && alarm.channel?.alarm_subscribed;
  const label = subscribed ? '已订阅' : '订阅';

  function tap() {
    if (subscribed || !ready || alarm.pending) {
      onOpenChange(true);
      if (subscribed && !alarm.pending) void alarm.refresh(true);
    }
    else void alarm.activate();
  }
  useEffect(() => {
    if (alarm.notice) onOpenChange(true);
  }, [alarm.notice, alarm.busy, onOpenChange]);

  return <>
    <Action ariaLabel={'全部设备的微信异常提醒，' + label} disabled={alarm.busy} onClick={tap}>
      <View className='subscription-bell' ariaHidden /><Text>{label}</Text>
    </Action>
    {open && <Sheet title='微信异常提醒' onClose={() => onOpenChange(false)} busy={alarm.busy}>
      <Text>{subscribed ? '已订阅本账号所有绑定设备的异常提醒。' : '一次订阅，接收本账号所有绑定设备的异常提醒。'}</Text>
      {!ready && <Notice>{alarm.unavailable ? '提醒服务暂不可用' : alarm.channel ? '微信异常提醒尚未启用' : '正在读取提醒状态…'}</Notice>}
      <Notice error={alarm.failed}>{alarm.notice}</Notice>
      <Action disabled={alarm.busy} onClick={() => void alarm.activate()}>
        {alarm.pending ? '重试保存订阅结果' : subscribed ? '更换接收微信' : ready ? '订阅微信提醒' : '重新检查'}
      </Action>
    </Sheet>}
  </>;
}
