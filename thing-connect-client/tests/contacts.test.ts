import assert from 'node:assert/strict';
import { test } from 'node:test';
import { canRemove, contactKey, contactName, filterContacts, validateContactTarget, type Contact } from '../src/domain/contacts.ts';
const rows: Contact[] = [
  { id: 1, type: 'device', source: 'auto', device_id: 'TIR-A', device_name: '客厅' },
  { id: 2, type: 'device', source: 'manual', device_id: 'TIR-B', remark: '朋友' },
  { id: 1, type: 'voip', source: 'voip', device_id: 'openid-fixture', wx_app_id: 'app-fixture' },
];
test('备注、名称和 ID 回退；微信联系人不展示 OpenID 作为名称', () => {
  assert.deepEqual(rows.map(contactName), ['客厅', '朋友', '微信用户']);
  assert.notEqual(contactKey(rows[0]), contactKey(rows[2]));
});
test('搜索与分类取交集，只有跨账号手动设备联系人可以删除', () => {
  assert.deepEqual(filterContacts(rows, ' tir-b ', 'manual'), [rows[1]]);
  assert.deepEqual(filterContacts(rows, 'TIR-B', 'voip'), []);
  assert.deepEqual(rows.map(canRemove), [false, true, false]);
});
test('申请阻止自己、重复联系人和同账号自动关联设备', () => {
  assert.ok(validateContactTarget('SELF', 'SELF', [], []));
  assert.ok(validateContactTarget('TIR-B', 'SELF', [], rows));
  assert.ok(validateContactTarget('OWN', 'SELF', ['OWN'], rows));
  assert.equal(validateContactTarget('OTHER', 'SELF', [], rows), '');
});
