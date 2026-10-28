"""Device audio recognition over HTTP; explicit commands, no background playback."""
from collections import OrderedDict
import io
import json
from pathlib import Path
import re
import sys
import tempfile
from urllib.parse import urlsplit
import uuid
import wave

import requests

_ID = re.compile(r"^[A-Za-z0-9_-]{1,64}$")


class AudioRecognition:
    def __init__(self, server, token, output_dir, device_id="", capture=None, audio_ready=None):
        self.audio_ready = audio_ready
        self.capture = capture
        self.device_id = device_id
        self._revisions = OrderedDict()
        self.server = server.rstrip("/")
        self.token = token
        self.output_dir = Path(output_dir) / "recognition"

    def on_notification(self, message):
        """Consume MQTT snapshots without HTTP work or media playback in callbacks."""
        if not isinstance(message, dict) or message.get("channel") != "recognition":
            return
        envelope = message.get("payload")
        if not isinstance(envelope, dict) or envelope.get("code") != 200:
            return
        if message.get("type") == "recognition_config_changed":
            print("[recognition] 识别配置已更新；可用 recognize config 查看，上传时自动读取最新配置", flush=True)
            return
        if message.get("type") != "recognition_result":
            return
        task = envelope.get("data")
        if not isinstance(task, dict):
            return
        request_id, revision = task.get("request_id"), task.get("status_revision")
        if (not isinstance(request_id, str) or not _ID.fullmatch(request_id)
                or type(revision) is not int or revision < 1
                or not isinstance(task.get("results"), list)
                or (self.device_id and task.get("device_id") != self.device_id)):
            return
        if revision <= self._revisions.get(request_id, 0):
            return
        self._revisions[request_id] = revision
        self._revisions.move_to_end(request_id)
        if len(self._revisions) > 256:
            self._revisions.popitem(last=False)
        print("[recognition] 异步识别结果 " + json.dumps(task, ensure_ascii=False, indent=2), flush=True)
        if any(isinstance(item, dict) and isinstance(item.get("speech"), dict)
               and item["speech"].get("status") == "ready" for item in task["results"]):
            print(f"[recognition] 语音已就绪，下载：recognize audio {request_id}", flush=True)
            if self.audio_ready is not None:
                for item in task["results"]:
                    if (isinstance(item, dict) and isinstance(item.get("speech"), dict)
                            and item["speech"].get("status") == "ready"
                            and isinstance(item.get("item_id"), str) and _ID.fullmatch(item["item_id"])):
                        self.audio_ready(request_id, item["item_id"])


    def _request(self, method, path, **kwargs):
        parsed = urlsplit(self.server)
        if parsed.scheme not in ("http", "https") or not parsed.netloc or parsed.username or parsed.query or parsed.fragment:
            raise ValueError("识别服务地址缺失或无效，请检查 --endpoint 对应的 /services 是否包含 recognition-srv")
        try:
            return requests.request(method, self.server + "/v1/recognition" + path,
                                    headers={"Authorization": "Bearer " + self.token},
                                    timeout=(5, 30), allow_redirects=False, **kwargs)
        except requests.RequestException:
            raise ValueError("识别请求连接失败或超时；上传结果不确定时先查询原任务号，不要重复上传") from None

    def _json(self, method, path, **kwargs):
        with self._request(method, path, **kwargs) as response:
            try:
                body = response.json()
            except (ValueError, requests.RequestException):
                raise ValueError("识别服务未返回 JSON，请检查服务地址和代理路由") from None
            if not isinstance(body, dict) or response.status_code not in (200, 202) or body.get("code") != 200:
                code = body.get("code") if isinstance(body, dict) else response.status_code
                messages = {401: "设备凭证失效，请重新启动模拟器", 40300: "设备未绑定或无权访问", 40901: "识别未开启或配置已变更，请检查设备设置", 429: "本月识别额度已用完", 50300: "模型 Key 尚未配置", 40000: "音频或请求参数不符合要求"}
                raise ValueError(messages.get(code, "识别请求失败") + f"（code={code}）")
            return body["data"]

    @staticmethod
    def _id(value):
        if not _ID.fullmatch(value):
            raise ValueError("任务号或子项标识无效")
        return value

    def upload(self, filename, sample_rate=16000, channels=1, audio_format=None):
        state = self._json("GET", "/config")
        if not state.get("effective_enabled"):
            raise ValueError("设备 AI识别不可用，请先在用户设备列表开启并选择已发布类型，检查额度与后台配置")
        limits = state["upload_limits"]
        maximum = min(int(limits["max_file_bytes"]), 5 << 20)
        try:
            with Path(filename).open("rb") as source:
                data = source.read(maximum + 1)
        except FileNotFoundError:
            raise ValueError("音频文件不存在，请检查实际文件名和路径：" + filename) from None
        except IsADirectoryError:
            raise ValueError("所选路径是文件夹，请选择音频文件：" + filename) from None
        except PermissionError:
            raise ValueError("无权读取音频文件，请检查文件权限或占用情况：" + filename) from None
        except OSError:
            raise ValueError("音频文件读取失败，请检查路径和磁盘状态：" + filename) from None
        return self._upload_data(state, data, filename, sample_rate, channels, audio_format)

    def record_once(self, stop_event):
        if sys.platform != "win32":
            raise ValueError("recognize record 仅支持 Windows 麦克风采集；其他平台请上传音频文件")
        if self.capture is None or stop_event is None:
            raise ValueError("麦克风录音入口未就绪")
        state = self._json("GET", "/config")
        if not state.get("effective_enabled"):
            raise ValueError("请先在设备设置中开启 AI识别并选择已发布类型")
        limits = state["upload_limits"]
        if limits["max_audio_seconds"] < 10 or limits["max_file_bytes"] < 320044:
            raise ValueError("当前类型的上传限制不足10秒PCM录音，请调整类型限制后重试")
        if stop_event.is_set():
            raise ValueError("录音已取消，未上传")
        data = self.capture(stop_event)
        if stop_event.is_set():
            raise ValueError("录音已取消，未上传")
        if len(data) != 320000:
            raise ValueError("录音不足10秒，未上传")
        print("[recognition] 录音完成，正在上传一次识别任务", flush=True)
        return self._upload_data(state, data, "recording.pcm", 16000, 1, "pcm_s16le")

    def _upload_data(self, state, data, filename, sample_rate, channels, audio_format):
        limits = state["upload_limits"]
        maximum = min(int(limits["max_file_bytes"]), 5 << 20)
        if len(data) > maximum:
            raise ValueError("音频文件超过设备上传上限")
        audio_format = audio_format or ("pcm_s16le" if Path(filename).suffix.lower() == ".pcm" else "wav")
        fields = {"audio_format": audio_format}
        if audio_format == "pcm_s16le":
            if not 8000 <= sample_rate <= 48000 or channels not in (1, 2):
                raise ValueError("PCM 采样率须为 8000–48000，声道须为 1 或 2")
            duration = len(data) / (sample_rate * channels * 2)
            valid = (len(data) % (channels * 2) == 0 and len(data) + 44 <= maximum
                     and 1 <= duration <= min(60, limits["max_audio_seconds"]))
            fields.update(sample_rate=str(sample_rate), channels=str(channels))
            upload_name, mime = "audio.pcm", "application/octet-stream"
        else:
            try:
                with wave.open(io.BytesIO(data), "rb") as audio:
                    duration = audio.getnframes() / audio.getframerate()
                    valid = (audio.getsampwidth() == 2 and audio.getcomptype() == "NONE"
                             and audio.getnchannels() in (1, 2) and 8000 <= audio.getframerate() <= 48000
                             and 1 <= duration <= min(60, limits["max_audio_seconds"]))
            except (wave.Error, EOFError, ZeroDivisionError):
                valid = False
            upload_name, mime = "audio.wav", "audio/wav"
        if not valid:
            raise ValueError("请使用符合大小和时长限制的 PCM S16LE 裸数据或 PCM16 WAV（8–48kHz，单/双声道）")
        request_id = "sim_" + uuid.uuid4().hex
        print(f"[recognition] 任务号 {request_id}；如上传超时，用 recognize status {request_id} 补查", flush=True)
        return self._json("POST", "/requests", data={"request_id": request_id,
                          "config_revision": str(state["policy"]["revision"]), "media_type": "audio", **fields},
                          files={"files": (upload_name, data, mime)})

    def status(self, request_id):
        return self._json("GET", "/requests/" + self._id(request_id))

    def download(self, request_id, item_id=None):
        task = self.status(request_id)
        saved = []
        for item in task["results"]:
            if item_id is not None and item.get("item_id") != item_id:
                continue
            if item.get("speech", {}).get("status") != "ready":
                continue
            current_item_id = self._id(item["item_id"])
            # Construct the authenticated local route; never follow an upstream URL.
            path = f"/requests/{self._id(request_id)}/audio/{current_item_id}"
            with self._request("GET", path, stream=True) as response:
                if response.status_code != 200:
                    raise ValueError("语音下载失败，请重新查询任务")
                data = bytearray()
                for chunk in response.iter_content(65536):
                    data.extend(chunk)
                    if len(data) > 10 << 20:
                        raise ValueError("返回语音超过大小上限")
            if len(data) < 44 or data[:4] != b"RIFF" or data[8:12] != b"WAVE":
                raise ValueError("返回的语音不是 WAV 文件")
            self.output_dir.mkdir(parents=True, exist_ok=True)
            destination = self.output_dir / f"{request_id}_{current_item_id}.wav"
            with tempfile.NamedTemporaryFile(dir=self.output_dir, delete=False) as output:
                temporary = Path(output.name)
                try:
                    output.write(data)
                    output.close()
                    temporary.replace(destination)
                finally:
                    temporary.unlink(missing_ok=True)
            saved.append(str(destination))
        return saved

    def command(self, line, stop_event=None):
        # Preserve Windows backslashes and spaces without shell parsing.
        action, _, argument = line.strip().partition(" ")
        argument = argument.strip()
        try:
            if action == "record":
                if argument:
                    raise ValueError("用法：recognize record（固定录制10秒）")
                result = self.record_once(stop_event)
            elif action == "config":
                result = self._json("GET", "/config")
            elif action == "pcm":
                parts = argument.split(maxsplit=2)
                if len(parts) != 3 or not parts[0].isdigit() or not parts[1].isdigit():
                    raise ValueError('用法：recognize pcm 16000 1 "文件路径"')
                result = self.upload(parts[2].strip('"'), sample_rate=int(parts[0]),
                                     channels=int(parts[1]), audio_format="pcm_s16le")
            elif action == "status":
                result = self.status(argument)
            elif action == "audio":
                result = self.download(argument)
                if not result:
                    print("[recognition] 暂无就绪语音，请查询 speech.status", flush=True)
            else:
                filename = line.strip().strip('"')
                if not filename:
                    raise ValueError("用法：recognize 文件.pcm/文件.wav | pcm 16000 1 文件路径 | config | status 任务号 | audio 任务号")
                result = self.upload(filename)
            print("[recognition] " + json.dumps(result, ensure_ascii=False, indent=2), flush=True)
        except (ValueError, OSError, KeyError, TypeError, requests.RequestException):
            # Network exceptions can contain credentials/URLs; never dump them.
            import sys
            error = sys.exc_info()[1]
            if type(error) is ValueError:
                message = str(error)
            elif isinstance(error, requests.RequestException):
                message = "识别请求网络中断；已有任务号时请先查询原任务"
            elif isinstance(error, OSError):
                message = "语音文件保存失败，请检查输出目录权限和磁盘空间"
            else:
                message = "识别服务响应格式不正确，请检查服务端与模拟器版本"
            print("[recognition] " + message, flush=True)
