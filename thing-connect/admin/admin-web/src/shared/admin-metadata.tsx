import { Tag, Typography } from 'antd';
import type { AnyRow } from './admin-ui';

export const prettyJSON = (value: unknown) => {
  try {
    return JSON.stringify(typeof value === 'string' ? JSON.parse(value) : value, null, 2);
  } catch {
    return String(value ?? '');
  }
};
export const serviceNames: Record<string, string> = {
  'device-server': '设备服务',
  'user-server': '用户服务',
  'voip-server': 'VoIP 服务',
  'ai-server': 'AI 服务',
  'call-server': '呼叫服务',
  'admin-server': '管理服务',
};
export const dependencyNames: Record<string, string> = {
  database: '数据库',
  redis: '缓存服务',
  mqtt: 'MQTT 消息服务',
};
export const configGroupNames: Record<string, string> = {
  device: '设备策略',
  mqtt: 'MQTT 通信',
  email: '邮件服务',
  captcha: '人机验证',
  email_template: '邮件模板',
  email_policy: '邮件策略',
  user: '用户策略',
  wechat: '微信应用',
  ai: 'AI 策略',
  call: '呼叫策略',
  tirtc: 'TiRTC 服务',
  security: '后台安全',
};
export const assignmentNames: Record<string, string> = {
  dynamic: '设备池分配',
  preburn: '出厂预置',
};
export const loginMessageNames: Record<string, string> = {
  success: '登录成功',
  'invalid credentials': '邮箱或密码错误',
  'account disabled': '账号已禁用',
  'invalid MFA': '双重验证失败',
};
export const auditActionNames: Record<string, string> = {
  'admin.create': '新增管理员',
  'admin.update': '修改管理员',
  'admin.roles.update': '调整管理员角色',
  'admin.sessions.revoke': '撤销管理员会话',
  'admin.mfa.reset': '重置管理员双重验证',
  'admin.mfa.recovery.regenerate': '重新生成恢复码',
  'admin.password.change': '修改管理员密码',
  'role.create': '新增角色',
  'role.update': '修改角色',
  'role.permissions.update': '调整角色权限',
  'role.menus.update': '调整角色菜单',
  'menu.write': '保存菜单',
  'user.status.write': '修改用户状态',
  'user.quota.write': '调整用户绑定额度',
  'user.password_reset': '发送密码重置邮件',
  'device.unbind': '强制解绑设备',
  'device.import': '导入设备池',
  'config.write': '发布配置',
  'config.test': '测试配置',
  'dictionary.type.create': '新增字典类型',
  'dictionary.type.update': '修改字典类型',
  'dictionary.item.write': '保存字典项',
  'job.retry': '重试任务',
};
export const resourceNames: Record<string, string> = {
  admin_user: '管理员',
  role: '角色',
  menu: '菜单',
  user: '用户',
  device: '设备',
  admin_job: '任务',
  config: '配置项',
  dict_type: '字典类型',
  dict_item: '字典项',
};
export const jobTypeNames: Record<string, string> = { device_pool_import: '设备池批量导入' };
export const voipConfigStatusNames: Record<string, string> = {
  healthy: '配置完整',
  incomplete: '配置不完整',
  unconfigured: '尚未配置',
};
export const voipAuthStatusNames: Record<string, string> = {
  active: '授权有效',
  invalid: '授权失效',
};
export const voipInvalidReasonNames: Record<string, string> = {
  wechat_errcode_9: '微信返回授权失效，请用户重新授权',
};
export const configNames: Record<string, string> = {
  'device.code_policy': '设备验证码策略',
  'device.token_policy': '设备登录有效期',
  'mqtt.ack_policy': 'MQTT 确认策略',
  smtp: 'SMTP 邮件服务',
  captcha: '人机验证',
  'email.template.registration_code': '注册验证码邮件',
  'email.template.password_reset_code': '找回密码验证码邮件',
  'email.code_ttl': '邮件验证码有效期',
  'email.send_rate_limit': '邮件发送限频',
  'user.default_bind_quota': '新用户默认绑定额度',
  'user.token_policy': '用户登录有效期',
  'wechat.apps': '微信小程序',
  'ai.role_policy': 'AI 角色策略',
  'ai.resource_policy': 'AI 资源策略',
  'call.contact_policy': '联系人策略',
  'call.room_policy': '房间策略',
  tirtc: 'TiRTC 服务',
  'mfa.policy': '双重验证策略',
  'admin.session_policy': '管理员会话策略',
};
export const profileFieldNames: Record<string, string> = {
  screen_width: '屏幕宽度',
  screen_height: '屏幕高度',
  camera_rotation: '摄像头旋转角度',
  aspect_ratio: '画面宽高比',
  hor_mirror: '水平镜像',
  vert_mirror: '垂直镜像',
  object_fit: '画面填充方式',
  audio_rate: '音频采样率',
  audio_channels: '音频声道数',
  up_video_mt: '上行视频格式',
  down_video_mt: '下行视频格式',
  down_audio_mt: '下行音频格式',
  no_video: '不支持视频',
  calling_timeout_sec: '呼叫超时时间（秒）',
};
export const routeNames: Record<string, string> = {
  '/overview': '数据概览',
  '/users': '用户管理',
  '/devices': '设备管理',
  '/configs/device-server': '设备服务',
  '/configs/user-server': '用户服务',
  '/configs/voip-server': 'VoIP 服务',
  '/configs/ai-server': 'AI 服务',
  '/configs/call-server': '呼叫服务',
  '/configs/common': '通用配置',
  '/admin-users': '管理员',
  '/access': '权限与菜单',
  '/dictionaries': '数据字典',
  '/configs/system': '系统配置',
  '/jobs': '任务中心',
  '/login-logs': '登录日志',
  '/audit-logs': '操作日志',
};
export const iconOptions = [
  { label: '仪表盘', value: 'DashboardOutlined' },
  { label: '用户', value: 'TeamOutlined' },
  { label: '设备', value: 'ApiOutlined' },
  { label: '设置', value: 'SettingOutlined' },
  { label: '安全', value: 'SafetyOutlined' },
  { label: '列表', value: 'UnorderedListOutlined' },
  { label: '邮件', value: 'MailOutlined' },
  { label: '电话', value: 'PhoneOutlined' },
  { label: 'AI 机器人', value: 'RobotOutlined' },
  { label: '数据库', value: 'DatabaseOutlined' },
];
export const downloadDeviceImportTemplate = () => {
  const blob = new Blob(
    ['\ufeffdevice_id,device_key\r\nTC-DEVICE-000001,replace-with-device-secret\r\n'],
    { type: 'text/csv;charset=utf-8' },
  );
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = 'device-pool-import-template.csv';
  link.click();
  URL.revokeObjectURL(url);
};
export const serviceName = (code: string) => serviceNames[code] || code;
export const assignmentName = (code: string) => assignmentNames[code] || code || '—';
export const devicePresence = (row: Pick<AnyRow, 'presence_known' | 'online'>) =>
  !row.presence_known ? (
    <Tag color="warning">状态未知</Tag>
  ) : row.online ? (
    <Tag color="success">在线</Tag>
  ) : (
    <Tag>离线</Tag>
  );
export const auditActionName = (code: string) => auditActionNames[code] || code || '—';
export const resourceName = (code: string) => resourceNames[code] || code || '—';
export const nameWithCode = (name: string, code: string) => (
  <>
    <span>{name}</span>
    {name !== code && (
      <>
        <br />
        <Typography.Text type="secondary" code>
          {code}
        </Typography.Text>
      </>
    )}
  </>
);
