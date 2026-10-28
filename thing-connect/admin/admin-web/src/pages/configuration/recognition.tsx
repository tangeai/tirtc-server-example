import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Drawer,
  Form,
  Input,
  InputNumber,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import { api, json } from '../../api';
import { reportError } from '../../error-feedback';
import { useLoad } from '../../shared/admin-ui';
import { ConfigPage } from './config-page';

type Scheme = {
  scheme_id: string;
  scheme_version: number;
  revision: number;
  name: string;
  description: string;
  media_type: string;
  model: string;
  prompt: string;
  output_mode: string;
  voice: string;
  limits: { max_file_bytes: number; max_audio_seconds: number };
  enabled: boolean;
};
type DeviceState = {
  policy: {
    enabled: boolean;
    allowed: boolean;
    monthly_limit: number;
    revision: number;
    schemes: { scheme_id: string; scheme_version: number }[];
  };
  schemes: Scheme[];
  usage: {
    used: number;
    month: string;
    input_tokens: number;
    output_tokens: number;
    unknown_calls: number;
    tts_characters: number;
  };
};
type RecordRow = {
  task: { request_id: string; device_id: string; status: string; results: unknown[] };
  calls: unknown[];
};
const outputs: Record<string, string> = { text: '文字', audio: '语音', text_audio: '文字＋语音' };
export function RecognitionPage({ mode = 'business' }: { mode?: 'business' | 'configuration' }) {
  const [access] = useLoad<{ permissions: string[] }>(() => api('/permissions'));
  const can = (permission: string) =>
    !!access?.permissions.some((p) => p === '*' || p === permission);
  const canReadTypes = can('recognition.read');
  const [data, , reload] = useLoad<Scheme[]>(
    () =>
      mode === 'configuration' && canReadTypes
        ? api<Scheme[]>('/recognition/schemes')
        : Promise.resolve([]),
    [mode, canReadTypes],
  );
  const [edit, setEdit] = useState<Scheme>();
  const [form] = Form.useForm();
  const [busy, setBusy] = useState(false);
  const [device, setDevice] = useState('');
  const [state, setState] = useState<DeviceState>();
  const [records, setRecords] = useState<RecordRow[]>([]);
  const [selected, setSelected] = useState<RecordRow>();
  const open = (s?: Scheme) => {
    const v = s || {
      scheme_id: '',
      scheme_version: 0,
      revision: 0,
      name: '',
      description: '',
      media_type: 'audio',
      model: 'qwen3-omni-flash',
      prompt: '',
      output_mode: 'text_audio',
      voice: 'Cherry',
      limits: { max_file_bytes: 5242880, max_audio_seconds: 15 },
      enabled: false,
    };
    setEdit(v);
    form.setFieldsValue({
      ...v,
      outputs: v.output_mode === 'text_audio' ? ['text', 'audio'] : [v.output_mode],
      file_mib: v.limits.max_file_bytes / 1048576,
      seconds: v.limits.max_audio_seconds,
      reason: '',
    });
  };
  const mutate = async (scheme: Scheme, action: string, reason: string) => {
    setBusy(true);
    try {
      await api(
        '/recognition/schemes',
        json('POST', { scheme, action, expected_revision: scheme.revision, reason }),
      );
      setEdit(undefined);
      await reload();
    } catch (e) {
      reportError(e);
    } finally {
      setBusy(false);
    }
  };
  const loadDevice = async () => {
    try {
      setState(await api<DeviceState>(`/devices/${encodeURIComponent(device)}/recognition`));
    } catch (e) {
      setState(undefined);
      reportError(e);
    }
  };
  const loadRecords = async () => {
    try {
      setRecords(
        await api<RecordRow[]>(`/recognition/requests?device_id=${encodeURIComponent(device)}`),
      );
    } catch (e) {
      reportError(e);
    }
  };
  return (
    <>
      <Typography.Title level={3}>AI识别</Typography.Title>
      <Tabs
        items={[
          {
            key: 'types',
            label: '识别类型',
            children: (
              <Card>
                <Space className="toolbar">
                  <Button
                    type="primary"
                    disabled={!can('recognition.write')}
                    onClick={() => open()}
                  >
                    新增类型
                  </Button>
                  <Button onClick={() => reload()}>刷新</Button>
                  <Typography.Text type="secondary">
                    音频分析 · 名称和提示词由管理员定义
                  </Typography.Text>
                </Space>
                <Table
                  rowKey="scheme_id"
                  dataSource={data || []}
                  scroll={{ x: 1000 }}
                  columns={[
                    { title: '名称', dataIndex: 'name' },
                    { title: '返回内容', render: (_, s: Scheme) => outputs[s.output_mode] },
                    { title: '模型', dataIndex: 'model' },
                    {
                      title: '状态',
                      render: (_, s: Scheme) => (
                        <Tag>{!s.scheme_version ? '草稿' : s.enabled ? '已发布' : '已停用'}</Tag>
                      ),
                    },
                    { title: '版本', dataIndex: 'scheme_version' },
                    {
                      title: '操作',
                      render: (_, s: Scheme) => (
                        <Space>
                          <Button type="link" onClick={() => open(s)}>
                            编辑 / 发布
                          </Button>
                          <Button
                            type="link"
                            disabled={!s.scheme_version || busy}
                            onClick={() =>
                              mutate(
                                { ...s, enabled: !s.enabled },
                                'status',
                                s.enabled ? '停用识别类型' : '恢复识别类型',
                              )
                            }
                          >
                            {s.enabled ? '停用' : '启用'}
                          </Button>
                        </Space>
                      ),
                    },
                  ]}
                />
              </Card>
            ),
          },
          {
            key: 'device',
            label: '设备用量与上限',
            children: (
              <Card>
                <Space className="toolbar">
                  <Input
                    placeholder="设备 ID"
                    value={device}
                    onChange={(e) => setDevice(e.target.value)}
                  />
                  <Button disabled={!device} onClick={loadDevice}>
                    查询设备
                  </Button>
                </Space>
                {state && (
                  <>
                    <Alert
                      type="info"
                      message={`${state.usage.month} 已占用 ${state.usage.used} 次；分析输入 / 输出 Token：${state.usage.input_tokens} / ${state.usage.output_tokens}；未知调用 ${state.usage.unknown_calls}；合成字符 ${state.usage.tts_characters}`}
                    />
                    <Typography.Paragraph>
                      用户开关：{state.policy.enabled ? '已开启' : '已关闭'}；类型顺序：
                      {state.schemes.map((s) => s.name).join(' → ') || '未选择'}
                    </Typography.Paragraph>
                    <Form
                      key={`${device}:${state.policy.revision}`}
                      layout="vertical"
                      initialValues={state.policy}
                      onFinish={async (v) => {
                        try {
                          const next = await api<DeviceState>(
                            `/devices/${encodeURIComponent(device)}/recognition`,
                            json('PUT', {
                              allowed: v.allowed,
                              monthly_limit: v.monthly_limit,
                              expected_revision: state.policy.revision,
                              reason: v.reason,
                            }),
                          );
                          setState(next);
                        } catch (e) {
                          reportError(e);
                        }
                      }}
                    >
                      <Form.Item name="allowed" label="允许使用AI识别" valuePropName="checked">
                        <Switch />
                      </Form.Item>
                      <Form.Item
                        name="monthly_limit"
                        label="每月识别次数上限（每执行一个类型计一次）"
                        rules={[{ required: true }]}
                      >
                        <InputNumber min={0} max={1000000} />
                      </Form.Item>
                      <Form.Item
                        name="reason"
                        label="修改原因"
                        rules={[{ required: true, whitespace: true }]}
                      >
                        <Input />
                      </Form.Item>
                      <Button htmlType="submit" type="primary">
                        保存上限
                      </Button>
                    </Form>
                  </>
                )}
              </Card>
            ),
          },
          {
            key: 'records',
            label: '识别记录',
            children: (
              <Card>
                <Space className="toolbar">
                  <Input
                    placeholder="设备 ID（留空查询全部）"
                    value={device}
                    onChange={(e) => setDevice(e.target.value)}
                  />
                  <Button onClick={loadRecords}>查询最近50条</Button>
                </Space>
                <Table
                  rowKey={(v) => v.task.device_id + v.task.request_id}
                  dataSource={records}
                  columns={[
                    { title: '设备', render: (_, v: RecordRow) => v.task.device_id },
                    { title: '任务', render: (_, v: RecordRow) => v.task.request_id },
                    { title: '状态', render: (_, v: RecordRow) => v.task.status },
                    {
                      title: '结果与逐次用量',
                      render: (_, v: RecordRow) => (
                        <Button onClick={() => setSelected(v)}>查看</Button>
                      ),
                    },
                  ]}
                />
              </Card>
            ),
          },
          {
            key: 'connection',
            label: '服务配置',
            children: <ConfigPage namespace="recognition-server" embedded />,
          },
        ].filter((item) =>
          mode === 'configuration'
            ? (item.key === 'types' && canReadTypes) ||
              (item.key === 'connection' && can('config.read'))
            : item.key === 'device' || item.key === 'records',
        )}
      />
      <Drawer width={680} open={!!edit} title="AI识别类型" onClose={() => setEdit(undefined)}>
        <Form
          form={form}
          layout="vertical"
          onFinish={(v) => {
            if (!edit) return;
            const scheme = {
              ...edit,
              name: v.name,
              description: v.description,
              model: v.model,
              prompt: v.prompt,
              voice: v.voice,
              output_mode: v.outputs.length === 2 ? 'text_audio' : v.outputs[0],
              limits: {
                max_file_bytes: Math.round(v.file_mib * 1048576),
                max_audio_seconds: v.seconds,
              },
            };
            void mutate(scheme, 'save', v.reason);
          }}
        >
          <Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true }]}>
            <Input maxLength={50} />
          </Form.Item>
          <Form.Item name="description" label="用户可见说明">
            <Input maxLength={300} />
          </Form.Item>
          <Form.Item label="输入类型">
            <Input value="音频" readOnly />
          </Form.Item>
          <Form.Item name="model" label="千问模型" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="prompt" label="提示词" rules={[{ required: true, whitespace: true }]}>
            <Input.TextArea rows={5} maxLength={8000} />
          </Form.Item>
          <Form.Item
            name="outputs"
            label="返回内容"
            rules={[{ type: 'array', min: 1, required: true, message: '至少勾选一种返回内容' }]}
          >
            <Checkbox.Group
              options={[
                { label: '文字', value: 'text' },
                { label: '语音', value: 'audio' },
              ]}
            />
          </Form.Item>
          <Form.Item name="voice" label="语音音色" rules={[{ required: true }]}>
            <Select
              options={['Cherry', 'Serena', 'Ethan', 'Chelsie'].map((value) => ({
                value,
                label: value,
              }))}
            />
          </Form.Item>
          <Space align="start">
            <Form.Item name="file_mib" label="文件上限（MiB）" rules={[{ required: true }]}>
              <InputNumber min={1} max={5} />
            </Form.Item>
            <Form.Item name="seconds" label="时长上限（秒）" rules={[{ required: true }]}>
              <InputNumber min={1} max={60} />
            </Form.Item>
          </Space>
          <Form.Item name="reason" label="修改原因" rules={[{ required: true, whitespace: true }]}>
            <Input />
          </Form.Item>
          <Space>
            <Button type="primary" htmlType="submit" loading={busy}>
              保存草稿
            </Button>
            <Button
              disabled={!edit?.scheme_id || busy}
              onClick={async () => {
                if (!edit) return;
                try {
                  const reason = await form.validateFields(['reason']);
                  await mutate(edit, 'publish', reason.reason);
                } catch {
                  /* Form displays validation errors. */
                }
              }}
            >
              发布已保存草稿
            </Button>
          </Space>
          <Typography.Paragraph type="secondary">
            发布会使用服务器已保存的草稿。编辑后请先保存，再打开并发布。模型 Key
            在本页“服务配置”页签中设置。
          </Typography.Paragraph>
        </Form>
      </Drawer>
      <Drawer
        width={700}
        open={!!selected}
        title="结果与逐次模型消耗"
        onClose={() => setSelected(undefined)}
      >
        <pre className="profile-json">{JSON.stringify(selected, null, 2)}</pre>
      </Drawer>
    </>
  );
}
