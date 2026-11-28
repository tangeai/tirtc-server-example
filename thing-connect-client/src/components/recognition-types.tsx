import { tr } from '../platform/translate';
import Taro from '@tarojs/taro';
import { Button } from '../platform/button';
import { Switch, Text, View } from '@tarojs/components';
import { useRef, useState } from 'react';
import { moveScheme, type Scheme } from '../domain/recognition';

type Props = {
  selected: Scheme[];
  catalog: Scheme[];
  locked: boolean;
  change(items: Scheme[]): void;
};

type Gesture = { from: number; startY: number; y: number; moved: boolean; centers: Promise<number[]> };

export function RecognitionTypes({ selected, catalog, locked, change }: Props) {
  const gesture = useRef<Gesture>();
  const skipTap = useRef(false);
  const [target, setTarget] = useState<number>();
  const [editingOrder, setEditingOrder] = useState(false);
  const sorting = editingOrder && selected.length > 1;
  const others = catalog.filter(item => !selected.some(s => s.scheme_id === item.scheme_id));

  function begin(from: number, y: number) {
    if (locked) return;
    skipTap.current = false;
    const centers = new Promise<number[]>(resolve => {
      Taro.createSelectorQuery().selectAll('.scheme-selected').boundingClientRect(result => {
        resolve(Array.isArray(result) ? result.map(rect => rect.top + rect.height / 2) : []);
      }).exec();
    });
    gesture.current = { from, startY: y, y, moved: false, centers };
  }

  function position(g: Gesture, centers: number[]) {
    let to = g.from;
    while (to > 0 && g.y < centers[to - 1]) to--;
    while (to + 1 < centers.length && g.y > centers[to + 1]) to++;
    return to;
  }

  async function move(y: number) {
    const g = gesture.current;
    if (!g) return;
    g.y = y;
    if (Math.abs(y - g.startY) > 5) g.moved = true;
    const centers = await g.centers;
    if (gesture.current === g && g.moved) setTarget(position(g, centers));
  }

  function cancel() {
    gesture.current = undefined;
    setTarget(undefined);
  }

  async function finish(y?: number) {
    const g = gesture.current;
    if (g && y !== undefined) {
      g.y = y;
      g.moved ||= Math.abs(y - g.startY) > 5;
    }
    if (!g?.moved) { cancel(); return; }
    skipTap.current = true;
    const centers = await g.centers;
    if (gesture.current !== g) return;
    const to = position(g, centers);
    cancel();
    if (!locked && centers.length === selected.length && to !== g.from) change(moveScheme(selected, g.from, to - g.from));
  }

  async function choosePosition(index: number) {
    if (skipTap.current) { skipTap.current = false; return; }
    const offsets = [-1, 1].filter(n => index + n >= 0 && index + n < selected.length);
    try {
      const result = await Taro.showActionSheet({ itemList: offsets.map(n => n < 0 ? '上移一位' : '下移一位') });
      change(moveScheme(selected, index, offsets[result.tapIndex]));
    } catch { /* Dismissing the sheet leaves the order unchanged. */ }
  }

  return <View className='card scheme-list'>
    <View className='scheme-header'><Text className='reminder-heading'>{tr("识别类型")}</Text>
      <Text className='scheme-count'>{tr("已开")} {selected.length} / 8</Text>
      {selected.length > 1 && <Button className='scheme-edit' disabled={locked}
        onClick={() => { cancel(); setEditingOrder(!sorting); }}>{sorting ? tr("完成") : tr("排序")}</Button>}
    </View>
    {sorting && <Text className='reminder-caption'>{tr("拖动右侧图标或点击调整顺序")}</Text>}
    {!sorting && selected.length >= 8 && <Text className='reminder-caption'>{tr("已达上限 8 项，关闭其他类型后可继续开启")}</Text>}
    {!selected.length && !others.length && <Text className='muted'>{tr("暂无可用类型")}</Text>}
    {(sorting ? selected : [...selected, ...others]).map((item, index) => {
      const on = index < selected.length;
      const output = ({ text: '文字', audio: '语音', text_audio: '文字＋语音' } as Record<string,string>)[item.output_mode];
      return <View key={item.scheme_id} className={'scheme-row' + (on ? ' scheme-selected' : '') + (target === index ? ' scheme-target' : '')}>
        <View className='grow'><Text>{item.name}</Text>
          <Text className='reminder-caption'>{[output && tr(output), item.description].filter(Boolean).join(' · ')}</Text></View>
        {sorting ? <View catchMove><Button className='scheme-grip' ariaLabel={tr("调整") + item.name + tr("顺序")} disabled={locked}
          onTouchStart={e => begin(index, e.touches[0].clientY)}
          onTouchMove={e => void move(e.touches[0].clientY)} onTouchEnd={e => void finish(e.changedTouches[0]?.clientY)}
          onTouchCancel={cancel}
          onClick={() => void choosePosition(index)}><View ariaHidden className='scheme-handle-lines' /></Button></View> : <Switch ariaLabel={item.name} checked={on} disabled={locked || (!on && selected.length >= 8)}
          color='#07c160' onChange={e => {
            if (e.detail.value !== on) change(on ? selected.filter(s => s.scheme_id !== item.scheme_id) : [...selected, item]);
          }} />}
      </View>;
    })}
  </View>;
}
