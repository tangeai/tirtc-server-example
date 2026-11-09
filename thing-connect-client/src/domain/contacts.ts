export interface Contact {
  id: number; type: 'device' | 'voip'; source: 'auto' | 'manual' | 'voip';
  device_id: string; device_name?: string; remark?: string; online?: boolean; wx_app_id?: string;
}
export interface PendingContact { id: number; initiator_device: string; target_device: string }
export const contactKey = (c: Contact) => JSON.stringify([c.type, c.id, c.device_id, c.wx_app_id]);
export const contactName = (c: Contact) => c.remark?.trim() || (c.type === 'voip' ? '微信用户' : c.device_name?.trim() || c.device_id);
export const canRemove = (c: Contact) => c.type === 'device' && c.source === 'manual';
export function filterContacts(rows: Contact[], search: string, filter: string) {
  const text = search.trim().toLowerCase();
  return rows.filter(c => (!text || [c.remark, c.device_name, c.device_id].some(v => v?.toLowerCase().includes(text)))
    && (filter === 'all' || (filter === 'voip' ? c.type === 'voip' : c.type === 'device' && c.source === (filter === 'mine' ? 'auto' : 'manual'))));
}
export function validateContactTarget(id: string, device: string, ownIds: string[], contacts: Contact[]) {
  return !id ? '请输入对方设备 ID' : id === device ? '不能添加自己为联系人'
    : contacts.some(c => c.type === 'device' && c.device_id === id) ? '该设备已在联系人列表中'
    : ownIds.includes(id) ? '我的设备会自动关联，无需申请' : '';
}
