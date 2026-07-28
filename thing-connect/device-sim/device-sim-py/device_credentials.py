#!/usr/bin/env python3
"""Secure local persistence for simulated device credentials."""

import json
import os
import tempfile


def get_creds_file(creds_file: str = "") -> str:
    return creds_file or os.path.join(os.path.dirname(__file__), "device_creds.json")


def credentials_are_paired(device_id: str, device_key: str) -> bool:
    return bool(device_id) == bool(device_key)


def load_saved_creds(creds_file: str = ""):
    """Return (device_id, device_key), or empty strings when unavailable."""
    path = get_creds_file(creds_file)
    try:
        with open(path, "r") as f:
            data = json.load(f)
        did = data.get("device_id", "")
        dkey = data.get("device_key", "")
        if did and dkey:
            print(f"\033[0;32m[device]\033[0m 从本地文件加载凭证 device_id={did}")
            return did, dkey
    except (FileNotFoundError, json.JSONDecodeError, KeyError, OSError):
        pass
    return "", ""


def save_creds(device_id: str, device_key: str, creds_file: str = "") -> bool:
    """Atomically save credentials with owner-only permissions."""
    path = get_creds_file(creds_file)
    tmp_path = ""
    try:
        parent = os.path.dirname(os.path.abspath(path))
        os.makedirs(parent, mode=0o700, exist_ok=True)
        with tempfile.NamedTemporaryFile("w", dir=parent, prefix=".device-creds-",
                                         delete=False) as f:
            tmp_path = f.name
            # os.fchmod is unavailable on Windows. chmod is the portable
            # best-effort fallback (Windows ultimately uses filesystem ACLs).
            fchmod = getattr(os, "fchmod", None)
            if callable(fchmod):
                fchmod(f.fileno(), 0o600)
            else:
                os.chmod(tmp_path, 0o600)
            json.dump({"device_id": device_id, "device_key": device_key}, f, indent=2)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp_path, path)
        os.chmod(path, 0o600)
        print(f"\033[0;32m[device]\033[0m 凭证已保存到 {path}")
        return True
    except OSError as e:
        print(f"\033[1;33m[device]\033[0m 保存凭证失败: {e}")
        return False
    finally:
        if tmp_path:
            try:
                os.unlink(tmp_path)
            except FileNotFoundError:
                pass
