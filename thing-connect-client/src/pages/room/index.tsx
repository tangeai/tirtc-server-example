import Taro from '@tarojs/taro';
import { Switch, Text, View } from '@tarojs/components';
import { useState } from 'react';
import { Action, Field, Notice } from '../../components/ui';
import { useDeviceWorkspace } from '../../hooks/device-workspace';
import { request } from '../../services/api';
import { devicesChanged } from '../../services/device-events';
import { leavePayload, roomPayload, roomStates, type Assignment } from '../../domain/room';
export default function Room() {
  const [mode, setMode] = useState<'create' | 'join'>('create'), [code, setCode] = useState(''), [password, setPassword] = useState(''), [protectedRoom, setProtectedRoom] = useState(false), [notice, setNotice] = useState('');
  const w = useDeviceWorkspace(async id => ({ assignment: await request<Assignment>(url(id)), verified: true }));
  function url(id: string) { return '/v1/call/group/web/device/' + encodeURIComponent(id); }
  async function change(kind: 'create' | 'join' | 'leave') {
    if (!w.device || !w.value) return;
    let body: object;
    try { body = kind === 'leave' ? leavePayload(w.value.assignment) : roomPayload(kind, code, kind === 'create' && !protectedRoom ? '' : password); }
    catch (e) { w.setError(e instanceof Error ? e.message : '请检查房间号和密码'); return; }
    await w.run(async () => {
      const previous = w.value!.assignment;
      if (kind === 'leave') {
        const answer = await Taro.showModal({ title: '退出房间', content: `确定让此设备退出房间 ${previous.room_code || ''}？`, confirmText: '退出房间' });
        if (!answer.confirm || !w.alive.current) return;
      }
      setNotice('');
      const endpoint = url(w.device!.device_id);
      const assignment = await request<Assignment>(endpoint + '/' + kind, 'POST', body);
      devicesChanged(); w.commit({ assignment, verified: false });
      if (!w.alive.current) return;
      setPassword('');
      setNotice(kind === 'leave' ? '退出请求已保存。' : '加入请求已保存，设备连接状态以下方查询结果为准。');
      try { w.commit({ assignment: await request<Assignment>(endpoint), verified: true }); }
      catch { if (w.alive.current) setNotice('操作已成功，状态更新失败，请点击刷新。'); }
    });
  }
  const a = w.value?.assignment;
  return <View className='page content-page'><View className='row between'><Text className='title device-page-name'>{w.device?.device_name || '当前设备'}</Text><Action disabled={w.busy} onClick={() => { setNotice(''); void w.refresh(); }}>刷新</Action></View><Text className='device-page-description'>让这台设备创建或加入对讲房间，与同一房间内的其他设备进行语音对讲。</Text><Notice error>{w.error}</Notice><Notice>{w.busy ? '正在处理…' : notice}</Notice>
    {a && (a.desired_state === 'joined' ? <View className='card'><Text className='label'>当前房间 · {a.password_set ? '已设置密码' : '无密码'}</Text><Text className='title' selectable>{a.room_code}</Text><Notice>{roomStates[a.state] || '状态待同步'}</Notice><Text className='muted'>在线设备：{w.value?.verified ? a.online_count ?? 0 : '待更新'}</Text><View className='wide'><Action danger disabled={w.busy} onClick={() => void change('leave')}>退出房间</Action></View></View> : <View className='card'><View className='tabs'><Action primary={mode === 'create'} disabled={w.busy} onClick={() => { setMode('create'); setPassword(''); }}>创建房间</Action><Action primary={mode === 'join'} disabled={w.busy} onClick={() => { setMode('join'); setPassword(''); }}>加入房间</Action></View>
      {mode === 'join' ? <Field label='6 位房间号' value={code} disabled={w.busy} numeric onChange={s => setCode(s.replace(/\D/g, '').slice(0, 6))} /> : <View className='row between'><Text>设置房间密码</Text><Switch color='#07c160' checked={protectedRoom} disabled={w.busy} onChange={e => { setProtectedRoom(e.detail.value); setPassword(''); }} /></View>}
      {(mode === 'join' || protectedRoom) && <Field label={mode === 'join' ? '4 位密码（无密码则留空）' : '4 位密码'} value={password} password disabled={w.busy} onChange={s => setPassword(s.replace(/\D/g, '').slice(0, 4))} />}
      <View className='wide'><Action primary disabled={w.busy} onClick={() => { if (mode === 'create' && protectedRoom && !password) { w.setError('请输入 4 位房间密码'); return; } void change(mode); }}>{mode === 'create' ? '创建房间' : '加入房间'}</Action></View></View>)}
    <Text className='muted'>加入后设备默认收听，按住设备上的说话键发言。离线设备上线后自动加入。点击刷新查看最新状态。</Text>
  </View>;
}
