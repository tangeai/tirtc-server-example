#!/usr/bin/env python3
"""通话类型解析规则。"""


class CallTypeError(ValueError):
    """通话类型无效或与当前媒体能力不匹配。"""


def resolve_call_type(call_type: str | None, video_capable: bool,
                      subject: str = "通话") -> str:
    """返回 audio/video；省略时按当前设备是否具备上行视频能力选择。"""
    if call_type is None or not str(call_type).strip():
        return "video" if video_capable else "audio"

    normalized = str(call_type).strip().lower()
    if normalized not in ("video", "audio"):
        raise CallTypeError(f"{subject}类型仅支持 video 或 audio")
    if normalized == "video" and not video_capable:
        raise CallTypeError(f"未配置上行视频文件，不能发起视频{subject}")
    return normalized
