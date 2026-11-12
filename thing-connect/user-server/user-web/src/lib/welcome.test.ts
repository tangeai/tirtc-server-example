import assert from 'node:assert/strict';
import { test } from 'node:test';
import { draftOf, rolePayload, resetWelcomeMode, defaultWelcomeText, defaultWelcomePrompt, type Role } from './roles.ts';

test('welcome modes round trip and explicitly clear the inactive cloud setting', () => {
  const role: Role = { id: 'r', name: '角色', agent_config: { welcome_text: '你好', opaque: true } };
  const draft = draftOf(role);
  assert.equal(draft.welcomeMode, 'static');
  draft.welcomeMode = 'ai';
  draft.welcomePrompt = '  用温暖的语气打招呼  ';
  const ai = rolePayload(role, draft, []);
  assert.equal(ai.agent_config?.welcome_text, '');
  assert.equal(ai.agent_config?.welcome_prompt, '用温暖的语气打招呼');
  assert.equal(ai.agent_config?.opaque, true);
  assert.equal(draftOf(ai).welcomeMode, 'ai');
  assert.equal(draftOf(ai).welcomePrompt, '用温暖的语气打招呼');
  draft.welcomeMode = 'static';
  const fixed = rolePayload({ ...ai, id: 'r' }, draft, []);
  assert.equal(fixed.agent_config?.welcome_text, '你好');
  assert.equal(fixed.agent_config?.welcome_prompt, '');
  assert.equal(draftOf(fixed).welcomeMode, 'static');
  draft.welcome = '';
  assert.equal(rolePayload({ ...fixed, id: 'r' }, draft, []).agent_config?.welcome_text, '');
  draft.welcomeMode = 'ai'; draft.welcomePrompt = '  ';
  assert.throws(() => rolePayload(role, draft, []), /请输入 AI 欢迎语提示词/);
});

test('changing welcome mode replaces custom content with the corresponding default', () => {
  const draft = draftOf({ id: 'r', agent_config: { welcome_text: '自定义原文' } });
  Object.assign(draft, resetWelcomeMode(true));
  assert.equal(draft.welcomePrompt, defaultWelcomePrompt);
  assert.equal(draft.welcome, '');
  draft.welcomePrompt = '自定义提示词';
  Object.assign(draft, resetWelcomeMode(false));
  assert.equal(draft.welcome, defaultWelcomeText);
  assert.equal(draft.welcomePrompt, '');
  Object.assign(draft, resetWelcomeMode(true));
  assert.equal(draft.welcomePrompt, defaultWelcomePrompt);
  assert.equal(draftOf({}).welcome, defaultWelcomeText);
  assert.equal(draftOf({ id: 'r', agent_config: { welcome_text: '' } }).welcome, '');
});
