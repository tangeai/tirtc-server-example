const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const html = fs.readFileSync(path.join(__dirname, '../static/player.html'), 'utf8');
const script = html.match(/<script type="module">([\s\S]*?)<\/script>/)[1]
  .replace(/^import .*;$/m, '');

async function connectWithProfile(profile) {
  const calls = {};
  const elements = new Map();
  const output = () => ({ attach() {}, detach() {} });
  const context = {
    window: { MiniProgramPage: { token: 'test-token' }, addEventListener() {} },
    document: {
      getElementById(id) {
        if (!elements.has(id)) elements.set(id, {
          style: { setProperty() {} }, classList: { toggle() {}, add() {}, remove() {} },
        });
        return elements.get(id);
      },
      addEventListener() {},
    },
    location: { search: '?device_id=test-device' },
    URLSearchParams, AbortController,
    setTimeout() { return 1; }, clearTimeout() {},
    fetch: async () => ({ json: async () => ({ code: 200, data: {
      token: 'rtc-token', app_id: 'app', profiles: profile && { stream: profile },
    } }) }),
    TiRtc: { initialize() {}, async videoOutputReady() {} },
    TiRtcInitOptions: options => options,
    TiRtcConn: class {
      async connect() {}
      disconnect() {}
      subscribeAudio({ streamId }) { calls.subscribeAudio = streamId; }
      subscribeVideo({ streamId }) { calls.subscribeVideo = streamId; }
      unsubscribeAudio({ streamId }) { calls.unsubscribeAudio = streamId; }
      unsubscribeVideo({ streamId }) { calls.unsubscribeVideo = streamId; }
    },
    TiRtcAudioOutput({ streamId }) { calls.audioOutput = streamId; return output(); },
    TiRtcVideoOutput({ streamId }) { calls.videoOutput = streamId; return output(); },
    TiRtcAudioInput: class {
      constructor({ streamId }) { calls.audioInput = streamId; }
      setOptions() {}
      stop() {}
      async detach() {}
    },
  };
  vm.runInNewContext(script, context);
  await new Promise(resolve => setImmediate(resolve));
  return calls;
}

test('player subscribes to device streams and sends to its downlink, preserving zero', async () => {
  assert.deepEqual(await connectWithProfile({
    up_audio_streamid: 0, up_video_streamid: 7,
    down_audio_streamid: 8, down_video_streamid: 15,
  }), { audioOutput: 0, videoOutput: 7, audioInput: 8, subscribeAudio: 0, subscribeVideo: 7 });
});

test('old devices and invalid stream IDs retain existing player defaults', async () => {
  for (const profile of [undefined, {}, {
    up_audio_streamid: '0', up_video_streamid: 16, down_audio_streamid: -1,
  }]) {
    assert.deepEqual(await connectWithProfile(profile), {
      audioOutput: 10, videoOutput: 11, audioInput: 14, subscribeAudio: 10, subscribeVideo: 11,
    });
  }
});

test('partially reported stream IDs fall back independently', async () => {
  assert.deepEqual(await connectWithProfile({ up_video_streamid: 0 }), {
    audioOutput: 10, videoOutput: 0, audioInput: 14, subscribeAudio: 10, subscribeVideo: 0,
  });
});
