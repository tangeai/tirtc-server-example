import { Tabs } from 'antd';
import { ConfigPage } from './config-page';
import { RoleTypesPage } from './role-types';

export function AIServicePage() {
  return (
    <Tabs
      items={[
        { key: 'config', label: '服务配置', children: <ConfigPage namespace="ai-server" /> },
        { key: 'role-types', label: '角色类型', children: <RoleTypesPage /> },
      ]}
    />
  );
}
