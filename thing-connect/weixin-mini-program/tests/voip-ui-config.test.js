const assert = require('node:assert/strict')
const test = require('node:test')

const {
  CALL_DIRECTION,
  buildVoipUIConfig,
  phoneDisplaySnapshot,
} = require('../utils/voip-ui-config')
const {
  buildVideoUIConfig,
  incomingDeviceID,
} = require('../utils/voip-video-profile')
const { parseIncomingQuery } = require('../utils/voip-incoming-query')

test('设备呼小程序：设备配置用于 caller，手机 listener 保持 0 度', () => {
  const deviceUI = buildVideoUIConfig({
    camera_rotation: 270,
    aspect_ratio: 0.75,
    hor_mirror: false,
    vert_mirror: true,
    object_fit: 'contain',
  })

  assert.deepEqual(
    buildVoipUIConfig(
      CALL_DIRECTION.DEVICE_TO_MINI_PROGRAM,
      deviceUI,
      874 / 402,
    ),
    {
      callerUI: {
        cameraRotation: 270,
        aspectRatio: 874 / 402,
        horMirror: false,
        vertMirror: true,
        objectFit: 'contain',
      },
      listenerUI: {
        cameraRotation: 0,
        aspectRatio: 874 / 402,
        horMirror: false,
        vertMirror: true,
        objectFit: 'contain',
      },
    },
  )
})

test('设备呼小程序：fill 保留设备 profile 比例，不套用手机比例', () => {
  const deviceUI = buildVideoUIConfig({
    camera_rotation: 90,
    aspect_ratio: 4 / 3,
    object_fit: 'fill',
  })

  assert.deepEqual(
    buildVoipUIConfig(
      CALL_DIRECTION.DEVICE_TO_MINI_PROGRAM,
      deviceUI,
      874 / 402,
    ),
    {
      callerUI: {
        cameraRotation: 90,
        aspectRatio: 4 / 3,
        objectFit: 'fill',
      },
      listenerUI: {
        cameraRotation: 0,
        aspectRatio: 4 / 3,
        objectFit: 'fill',
      },
    },
  )
})

test('设备呼小程序：无法取得手机比例时 contain 保留 profile 比例', () => {
  const deviceUI = buildVideoUIConfig({
    aspect_ratio: 0.75,
    object_fit: 'contain',
  })

  assert.deepEqual(
    buildVoipUIConfig(
      CALL_DIRECTION.DEVICE_TO_MINI_PROGRAM,
      deviceUI,
      null,
    ),
    {
      callerUI: {
        aspectRatio: 0.75,
        objectFit: 'contain',
      },
      listenerUI: {
        cameraRotation: 0,
        aspectRatio: 0.75,
        objectFit: 'contain',
      },
    },
  )
})

test('小程序呼设备：设备配置只控制 listener，手机 caller 保持 0 度', () => {
  const deviceUI = buildVideoUIConfig({
    camera_rotation: 270,
    aspect_ratio: 0.75,
    hor_mirror: true,
    vert_mirror: false,
    object_fit: 'contain',
  })

  assert.deepEqual(
    buildVoipUIConfig(
      CALL_DIRECTION.MINI_PROGRAM_TO_DEVICE,
      deviceUI,
      720 / 1280,
    ),
    {
      callerUI: {
        cameraRotation: 0,
        aspectRatio: 720 / 1280,
      },
      listenerUI: {
        aspectRatio: 720 / 1280,
        cameraRotation: 270,
        horMirror: true,
        vertMirror: false,
        objectFit: 'contain',
      },
    },
  )
})

test('小程序呼设备：没有手机比例时不错误使用设备素材比例', () => {
  const deviceUI = buildVideoUIConfig({
    camera_rotation: 90,
    aspect_ratio: 4 / 3,
    object_fit: 'fill',
  })

  assert.deepEqual(
    buildVoipUIConfig(
      CALL_DIRECTION.MINI_PROGRAM_TO_DEVICE,
      deviceUI,
      undefined,
    ),
    {
      callerUI: { cameraRotation: 0 },
      listenerUI: {
        cameraRotation: 90,
        objectFit: 'fill',
      },
    },
  )
})

test('profile 优先使用 query，并按字段回退到缓存', () => {
  assert.deepEqual(
    buildVideoUIConfig(
      {
        camera_rotation: '270',
        hor_mirror: 'false',
        object_fit: 'contain',
      },
      {
        aspect_ratio: 0.75,
        vert_mirror: true,
        object_fit: 'fill',
      },
    ),
    {
      cameraRotation: 270,
      aspectRatio: 0.75,
      horMirror: false,
      vertMirror: true,
      objectFit: 'contain',
    },
  )
})

test('空旋转值保持未配置，不会被误判为 0 度', () => {
  assert.deepEqual(buildVideoUIConfig({ camera_rotation: null }), {})
  assert.deepEqual(buildVideoUIConfig({ camera_rotation: '' }), {})
  assert.deepEqual(buildVideoUIConfig({ camera_rotation: '  ' }), {})
  assert.deepEqual(buildVideoUIConfig({ camera_rotation: 0 }), { cameraRotation: 0 })
  assert.deepEqual(buildVideoUIConfig({ camera_rotation: '0' }), { cameraRotation: 0 })
})

test('入呼设备 ID 支持微信 callerId 和已有字段', () => {
  assert.equal(incomingDeviceID({ callerId: 'device-from-wechat' }), 'device-from-wechat')
  assert.equal(incomingDeviceID({ caller_id: 'device-snake' }), 'device-snake')
  assert.equal(incomingDeviceID({ device_id: 'preferred', callerId: 'fallback' }), 'preferred')
  assert.equal(incomingDeviceID({ deviceId: 'camel' }), 'camel')
  assert.equal(incomingDeviceID({ sn: 'legacy' }), 'legacy')
})

test('入呼字符串 query 可展开微信原样传递的 unicode 分隔符', () => {
  assert.deepEqual(
    parseIncomingQuery({
      query: 'callerId=device-1\\u0026aspect_ratio=0.75\\u0026object_fit=contain',
    }, 'test'),
    {
      callerId: 'device-1',
      aspect_ratio: '0.75',
      object_fit: 'contain',
    },
  )
})

test('入呼对象 query 可从首个字段值展开后续参数', () => {
  assert.deepEqual(
    parseIncomingQuery({
      query: {
        aspect_ratio: '0.75\\u0026camera_rotation=270&object_fit=contain',
        hor_mirror: false,
      },
    }, 'test'),
    {
      aspect_ratio: '0.75',
      camera_rotation: '270',
      object_fit: 'contain',
      hor_mirror: false,
    },
  )
})

test('手机显示快照优先使用 screen 尺寸并计算纵横比', () => {
  const snapshot = phoneDisplaySnapshot({
    getWindowInfo() {
      return {
        screenWidth: 402,
        screenHeight: 874,
        windowWidth: 402,
        windowHeight: 780,
        pixelRatio: 3,
      }
    },
  })

  assert.deepEqual(snapshot, {
    screenWidth: 402,
    screenHeight: 874,
    windowWidth: 402,
    windowHeight: 780,
    pixelRatio: 3,
    recommendedContainAspectRatio: 874 / 402,
  })
})

test('未知呼叫方向会明确失败', () => {
  assert.throws(
    () => buildVoipUIConfig('unknown', {}, 1),
    /不支持的 VoIP 呼叫方向/,
  )
})
