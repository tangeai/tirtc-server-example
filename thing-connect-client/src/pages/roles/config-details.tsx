import { tr } from '../../platform/translate';
import { Text, View } from '@tarojs/components';
import { useState } from 'react';
import { Action } from '../../components/ui';

export function ConfigDetails({ value, title }: { value: unknown; title: string }) {
  const [open, setOpen] = useState(false);
  return <View className='card'>
    <Action onClick={() => setOpen(!open)}>{open ? tr("收起") : tr("查看")}{title}</Action>
    {open && <Text selectable className='role-config-values'>{JSON.stringify(value, (key, v) => {
      if (/token|secret|password|credential|authorization|api.?key|access.?key|private.?key/i.test(key)) return '••••••';
      return v;
    }, 2)}</Text>}
  </View>;
}
