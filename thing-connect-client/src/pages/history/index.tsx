import Taro from '@tarojs/taro';
import { Text, View } from '@tarojs/components';
import { useState } from 'react';
import { Action, Notice } from '../../components/ui';
import { useDeviceWorkspace } from '../../hooks/device-workspace';
import { request } from '../../services/api';
interface Entry { id: string; started_at: string | null; reference: string }
interface Message { role: string; content: string; time: string }
interface HistoryPage { items: Entry[]; page: number; has_more: boolean }
const stamp = (time: string | null) => time && !Number.isNaN(Date.parse(time)) ? new Date(time).toLocaleString() : '时间未知';
export default function History() {
  const [entry, setEntry] = useState<Entry>(), [messages, setMessages] = useState<Message[]>(), [notice, setNotice] = useState('');
  const w = useDeviceWorkspace(id => request<HistoryPage>(base(id) + '?page=1&page_size=10'));
  function base(id: string) { return '/v1/ai/device/' + encodeURIComponent(id) + '/conversations'; }
  async function page(number: number) {
    if (!w.device) return;
    await w.run(async () => {
      const result = await request<HistoryPage>(base(w.device!.device_id) + '?page=' + number + '&page_size=10');
      w.commit(result); if (w.alive.current) { setEntry(undefined); setMessages(undefined); }
    });
  }
  async function read(item: Entry) {
    if (!w.device) return;
    await w.run(async () => {
      setEntry(item); setMessages(undefined); setNotice('');
      const result = await request<{ items: Message[] }>(base(w.device!.device_id) + '/' + encodeURIComponent(item.id), 'GET', undefined, { 'X-Conversation-Reference': item.reference });
      if (w.alive.current) setMessages(result.items);
    });
  }
  async function remove() {
    if (!w.device || !entry || !w.value) return;
    await w.run(async () => {
      const answer = await Taro.showModal({ title: '删除会话', content: '删除后无法恢复，确定删除这条记录？', confirmText: '删除', confirmColor: '#fa5151' });
      if (!answer.confirm || !w.alive.current) return;
      await request(base(w.device!.device_id) + '/' + encodeURIComponent(entry.id), 'DELETE', undefined, { 'X-Conversation-Reference': entry.reference });
      if (!w.alive.current) return;
      const remaining = w.value!.items.filter(item => item.id !== entry.id);
      w.commit({ ...w.value!, items: remaining }); setEntry(undefined); setMessages(undefined); setNotice('会话已删除');
      const number = !remaining.length && w.value!.page > 1 ? w.value!.page - 1 : w.value!.page;
      try { w.commit(await request<HistoryPage>(base(w.device!.device_id) + '?page=' + number + '&page_size=10')); }
      catch { if (w.alive.current) setNotice('会话已删除，列表更新失败，请点击刷新。'); }
    });
  }
  return <View className='page content-page'><View className='row between'><Text className='title'>AI 对话记录</Text><Action disabled={w.busy} onClick={() => { if (w.value) void page(w.value.page); else void w.refresh(); }}>刷新列表</Action></View><Notice error>{w.error}</Notice><Notice>{w.busy ? '正在加载…' : notice}</Notice>
    {entry ? <><View className='row between'><Action disabled={w.busy} onClick={() => { setEntry(undefined); setMessages(undefined); w.setError(''); }}>返回列表</Action><Action danger disabled={w.busy || !messages} onClick={() => void remove()}>删除会话</Action></View><Text className='muted'>{stamp(entry.started_at)}</Text>{!w.busy && !messages && <Action onClick={() => void read(entry)}>重试读取</Action>}{messages?.map((m, i) => <View className='card' key={i}><Text className='label'>{({ user: '用户', assistant: 'AI 助手', system: '系统', tool: '工具' } as Record<string,string>)[m.role] || '消息'} · {stamp(m.time)}</Text><Text selectable className='conversation-content'>{m.content}</Text></View>)}{messages && !messages.length && <Notice>这条会话暂无文字内容</Notice>}</> : <>{w.value?.items.map(item => <View className='card' key={item.id}><Text className='label'>{stamp(item.started_at)}</Text><Action disabled={w.busy} onClick={() => void read(item)}>查看对话</Action></View>)}{w.value && !w.value.items.length && <Notice>暂无AI 对话记录</Notice>}<View className='row between'><Action disabled={w.busy || !w.value || w.value.page <= 1} onClick={() => void page(w.value!.page - 1)}>上一页</Action><Text>第 {w.value?.page || 1} 页</Text><Action disabled={w.busy || !w.value?.has_more} onClick={() => void page(w.value!.page + 1)}>下一页</Action></View></>}
  </View>;
}
