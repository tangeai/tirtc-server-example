(function (root) {
  "use strict";
  const WebI18n = root.WebI18n || { t: (key, params = {}) => key.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? "")) };
  const states = {
    get assigned() { return WebI18n.t("已安排加入"); },
    get waiting_device() { return WebI18n.t("设备离线"); },
    get suspended() { return WebI18n.t("设备忙，空闲后加入"); },
    get connecting() { return WebI18n.t("正在加入"); },
    get joined() { return WebI18n.t("已加入"); },
    get connect_failed() { return WebI18n.t("加入失败，将重试"); },
    get left() { return WebI18n.t("已退出"); },
    get room_closed() { return WebI18n.t("房间已解散"); },
  };
  function roomSummary(room) {
    if (!room || room.desired_state !== "joined")
      return room?.state === "room_closed"
        ? WebI18n.t("房间已解散，可重新建房或加入")
        : WebI18n.t("当前未加入房间");
    const code = room.room_code || WebI18n.t("正在同步房间号");
    if (room.state === "waiting_device") return code;
    return room.state === "joined"
      ? WebI18n.t("{p0} · {p1} 人在线", {p0: code, p1: room.online_count ?? 0})
      : `${code} · ${states[room.state] || WebI18n.t("正在同步")}`;
  }
  function roleSummary(binding, role) {
    if (!binding?.role_id) {
      if (!binding?.default_role_id) return WebI18n.t("未配置默认角色");
      return role?.name ? WebI18n.t("{p0}（默认）", {p0: role.name}) : WebI18n.t("默认角色不可用");
    }
    return role?.name ? WebI18n.t("当前：{p0}", {p0: role.name}) : WebI18n.t("角色不可用，请重新选择");
  }
  function codecList(value) {
    if (value === undefined || value === null) return WebI18n.t("未上报");
    const values = Array.isArray(value)
      ? value
      : String(value).split(/[,/;|\s]+/);
    const names = {
      opus: "Opus",
      aac: "AAC",
      get none() { return WebI18n.t("无"); },
      amr: "AMR",
      amr_nb: "AMR-NB",
      amr_wb: "AMR-WB",
      pcm: "PCM",
      alaw: "G.711 A-law",
      g711a: "G.711 A-law",
      h264: "H.264",
      h265: "H.265",
      mjpeg: "MJPEG",
    };
    return (
      values
        .filter(Boolean)
        .map((v) => names[String(v).toLowerCase()] || String(v))
        .join(" / ") || WebI18n.t("无")
    );
  }
  function mediaRows(profiles, scene) {
    const p = profiles?.[scene];
    if (!p || !Object.keys(p).length) return null;
    const display = (key, format = String) =>
      p[key] == null ? WebI18n.t("未上报") : format(p[key]);
    const video = (key) => (p.no_video === true ? WebI18n.t("无") : codecList(p[key]));
    return [
      [WebI18n.t("上行音频"), codecList(p.up_audio_mt)],
      [WebI18n.t("上行视频"), video("up_video_mt")],
      [WebI18n.t("下行音频（首选顺序）"), codecList(p.down_audio_mt)],
      [WebI18n.t("下行视频（首选顺序）"), video("down_video_mt")],
      ...(scene === "stream"
        ? [
            [WebI18n.t("上行音频 Stream ID"), display("up_audio_streamid")],
            [WebI18n.t("上行视频 Stream ID"), display("up_video_streamid")],
            [WebI18n.t("下行音频 Stream ID"), display("down_audio_streamid")],
            [WebI18n.t("下行视频 Stream ID"), display("down_video_streamid")],
          ]
        : []),
      [WebI18n.t("音频采样率"), display("audio_rate", (v) => `${v} Hz`)],
      [WebI18n.t("音频声道数"), display("audio_channels")],
      [WebI18n.t("摄像头旋转"), display("camera_rotation", (v) => WebI18n.t("{p0}°（顺时针）", {p0: v}))],
      ...(scene === "voip"
        ? [
            [
              WebI18n.t("下行视频方向"),
              ({ get 0() { return WebI18n.t("默认"); }, get 1() { return WebI18n.t("正向画面"); }, get 2() { return WebI18n.t("保留旋转画面"); } })[
                Number(p.down_video_rotation ?? 0)
              ] || WebI18n.t("默认"),
            ],
          ]
        : []),
      [WebI18n.t("水平镜像"), display("hor_mirror", (v) => (v ? WebI18n.t("是") : WebI18n.t("否")))],
      [WebI18n.t("垂直镜像"), display("vert_mirror", (v) => (v ? WebI18n.t("是") : WebI18n.t("否")))],
      [WebI18n.t("画面比例"), display("aspect_ratio")],
      [
        WebI18n.t("缩放方式"),
        display(
          "object_fit",
          (v) =>
            ({
              get fill() { return WebI18n.t("拉伸填充"); },
              get contain() { return WebI18n.t("保持比例，完整显示"); },
              get cover() { return WebI18n.t("保持比例，裁剪填充"); },
            })[v] || v,
        ),
      ],
    ];
  }
  function deviceRows(device) {
    return [
      [WebI18n.t("设备名称"), device?.device_name || WebI18n.t("未命名设备")],
      [WebI18n.t("设备 ID"), device?.device_id || WebI18n.t("未上报")],
      ["MAC", device?.mac || WebI18n.t("未上报")],
      [WebI18n.t("芯片型号"), device?.hardware?.chip_model || WebI18n.t("未上报")],
      [WebI18n.t("开发板型号"), device?.hardware?.board_model || WebI18n.t("未上报")],
      [WebI18n.t("固件版本"), device?.firmware_version || WebI18n.t("未上报")],
      [WebI18n.t("绑定时间"), device?.bind_time || WebI18n.t("未上报")],
    ];
  }
  const value = { states, roomSummary, roleSummary, mediaRows, deviceRows };
  if (typeof module !== "undefined" && module.exports) module.exports = value;
  else root.DevicePresentation = value;
})(typeof window !== "undefined" ? window : globalThis);
