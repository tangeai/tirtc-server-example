import { useEffect, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import { pageTitle } from '../../shared/admin-ui';

type RoleType = {
  language: string;
  source_voice_id?: string;
  match_kind?: string;
  id: string;
  name: string;
  voice_id: string;
  voice_model: string;
  prompt: string;
  sort_order: number;
  status: 'draft' | 'published' | 'offline';
  revision: number;
};
type Voice = { id: string; name: string; model: string; sample_url?: string; languages?: string[] };
type Editor = RoleType & { reason: string; voice_selection: string };
const supported = (model: string) =>
  ['cosyvoice-v3', 'cosyvoice-v3-flash', 'seed-tts-2.0'].includes(model);
const voiceKey = (voice: Voice) => JSON.stringify([voice.model, voice.id]);
const statuses = { draft: '草稿', published: '已启用', offline: '未启用' };

export function RoleTypesPage() {
  const [items, setItems] = useState<RoleType[]>([]),
    [voices, setVoices] = useState<Voice[]>([]),
    [loading, setLoading] = useState(true),
    [loadError, setLoadError] = useState(false),
    [voiceError, setVoiceError] = useState(false);
  const [editing, setEditing] = useState<RoleType>(),
    [busy, setBusy] = useState(false),
    [query, setQuery] = useState(''),
    [language, setLanguage] = useState(''),
    [statusTarget, setStatusTarget] = useState<RoleType>(),
    [reason, setReason] = useState(''),
    [playing, setPlaying] = useState(false);
  const [form] = Form.useForm<Editor>();
  const audio = useRef<HTMLAudioElement>(null),
    mounted = useRef(true),
    operation = useRef(false),
    playRevision = useRef(0);
  const selectedVoice = Form.useWatch('voice_selection', form);
  const stop = () => {
    playRevision.current++;
    audio.current?.pause();
    setPlaying(false);
  };
  async function load() {
    setLoading(true);
    setLoadError(false);
    try {
      const data = await api<{ items: RoleType[] }>('/ai-role-types');
      if (mounted.current) setItems(data.items || []);
    } catch (error) {
      if (mounted.current) {
        setLoadError(true);
        reportError(error);
      }
    } finally {
      if (mounted.current) setLoading(false);
    }
  }
  async function loadVoices() {
    setVoiceError(false);
    try {
      const data = await api<{ items: Voice[] }>('/ai-role-types/voices');
      if (mounted.current) setVoices(data.items || []);
    } catch {
      if (mounted.current) setVoiceError(true);
    }
  }
  useEffect(() => {
    mounted.current = true;
    void load();
    void loadVoices();
    return () => {
      mounted.current = false;
      playRevision.current++;
      audio.current?.pause();
    };
  }, []);
  useEffect(() => {
    stop();
  }, [selectedVoice, editing?.id]);
  function edit(item?: RoleType) {
    const next: RoleType = item || {
      language: 'zh',
      id: '',
      name: '',
      voice_id: '',
      voice_model: 'cosyvoice-v3-flash',
      prompt: '',
      sort_order: 0,
      status: 'draft',
      revision: 0,
    };
    form.resetFields();
    form.setFieldsValue({
      ...next,
      reason: '',
      voice_selection: next.voice_id ? JSON.stringify([next.voice_model, next.voice_id]) : '',
    });
    setEditing(next);
  }
  async function close() {
    if (busy) return;
    if (form.isFieldsTouched())
      Modal.confirm({
        title: '放弃未保存的修改？',
        okText: '放弃修改',
        cancelText: '继续编辑',
        onOk: () => {
          stop();
          setEditing(undefined);
        },
      });
    else {
      stop();
      setEditing(undefined);
    }
  }
  async function save() {
    if (!editing || operation.current) return;
    let value: Editor;
    try {
      value = await form.validateFields();
    } catch {
      return;
    }
    const { voice_selection, reason: operationReason, ...fields } = value;
    const selected = voices.find((v) => voiceKey(v) === voice_selection);
    operation.current = true;
    setBusy(true);
    try {
      const saved = await api<RoleType>(
        '/ai-role-types' + (editing.id ? '/' + encodeURIComponent(editing.id) : ''),
        json(editing.id ? 'PUT' : 'POST', {
          role_type: {
            ...editing,
            ...fields,
            voice_id: voice_selection ? selected?.id || editing.voice_id : '',
            voice_model: selected?.model || editing.voice_model,
          },
          expected_revision: editing.revision,
          reason: operationReason,
        }),
      );
      if (mounted.current) {
        setItems((current) =>
          editing.id
            ? current.map((item) => (item.id === saved.id ? saved : item))
            : [...current, saved],
        );
        stop();
        setEditing(undefined);
        message.success('角色类型已保存');
      }
    } catch (error) {
      if (mounted.current) reportError(error);
    } finally {
      operation.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  async function changeStatus() {
    if (!statusTarget || !reason.trim() || operation.current) return;
    operation.current = true;
    setBusy(true);
    try {
      const saved = await api<RoleType>(
        '/ai-role-types/' + encodeURIComponent(statusTarget.id) + '/status',
        json('PUT', {
          status: statusTarget.status === 'published' ? 'offline' : 'published',
          expected_revision: statusTarget.revision,
          reason,
        }),
      );
      if (mounted.current) {
        setItems((current) => current.map((item) => (item.id === saved.id ? saved : item)));
        setStatusTarget(undefined);
        message.success('角色类型状态已更新');
      }
    } catch (error) {
      if (mounted.current) reportError(error);
    } finally {
      operation.current = false;
      if (mounted.current) setBusy(false);
    }
  }
  async function preview() {
    const player = audio.current,
      voice = voices.find((v) => voiceKey(v) === selectedVoice);
    if (!player || !voice?.sample_url) return;
    if (playing) {
      stop();
      return;
    }
    stop();
    const revision = playRevision.current;
    player.src = voice.sample_url;
    try {
      await player.play();
      if (mounted.current && revision === playRevision.current) setPlaying(true);
    } catch {
      if (mounted.current && revision === playRevision.current) message.error('试听失败，请重试');
    }
  }
  const voice = voices.find((v) => voiceKey(v) === selectedVoice);
  return (
    <>
      {pageTitle(
        'AI 角色类型',
        '维护用户创建角色时使用的类型名称、音色和默认提示词。',
        <Space>
          <Button onClick={() => void load()} disabled={busy || loading}>
            刷新
          </Button>
          <Button type="primary" disabled={busy} onClick={() => edit()}>
            新增角色类型
          </Button>
        </Space>,
      )}
      {loadError && (
        <Alert type="error" message="角色类型加载失败，请刷新重试" style={{ marginBottom: 16 }} />
      )}
      <Card>
        <Typography.Paragraph>
          共 {items.length} 个角色类型 · 已启用{' '}
          {items.filter((item) => item.status === 'published').length} 个 · 未启用{' '}
          {items.filter((item) => item.status !== 'published').length} 个 · 未配置音色{' '}
          {items.filter((item) => !item.voice_id).length} 个 · 缺少提示词{' '}
          {items.filter((item) => !item.prompt.trim()).length} 个
        </Typography.Paragraph>
        <Select
          aria-label="筛选语言"
          value={language}
          onChange={setLanguage}
          style={{ width: 150, marginRight: 12 }}
          options={[
            { value: '', label: '全部语言' },
            ...Array.from(new Set(items.map((item) => item.language)))
              .sort()
              .map((value) => ({ value, label: value === 'zh' ? '中文' : value })),
          ]}
        />
        <Input.Search
          aria-label="搜索角色类型"
          placeholder="搜索角色类型"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ maxWidth: 320, marginBottom: 20 }}
        />
        <Table<RoleType>
          rowKey="id"
          loading={loading}
          scroll={{ x: 900 }}
          dataSource={items
            .filter(
              (item) =>
                (!language || item.language === language) &&
                [item.name, item.voice_id, item.voice_model].some((value) =>
                  value.toLowerCase().includes(query.toLowerCase()),
                ),
            )
            .sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name))}
          pagination={{ pageSize: 20, showSizeChanger: false }}
          columns={[
            { title: '角色类型名称', dataIndex: 'name', width: 170 },
            { title: '语言', dataIndex: 'language', width: 70 },
            {
              title: '角色音色',
              dataIndex: 'voice_id',
              render: (id, item) =>
                id ? (
                  <Space direction="vertical" size={2}>
                    <span>
                      {voices.find((v) => v.id === id && v.model === item.voice_model)?.name || id}
                    </span>
                    <Typography.Text type="secondary">{item.voice_model}</Typography.Text>
                    {item.match_kind === 'version' && <Tag>同系列版本</Tag>}
                    {item.match_kind === 'substitute' && (
                      <Tag color="gold" title="按角色风格选择的替代音色，不是原音色或同系列版本">
                        风格替代
                      </Tag>
                    )}
                    {!supported(item.voice_model) && (
                      <Tag color="orange">音色存在，当前暂不支持此模型</Tag>
                    )}
                  </Space>
                ) : (
                  <Tag color="orange">暂无对应音色</Tag>
                ),
            },
            {
              title: '默认提示词',
              dataIndex: 'prompt',
              width: 280,
              ellipsis: true,
              render: (value: string) => value || <Tag color="orange">来源未提供提示词</Tag>,
            },
            { title: '排序', dataIndex: 'sort_order', width: 70 },
            {
              title: '状态',
              dataIndex: 'status',
              render: (value: RoleType['status']) => (
                <Tag color={value === 'published' ? 'green' : 'default'}>{statuses[value]}</Tag>
              ),
            },
            {
              title: '操作',
              width: 170,
              render: (_, item) => (
                <Space>
                  <Button disabled={busy} onClick={() => edit(item)}>
                    编辑
                  </Button>
                  <Button
                    disabled={
                      busy ||
                      ((!item.voice_id || !item.prompt.trim() || !supported(item.voice_model)) &&
                        item.status !== 'published')
                    }
                    onClick={() => {
                      setReason('');
                      setStatusTarget(item);
                    }}
                  >
                    {item.status === 'published' ? '停用' : '启用'}
                  </Button>
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>
        模板更新只影响后续选择；用户已保存的角色与设备绑定保持原样。
      </Typography.Paragraph>
      <Modal
        open={!!editing}
        title={editing?.id ? '编辑角色类型' : '新增角色类型'}
        onCancel={() => void close()}
        onOk={() => void save()}
        confirmLoading={busy}
        cancelButtonProps={{ disabled: busy }}
        closable={!busy}
        maskClosable={false}
        okText="保存类型"
        cancelText="取消"
        width={720}
      >
        <Form form={form} layout="vertical" disabled={busy}>
          <Form.Item
            name="name"
            label="角色类型名称"
            rules={[
              { required: true, whitespace: true, message: '请输入角色类型名称' },
              { max: 64, message: '名称不能超过 64 个字符' },
            ]}
          >
            <Input maxLength={64} />
          </Form.Item>
          {voiceError && (
            <Alert
              type="warning"
              message="音色列表暂不可用，已有音色和提示词仍可编辑保存。"
              action={<Button onClick={() => void loadVoices()}>重试</Button>}
              style={{ marginBottom: 16 }}
            />
          )}
          {!selectedVoice && (
            <Alert
              type="warning"
              message="暂无对应音色。保留类型和提示词为草稿，不提供试听或用户选择。"
              style={{ marginBottom: 16 }}
            />
          )}
          {selectedVoice &&
            !supported(
              voices.find((v) => voiceKey(v) === selectedVoice)?.model ||
                editing?.voice_model ||
                '',
            ) && (
              <Alert
                type="warning"
                message="探鸽存在此音色，当前角色编辑器暂不支持该模型。可以保存草稿，不能启用；不会自动换成其他音色。"
                style={{ marginBottom: 16 }}
              />
            )}
          <Form.Item
            name="language"
            label="角色语言"
            rules={[
              { required: true, message: '请输入语言代码' },
              { max: 16, message: '语言代码不能超过 16 字符' },
            ]}
          >
            <Input placeholder="zh / en / ja" />
          </Form.Item>
          <Form.Item
            name="voice_selection"
            label="角色音色"
            extra="只关联探鸽实际提供的对应音色；缺失时留空，不用相似音色代替。"
          >
            <Select
              allowClear
              showSearch
              optionFilterProp="label"
              onClear={() => form.setFieldValue('voice_selection', '')}
              options={[
                ...(selectedVoice && !voices.some((v) => voiceKey(v) === selectedVoice)
                  ? [
                      {
                        value: selectedVoice,
                        label: `${editing?.voice_id} · ${editing?.voice_model}`,
                      },
                    ]
                  : []),
                ...voices.map((v) => ({ value: voiceKey(v), label: `${v.name} · ${v.model}` })),
              ]}
            />
          </Form.Item>
          <Button
            disabled={busy || !voice?.sample_url}
            onClick={() => void preview()}
            style={{ marginBottom: 16 }}
          >
            {playing ? '停止试听' : '试听所选音色'}
          </Button>
          {selectedVoice && !voice?.sample_url && (
            <Typography.Text type="secondary"> 暂无试听音频</Typography.Text>
          )}
          <Form.Item
            name="prompt"
            label="默认提示词"
            extra="用户选择类型时自动填入，用户可以自行编辑。"
            rules={[
              {
                required: editing?.status === 'published',
                whitespace: true,
                message: '启用的角色类型必须有默认提示词',
              },
              { max: 12000, message: '提示词不能超过 12000 个字符' },
            ]}
          >
            <Input.TextArea rows={12} showCount maxLength={12000} />
          </Form.Item>
          <Form.Item
            name="sort_order"
            label="排序（数值越小越靠前）"
            rules={[{ required: true, message: '请输入排序值' }]}
          >
            <InputNumber min={0} max={99999} precision={0} />
          </Form.Item>
          <Form.Item
            name="reason"
            label="操作说明"
            rules={[{ required: true, whitespace: true, message: '请填写操作说明' }]}
          >
            <Input maxLength={150} />
          </Form.Item>
          <Alert
            type="info"
            message={
              editing?.status === 'published'
                ? '保存后供用户后续选择，不覆盖已有角色。'
                : '保存后可在列表中启用该类型。'
            }
          />
        </Form>
      </Modal>
      <Modal
        open={!!statusTarget}
        title={statusTarget?.status === 'published' ? '停用角色类型' : '启用角色类型'}
        onCancel={() => {
          if (!busy) setStatusTarget(undefined);
        }}
        onOk={() => void changeStatus()}
        confirmLoading={busy}
        okButtonProps={{ disabled: !reason.trim() }}
        cancelButtonProps={{ disabled: busy }}
        closable={!busy}
        okText="确认"
        cancelText="取消"
      >
        <p>
          {statusTarget?.name}：
          {statusTarget?.status === 'published'
            ? '停用后从选择列表隐藏，已有角色不受影响。'
            : '启用后用户可以选择此类型。'}
        </p>
        <Input
          aria-label="操作说明"
          placeholder="操作说明"
          value={reason}
          disabled={busy}
          maxLength={150}
          onChange={(e) => setReason(e.target.value)}
        />
      </Modal>
      <audio
        ref={audio}
        preload="none"
        onEnded={() => setPlaying(false)}
        onError={() => {
          setPlaying(false);
          message.error('试听失败，请重试');
        }}
      />
    </>
  );
}
