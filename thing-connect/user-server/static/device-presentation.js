(function (root) {
  "use strict";
  const states = {
    assigned: "已安排加入",
    waiting_device: "设备离线",
    suspended: "设备忙，空闲后加入",
    connecting: "正在加入",
    joined: "已加入",
    connect_failed: "加入失败，将重试",
    left: "已退出",
    room_closed: "房间已解散",
  };
  function roomSummary(room) {
    if (!room || room.desired_state !== "joined")
      return room?.state === "room_closed"
        ? "房间已解散，可重新建房或加入"
        : "当前未加入房间";
    const code = room.room_code || "正在同步房间号";
    if (room.state === "waiting_device") return code;
    return room.state === "joined"
      ? `${code} · ${room.online_count ?? 0} 人在线`
      : `${code} · ${states[room.state] || "正在同步"}`;
  }
  function roleSummary(binding, role) {
    if (!binding?.role_id) {
      if (!binding?.default_role_id) return "未配置默认角色";
      return role?.name ? `${role.name}（默认）` : "默认角色不可用";
    }
    return role?.name ? `当前：${role.name}` : "角色不可用，请重新选择";
  }
  function codecList(value) {
    if (value === undefined || value === null) return "未上报";
    const values = Array.isArray(value)
      ? value
      : String(value).split(/[,/;|\s]+/);
    const names = {
      opus: "Opus",
      aac: "AAC",
      none: "无",
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
        .join(" / ") || "无"
    );
  }
  function mediaRows(profiles, scene) {
    const p = profiles?.[scene];
    if (!p || !Object.keys(p).length) return null;
    const display = (key, format = String) =>
      p[key] == null ? "未上报" : format(p[key]);
    const video = (key) => (p.no_video === true ? "无" : codecList(p[key]));
    return [
      ["上行音频", codecList(p.up_audio_mt)],
      ["上行视频", video("up_video_mt")],
      ["下行音频（首选顺序）", codecList(p.down_audio_mt)],
      ["下行视频（首选顺序）", video("down_video_mt")],
      ["音频采样率", display("audio_rate", (v) => `${v} Hz`)],
      ["音频声道数", display("audio_channels")],
      ["摄像头旋转", display("camera_rotation", (v) => `${v}°（顺时针）`)],
      ...(scene === "voip"
        ? [
            [
              "下行视频方向",
              ({ 0: "默认", 1: "正向画面", 2: "保留旋转画面" })[
                Number(p.down_video_rotation ?? 0)
              ] || "默认",
            ],
          ]
        : []),
      ["水平镜像", display("hor_mirror", (v) => (v ? "是" : "否"))],
      ["垂直镜像", display("vert_mirror", (v) => (v ? "是" : "否"))],
      ["画面比例", display("aspect_ratio")],
      [
        "缩放方式",
        display(
          "object_fit",
          (v) =>
            ({
              fill: "拉伸填充",
              contain: "保持比例，完整显示",
              cover: "保持比例，裁剪填充",
            })[v] || v,
        ),
      ],
    ];
  }
  const value = { states, roomSummary, roleSummary, mediaRows };
  if (typeof module !== "undefined" && module.exports) module.exports = value;
  else root.DevicePresentation = value;
})(typeof window !== "undefined" ? window : globalThis);
