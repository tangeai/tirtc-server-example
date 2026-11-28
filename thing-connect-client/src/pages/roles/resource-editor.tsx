import Taro from '@tarojs/taro';
import { Input, Picker, Switch, Text, Textarea, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Notice, Sheet } from '../../components/ui';
import { request, errorText } from '../../services/api';

export const resourcePaths = {
  knowledge: '/v1/ai/knowledge/indexes',
  mcp: '/v1/ai/mcp/app-tools',
  plugins: '/v1/ai/plugins',
};
export const resourceTitles = { knowledge: '知识库', mcp: 'MCP 工具', plugins: '设备插件' };
export type Kind = keyof typeof resourcePaths;
type Parameter = { name?: string; type: string; description?: string; default_value?: string; enum?: string[]; required?: boolean };
type Config = { name?: string; url?: string; description?: string; enable?: boolean; type?: string; authentication?: {type: string; bearer_token?: string} | null };
type Definition = { name?: string; description?: string; action?: string; config?: Config; enabled?: boolean; input_params?: Parameter[]; return_params?: Parameter[]; document_ids?: string[] };

function Line({ label, value = '', set, secret, multiline }: { label: string; value?: string; set(value: string): void; secret?: boolean; multiline?: boolean }) {
  return <View className='field'><Text className='label'>{label}</Text>{multiline
    ? <Textarea className='textarea' ariaLabel={label} maxlength={-1} value={value} onInput={e => set(e.detail.value)} />
    : <Input className='input field-input' ariaLabel={label} maxlength={-1} password={secret} value={value} onInput={e => set(e.detail.value)} />}</View>;
}
function Choice({ label, value = '', options, set }: { label: string; value?: string; options: string[]; set(value: string): void }) {
  const values = [...new Set([...options, value])];
  return <View className='field'><Text className='label'>{label}</Text><Picker range={values.map(v => v || '未设置')} value={values.indexOf(value)} onChange={e => set(values[Number(e.detail.value)])}>
    <View className='input role-select'><Text className='role-select-value'>{value || '未设置'}</Text><View className='role-select-arrow'/></View>
  </Picker></View>;
}
function Toggle({ label, value, set }: { label: string; value?: boolean; set(value: boolean): void }) {
  return <View className='row between'><Text>{label}</Text><Switch ariaLabel={label} color='#07c160' checked={!!value} onChange={e => set(e.detail.value)} /></View>;
}
function Parameters({ title, rows, set }: { title: string; rows: Parameter[]; set(rows: Parameter[]): void }) {
  const keys = useRef<number[]>([]), next = useRef(0);
  while (keys.current.length < rows.length) keys.current.push(++next.current);
  function patch(index: number, change: Partial<Parameter>) { set(rows.map((p, i) => i === index ? { ...p, ...change } : p)); }
  return <View className='role-resource-group'><View className='row between'><Text className='label'>{title} · {rows.length}</Text>
    <Action onClick={() => set([...rows, { name: '', type: 'string' }])}>添加参数</Action></View>
    {rows.map((p, i) => <View className='role-param-card' key={keys.current[i]}>
      <Line label='参数名称' value={p.name} set={name => patch(i, { name })}/>
      <Choice label='类型' value={p.type} options={['string','integer','boolean','array','object']} set={type => patch(i, {type})}/>
      <Line label='参数说明' value={p.description} set={description => patch(i, { description })}/>
      <Line label='默认值' value={p.default_value} set={default_value => patch(i, { default_value })}/>
      <Line label='枚举值（每行一项）' multiline value={p.enum?.join('\n')} set={v => patch(i, {enum: v ? v.split('\n') : []})}/>
      <Toggle label='必填' value={p.required} set={required => patch(i, {required})}/>
      <Action danger onClick={() => { keys.current.splice(i, 1); set(rows.filter((_, n) => n !== i)); }}>移除参数</Action>
    </View>)}
  </View>;
}

