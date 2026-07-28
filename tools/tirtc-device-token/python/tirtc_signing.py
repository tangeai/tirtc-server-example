"""
TGV1-HMAC-SHA256 request signing for tange.ai TiRTC server APIs.

Reference: https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg

Usage:
    from tirtc_signing import sign_request
    headers = sign_request(
        access_key="your-access-key",
        access_secret="your-access-secret",
        app_id="your-app-id",
        method="POST",
        uri_path="/v2/device/info",
        body='{"device_id":"TEST"}',
    )
    # headers["Authorization"] contains the signed token
"""

import hashlib
import hmac
from datetime import datetime, timedelta, timezone
from typing import Dict, Optional

TGV1_ALG = "TGV1-HMAC-SHA256"
CREDENTIAL_SCOPE_SUFFIX = "tgv1_request"


def _sha256_hex(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _hmac_sha256(data: str, key: bytes) -> bytes:
    return hmac.new(key, data.encode("utf-8"), hashlib.sha256).digest()


def _hmac_sha256_hex(data: str, key: bytes) -> str:
    return hmac.new(key, data.encode("utf-8"), hashlib.sha256).hexdigest()


def _build_credential_scope(signing_time: datetime) -> str:
    return (signing_time + timedelta(days=7)).strftime("%Y%m%d") + "/" + CREDENTIAL_SCOPE_SUFFIX


def _canonical_uri_path(path: str) -> str:
    path = path.strip()
    if len(path) > 1 and path.endswith("/"):
        path = path.rstrip("/")
    return path


def _canonical_query(method: str, raw_query: str) -> str:
    if method.upper() in ("POST", "PUT", "PATCH"):
        return ""
    raw_query = raw_query.removeprefix("?")
    return raw_query.replace("+", "%20")


def sign_request(
    access_key: str,
    access_secret: str,
    app_id: str,
    method: str,
    uri_path: str,
    body: str = "",
    raw_query: str = "",
    signing_time: Optional[datetime] = None,
) -> Dict[str, str]:
    """Build TGV1-HMAC-SHA256 signed HTTP headers.

    Args:
        access_key: Appears in the Authorization Credential field.
        access_secret: Used for HMAC key derivation (keep this secret).
        app_id: TiRTC application ID (X-Tg-App-Id header).
        method: HTTP method (GET, POST, PUT, PATCH, DELETE).
        uri_path: URI path, e.g. "/v2/device/info".
        body: Request body string (empty for GET/DELETE).
        raw_query: Raw query without leading "?" (ignored for POST/PUT/PATCH).
        signing_time: UTC datetime; uses now() if None.

    Returns:
        Dict of HTTP header name to value.
    """
    method = method.upper()
    if signing_time is None:
        signing_time = datetime.now(timezone.utc)
    else:
        # Ensure UTC
        signing_time = signing_time.astimezone(timezone.utc)

    body_bytes = body.encode("utf-8")
    tg_date = signing_time.strftime("%Y%m%dT%H%M%SZ")
    scope = _build_credential_scope(signing_time)
    payload_hash = _sha256_hex(body_bytes)

    # Step 1: build header values map
    hv: Dict[str, str] = {
        "x-tg-algorithm": TGV1_ALG,
        "x-tg-date": tg_date,
        "x-tg-app-id": app_id.strip(),
        "x-tg-content-sha256": payload_hash,
    }
    if method in ("POST", "PUT", "PATCH"):
        hv["content-type"] = "application/json"
        hv["content-length"] = str(len(body_bytes))

    # Step 2: sorted lowercased header names -> signed headers
    names = sorted(k.lower() for k in hv)
    signed_headers = ";".join(names)

    # Step 3: canonical header string
    canon_lines = [f"{name}:{hv[name].strip()}" for name in names]
    h_canon = "\n".join(canon_lines)

    # Step 4: canonical request
    uri_p = _canonical_uri_path(uri_path)
    q_canon = _canonical_query(method, raw_query)
    canonical_req = "\n".join([method, uri_p, q_canon, h_canon, signed_headers, payload_hash])

    # Step 5: string-to-sign
    hash_canon = _sha256_hex(canonical_req.encode("utf-8"))
    str_to_sign = "\n".join([TGV1_ALG, tg_date, scope, hash_canon])

    # Step 6: derive signing key
    k = _hmac_sha256(signing_time.strftime("%Y%m%d"), ("TGV1" + access_secret).encode("utf-8"))
    k = _hmac_sha256(uri_p, k)
    k = _hmac_sha256(CREDENTIAL_SCOPE_SUFFIX, k)

    # Step 7: compute signature
    sig = _hmac_sha256_hex(str_to_sign, k)

    # Step 8: build Authorization header
    cred = access_key + "/" + scope
    auth = f"{TGV1_ALG} Credential={cred}, SignedHeaders={signed_headers}, Signature={sig}"

    # Build output
    result: Dict[str, str] = {
        "X-Tg-Algorithm": hv["x-tg-algorithm"],
        "X-Tg-Date": hv["x-tg-date"],
        "X-Tg-App-Id": hv["x-tg-app-id"],
        "X-Tg-Content-Sha256": hv["x-tg-content-sha256"],
        "X-Tg-Signed-Headers": signed_headers,
        "Authorization": auth,
    }
    if "content-type" in hv:
        result["Content-Type"] = hv["content-type"]
    if "content-length" in hv:
        result["Content-Length"] = hv["content-length"]
    return result
