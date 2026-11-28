import Taro from '@tarojs/taro';
import { useEffect, useRef, useState } from 'react';
import { Text, View } from '@tarojs/components';
import { Action, Notice } from './ui';
import { ApiError, errorText, request, token } from '../services/api';

const base = '/v1/push/user';
type Ticket = { sn_ticket: string; model_id: string };
type Capability = { device_id: string; alarm: boolean; alarm_template_id?: string; alarm_subscribed?: boolean };
type Decision = 'accept' | 'reject' | 'cancel';
type Session = { session_id: string; replace: boolean; template_id: string };
type Pending = { owner: string; decision: Decision; session: Session; confirmed?: boolean; replaceApproved?: boolean };
declare const wx: {
  requestSubscribeDeviceMessage(options: {
    sn: string; snTicket: string; modelId: string;
    tmplIds: string[];
    success(result: Record<string, string>): void;
    fail(error: { errMsg: string; errCode?: number }): void;
  }): void;
};

function prompt(template: string, device: string, ticket: Ticket): Promise<Decision> {
  if (typeof wx.requestSubscribeDeviceMessage !== 'function') throw new ApiError(-1, '请升级微信后开启设备提醒');
  return new Promise((resolve, reject) => wx.requestSubscribeDeviceMessage({
    sn: device, snTicket: ticket.sn_ticket, modelId: ticket.model_id,
    tmplIds: [template],
    success(result) {
      const choice = result[template];
      resolve(choice === 'accept' || choice === 'acceptWithAudio' ? 'accept' : choice === 'reject' || choice === 'ban' ? 'reject' : 'cancel');
    },
    fail(error) {
      if (/^requestSubscribe(?:Device)?Message:fail (?:user )?cancel$/i.test(error.errMsg.trim())) resolve('cancel');
      else {
        const code = error.errCode;
        const hint = code === 19720726 ? '微信未通过设备票据校验，请核对小程序 AppID、设备型号和设备编号' :
          code === 19720727 ? '设备票据已过期' :
          code === 20004 ? '请在小程序设置中打开订阅消息，再重新授权' :
          code === 20001 ? '请联系管理员核对本小程序的订阅模板' : '微信未能打开订阅，请重新点击授权';
        reject(new ApiError(code === 19720726 || code === 19720727 ? code : -1, typeof code === 'number' ? hint + '（微信错误码 ' + code + '）' : hint));
      }
    },
  }));
}

