import Taro from '@tarojs/taro';
import { Button, Switch, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Notice, Sheet } from '../../components/ui';
import { errorText, request } from '../../services/api';
import { ResourceEditor, resourcePaths, resourceTitles, type Kind } from './resource-editor';

type Reference = { id: string; name?: string; enabled?: boolean };
type Props = { kind: Kind; selected: string[]; change(ids: string[]): void; disabled: boolean; readonly: boolean; names?: Reference[] };

export function RoleResources({ kind, selected, change, disabled, readonly, names = [] }: Props) {
  const [owned,setOwned]=useState<Reference[]>([]), [shared,setShared]=useState<Reference[]>([]);
  const [error,setError]=useState(''), [busy,setBusy]=useState(false), [editing,setEditing]=useState<{id?:string}>();
  const [detail, setDetail] = useState<{name?: string; description?: string; id: string}>();
  const alive=useRef(true), locked=useRef(false);
  const title=resourceTitles[kind], path=resourcePaths[kind];
  async function showGlobalTool(id: string) {
    if (locked.current) return;
    locked.current = true;
    setBusy(true);
    setError('');
    try {
      const item = await request<{id: string; name?: string; description?: string}>('/v1/ai/mcp/tools/' + encodeURIComponent(id));
      if (alive.current) setDetail(item);
    } catch (e) {
      if (alive.current) setError(errorText(e));
    } finally {
      locked.current = false;
      if (alive.current) setBusy(false);
    }
  }
  async function load() {
    locked.current=true; setBusy(true); setError('');
    try {
      const read=async (url:string,set:(items:Reference[])=>void) => {
        const data=await request<{items:Reference[]|null}>(url);
        if(alive.current)set(data.items || []);
      };
      const reads=[read(path,setOwned)];
      if(kind==='mcp') reads.push(read('/v1/ai/mcp/tools',setShared));
      const results=await Promise.allSettled(reads);
      for(const result of results) if(result.status==='rejected') throw result.reason;
    } catch(e) {if(alive.current)setError(errorText(e));}
    finally {locked.current=false;if(alive.current)setBusy(false);}
  }
  useEffect(()=>{alive.current=true;void load();return ()=>{alive.current=false;};},[]);
  async function remove(item:Reference) {
    if(locked.current || disabled)return;
    locked.current=true;setBusy(true);setError('');
    try {
      const answer=await Taro.showModal({title:'删除'+title,content:'确定删除“'+(item.name || item.id)+'”？删除前请确认其他角色的引用。',confirmText:'删除',confirmColor:'#fa5151'});
      if(!answer.confirm || !alive.current)return;
      await request(path+'/'+encodeURIComponent(item.id),'DELETE');
      if(alive.current){setOwned(rows=>rows.filter(r=>r.id!==item.id));change(selected.filter(id=>id!==item.id));}
    } catch(e){if(alive.current)setError(errorText(e));}
    finally {locked.current=false;if(alive.current)setBusy(false);}
  }
  async function manageTool(item: Reference) {
    if (disabled || locked.current) return;
    const choice = await Taro.showActionSheet({ itemList: ['编辑工具', '删除工具'] }).catch(() => undefined);
    if (!choice || !alive.current) return;
    if (choice.tapIndex === 0) setEditing({ id: item.id });
    else if (choice.tapIndex === 1) await remove(item);
  }
  const catalog=new Map([...names,...shared,...owned].map(r=>[r.id,r]));
  const ids=readonly ? selected : [...new Set([...selected,...catalog.keys()])];
  return <View className={'card role-resource-group' + (kind === 'mcp' ? ' role-tool-list' : '')}>
    <View className='row between'><Text className='label'>{title}</Text>{!readonly && <Action disabled={busy || disabled} onClick={()=>setEditing({})}>新建{title}</Action>}</View>
    {ids.map(id=>{
      const item=catalog.get(id), on=selected.includes(id);
      const manageable=!readonly && owned.some(r=>r.id===id);
      const toolLink = kind === 'mcp' && item && (manageable || shared.some(tool => tool.id === id));
      return <View className='role-resource-item' key={id}>
        <View className='row between'>
          {toolLink ? <Button className='settings-link role-tool-link' disabled={busy || (disabled && !readonly)}
            ariaLabel={(manageable ? '管理工具 ' : '查看工具 ') + (item.name || id)}
            onClick={() => manageable ? void manageTool(item) : void showGlobalTool(id)}>
            <Text className='role-tool-name'>{item.name || id}</Text><View className='settings-chevron' ariaHidden />
          </Button> : <Text className='device-page-name'>{item?.name || id}</Text>}
          <Switch ariaLabel={item?.name || id} color='#07c160' checked={on} disabled={disabled || busy || (!on && item?.enabled===false)}
            onChange={e=>change(e.detail.value ? kind==='knowledge' ? [id] : [...new Set([...selected,id])] : selected.filter(v=>v!==id))}/></View>
        {manageable && kind !== 'mcp' && <View className='row'><Action disabled={disabled || busy} onClick={()=>setEditing({id})}>编辑</Action>
          <Action danger disabled={disabled || busy} onClick={()=>void remove(item!)}>删除</Action></View>}
      </View>;
    })}
    {!ids.length && !error && <Text className='muted'>{busy?'正在加载…':'暂无'+title}</Text>}
    <Notice error>{error}</Notice>{error && <Action disabled={busy} onClick={()=>void load()}>重新加载{title}</Action>}
    {detail && <Sheet title={detail.name || detail.id} onClose={() => setDetail(undefined)}>
      <Text>{detail.description || '暂无描述'}</Text><Text className='muted'>工具 ID · {detail.id}</Text>
    </Sheet>}
    {editing && <ResourceEditor kind={kind} id={editing.id} close={()=>setEditing(undefined)} saved={id=>{
      if(kind==='knowledge' && id && !editing.id)change([id]);
      setEditing(undefined);void load();
    }}/>}
  </View>;
}