export function ResourceEditor({ kind, id, close, saved }: {kind: Kind; id?: string; close(): void; saved(id?: string): void}) {
  const initial: Definition = kind === 'mcp' ? {enabled:true,config:{name:'',url:'',authentication:null}} : {};
  const [form, setForm] = useState<Definition>(initial), [ready, setReady] = useState(!id), [busy,setBusy] = useState(false), [error,setError] = useState('');
  const baseline = useRef(JSON.stringify(initial)), alive = useRef(true), locked = useRef(false);
  const path = resourcePaths[kind] + (id ? '/' + encodeURIComponent(id) : '');
  function patch(value: Partial<Definition>) { if (!locked.current) setForm(f => ({...f,...value})); }
  function config(value: Partial<Config>) { patch({config:{...form.config,...value}}); }
  async function load() {
    if (locked.current) return;
    locked.current=true; setError(''); setBusy(true);
    try {
      const result = await request<Definition>(path);
      if (alive.current) { baseline.current=JSON.stringify(result); setForm(result); setReady(true); }
    } catch(e) { if (alive.current) setError(errorText(e)); }
    finally { locked.current=false; if (alive.current) setBusy(false); }
  }
  useEffect(() => { alive.current=true; if (id) void load(); return () => {alive.current=false;}; }, []);
  async function dismiss() {
    if (locked.current) return;
    if (JSON.stringify(form)!==baseline.current) {
      const result=await Taro.showModal({title:'放弃修改？',content:'该资源尚有未保存的修改。',confirmText:'放弃修改'});
      if (!result.confirm || !alive.current) return;
    }
    close();
  }
  async function save() {
    if (locked.current || !ready) return;
    const name=(kind==='mcp' ? form.config?.name : form.name)?.trim();
    if (!name) {setError('请输入名称');return;}
    let body: Definition;
    if (kind==='mcp') {
      if (!form.config?.url?.trim()) {setError('请输入 MCP 服务 URL');return;}
      body={enabled:form.enabled,config:{...form.config,name,url:form.config.url.trim()}};
    } else if (kind==='plugins') {
      if (!/^[A-Za-z0-9_]{1,64}$/.test(form.action || '')) {setError('功能标识须为 1 至 64 位字母、数字或下划线');return;}
      const input=form.input_params || [], output=form.return_params || [];
      if ([...input,...output].some(p => !/^[A-Za-z0-9_]+$/.test(p.name || ''))) {setError('请为每个参数填写字母、数字或下划线组成的名称');return;}
      body={name,action:form.action,description:form.description,input_params:input,return_params:output};
    } else {
      if (!id && !form.description?.trim()) {setError('请输入知识库描述');return;}
      body={name,description:form.description};
      if (JSON.stringify(form.document_ids)!==JSON.stringify((JSON.parse(baseline.current) as Definition).document_ids)) body.document_ids=[...new Set((form.document_ids || []).map(v => v.trim()).filter(Boolean))];
    }
    locked.current=true;setBusy(true);setError('');
    try {
      const result=await request<{id?:string;index_id?:string}>(path,id?'PUT':'POST',body);
      if (alive.current) saved(id || result?.index_id || result?.id);
    } catch(e) { if (alive.current) setError(errorText(e)); }
    finally {locked.current=false;if(alive.current)setBusy(false);}
  }
  const mc=form.config || {};
  return <Sheet title={(id?'编辑':'新建')+resourceTitles[kind]} busy={busy} onClose={() => void dismiss()}>
    {busy && !ready && <Notice>正在加载配置…</Notice>}
    {ready && <View className='role-resource-group'>
      <Line label='名称' value={kind==='mcp'?mc.name:form.name} set={name => kind==='mcp'?config({name}):patch({name})}/>
      <Line label='描述' multiline value={kind==='mcp'?mc.description:form.description} set={description => kind==='mcp'?config({description}):patch({description})}/>
      {kind==='mcp' && <>
        <Line label='MCP 服务 URL' value={mc.url} set={url=>config({url})}/>
        <Choice label='传输协议' value={mc.type} options={['','sse','streamableHttp']} set={type=>config({type:type || undefined})}/>
        <Choice label='认证方式' value={mc.authentication?.type} options={['','BearerToken']} set={type=>config({authentication:type?{type,bearer_token:''}:null})}/>
        {mc.authentication && <Line secret label='Bearer Token' value={mc.authentication.bearer_token} set={bearer_token=>config({authentication:{...mc.authentication!,bearer_token}})}/>}
        <Toggle label='启用工具' value={form.enabled} set={enabled=>patch({enabled})}/>
        <Toggle label='启用运行时服务' value={mc.enable} set={enable=>config({enable})}/>
      </>}
      {kind==='plugins' && <>
        <Line label='功能标识（Action）' value={form.action} set={action=>patch({action})}/>
        <Parameters title='输入参数' rows={form.input_params || []} set={input_params=>patch({input_params})}/>
        <Parameters title='返回参数' rows={form.return_params || []} set={return_params=>patch({return_params})}/>
      </>}
      {kind==='knowledge' && id && <Line label='关联文档 ID（每行一项）' multiline value={form.document_ids?.join('\n')} set={v=>patch({document_ids:v.split('\n')})}/>}
    </View>}
    <Notice error>{error}</Notice>
    {ready ? <Action primary disabled={busy} onClick={()=>void save()}>保存{resourceTitles[kind]}</Action> : error && <Action disabled={busy} onClick={()=>void load()}>重新加载配置</Action>}
  </Sheet>;
}