export function useAlarmSubscription(device: string, enabled = true) {
  const [channel, setChannel] = useState<Capability>();
  const [notice, setNotice] = useState('');
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [checking, setChecking] = useState(false);
  const [unavailable, setUnavailable] = useState(false);
  const pending = useRef<Pending>();
  const prepared = useRef<{ owner: string; session: Session; ticket: Ticket; until: number }>();
  const renewedTicket = useRef(false);
  const locked = useRef(false);
  const revision = useRef(0);
  const owner = token();

  async function prepareSession(ticket: Ticket) {
    const login = await Taro.login({ timeout: 10000 });
    if (token() !== owner) throw new ApiError(40901, '账号已切换，请重新打开页面');
    return request<Session>(base + '/device-subscriptions', 'POST', {
      app_id: Taro.getAccountInfoSync().miniProgram.appId, code: login.code, event_type: 'alarm', device_id: device, model_id: ticket.model_id,
    });
  }

  async function refresh(forAuthorization = false) {
    const version = ++revision.current;
    prepared.current = undefined;
    setChecking(true);
    try {
      const cap = await request<Capability>(base + '/capabilities?device_id=' + encodeURIComponent(device));
      if (cap.device_id !== device) throw new ApiError(50300, '请升级设备提醒服务');
      if (enabled && cap.alarm && (forAuthorization || !cap.alarm_subscribed || !cap.alarm_template_id)) {
        // The session must exist before WeChat accepts the gesture. Otherwise
        // a slow login could assign an older decision a newer sequence number.
        const start = Date.now();
        const ticket = await request<Ticket>('/v1/voip/user/sn-ticket', 'POST', {
          device_id: device, wx_app_id: Taro.getAccountInfoSync().miniProgram.appId,
        });
        if (!ticket.sn_ticket || !ticket.model_id) throw new ApiError(50300, '设备授权票据或型号不可用');
        const session = await prepareSession(ticket);
        if (version !== revision.current || token() !== owner) return;
        if (cap.alarm_template_id && session.template_id !== cap.alarm_template_id) {
          throw new ApiError(40901, '提醒模板已更新，请重新检查');
        }
        prepared.current = { owner, session, ticket, until: start + 240000 };
        cap.alarm_template_id = session.template_id;
      }
      if (version === revision.current && token() === owner) {
        setChannel(cap); setUnavailable(false); setNotice(''); setFailed(false);
      }
    } catch (error) {
      if (version === revision.current && token() === owner) {
        setChannel(undefined); setUnavailable(true); setFailed(true); setNotice(errorText(error));
      }
    } finally {
      if (version === revision.current && token() === owner) setChecking(false);
    }
  }
  useEffect(() => {
    renewedTicket.current = false;
    setChannel(undefined); setUnavailable(false); setNotice('');
    if (pending.current?.owner !== owner) pending.current = undefined;

    if (owner && device) void refresh();
    return () => { revision.current++; };
  }, [owner, device, enabled]);

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
        const decision = await prompt(attempt.session.template_id, device, attempt.ticket);
        renewedTicket.current = false;
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
      // A successful replay acknowledges this session even when a newer
      // session has since changed the account's grant.
      await refresh();
      if (work.decision !== 'accept') setNotice(work.decision === 'reject' ? '已拒绝设备提醒' : '已取消，保留原订阅状态');
    } catch (error) {
      if (error instanceof ApiError && (error.code === 19720726 || error.code === 19720727)) {
        prepared.current = undefined;
        if (renewedTicket.current && error.code === 19720726) {
          setUnavailable(true); setFailed(true); setNotice(errorText(error));
          return;
        }
        renewedTicket.current = true;
        await refresh(true);
        if (prepared.current) setNotice('设备票据已更新，请再次点击授权（微信错误码 ' + error.code + '）');
        return;
      }
      if (error instanceof ApiError && (error.code === 41000 || error.code === 40901 || error.code === 6006)) {
        pending.current = undefined;
        await refresh();
      }
      setFailed(true); setNotice(errorText(error));
    } finally { locked.current = false; setBusy(false); }
  }
  return { channel, busy: busy || checking, notice, failed, unavailable, pending: !!pending.current, prepared: !!prepared.current, activate, refresh };
}

export function DeviceAlarm({ device, enabled }: { device: string; enabled: boolean }) {
  const alarm = useAlarmSubscription(device, enabled);
  const subscribed = alarm.channel?.alarm_subscribed;
  const ready = alarm.channel?.alarm;
  const action = alarm.unavailable ? '重新检查' : alarm.pending ? '重试保存订阅结果' :
    subscribed ? alarm.prepared ? '确认微信授权' : '重新授权' : '开启微信提醒';
  return <View className='reminder-channel'><View className='reminder-line'><View className='reminder-copy'>
    <Text className='reminder-heading'>微信异常提醒</Text>
    <Text className={'reminder-caption' + (subscribed ? ' reminder-active' : '')}>{alarm.unavailable ? '暂时无法读取设备订阅状态' : ready ? subscribed ? '微信已授权' : '微信尚未授权' : alarm.channel ? '微信异常提醒尚未启用' : '正在读取提醒状态…'}</Text>
    </View>
    {(alarm.unavailable || enabled && ready) && <Action primary={!subscribed} disabled={alarm.busy}
      onClick={() => alarm.unavailable ? void alarm.refresh() : void alarm.activate()}>
      {action}
    </Action>}</View>
    {!enabled && <Text className='reminder-caption'>AI 识别已关闭，当前不会发送异常提醒。</Text>}
    <Notice error={alarm.failed}>{alarm.notice}</Notice>
  </View>;
}
