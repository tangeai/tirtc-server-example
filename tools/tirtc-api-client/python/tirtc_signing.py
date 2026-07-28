"""
TGV1-HMAC-SHA256 request signing for tange.ai server APIs.

Usage:
    from tirtc_signing import sign_request

    headers = sign_request(
        access_key="...", access_secret="...", app_id="...",
        method="POST", uri_path="/v2/user/login/user-id",
        body='{"user_id":"test"}',
    )
"""

import hashlib
import hmac
from datetime import datetime, timedelta, timezone
from typing import Optional


ALG_TGV1 = "TGV1-HMAC-SHA256"


def sign_request(
    access_key: str,
    access_secret: str,
    app_id: str,
    method: str,
    uri_path: str,
    raw_query: str = "",
    body: Optional[str] = None,
    signing_time: Optional[datetime] = None,
) -> dict[str, str]:
    """Build signed HTTP headers for a tange.ai OpenAPI request."""
    method = method.upper()
    if signing_time is None:
        signing_time = datetime.now(timezone.utc)
    now = signing_time
    body_str = body or ""
    payload_hash = _sha256_hex(body_str)

    # Step 1: build header map
    hv = {
        "X-Tg-Algorithm": ALG_TGV1,
        "X-Tg-Date": now.strftime("%Y%m%dT%H%M%SZ"),
        "X-Tg-App-Id": app_id.strip(),
        "X-Tg-Content-Sha256": payload_hash,
    }
    # Signed headers: only x-tg-app-id, x-tg-date (+ content-type, content-length for body methods)
    signed_names = ["X-Tg-App-Id", "X-Tg-Date"]
    if method in ("POST", "PUT", "PATCH"):
        hv["Content-Type"] = "application/json"
        hv["Content-Length"] = str(len(body_str.encode("utf-8")))
        signed_names = ["Content-Length", "Content-Type"] + signed_names

    # Step 2: sorted signed header names
    signed_hdrs = ";".join(k.lower() for k in signed_names)
    lines = [f"{k.lower()}:{hv[k]}" for k in signed_names]
    canon_hdrs = "\n".join(lines)

    # Step 4: canonical request
    uri = _canonical_uri(uri_path)
    q_canon = _canonical_query(method, raw_query.lstrip("?"))
    canon_req = "\n".join([method, uri, q_canon, canon_hdrs, signed_hdrs, payload_hash])

    # Step 5: string to sign
    scope_date = (now + timedelta(days=7)).strftime("%Y%m%d")
    scope = f"{scope_date}/tgv1_request"
    str_to_sign = "\n".join([ALG_TGV1, hv["X-Tg-Date"], scope, _sha256_hex(canon_req)])

    # Step 6: derive signing key
    k = _hmac_sha256(now.strftime("%Y%m%d"), f"TGV1{access_secret}".encode())
    k = _hmac_sha256(uri, k)
    k = _hmac_sha256("tgv1_request", k)

    # Step 7: signature
    sig = _hmac_sha256_hex(str_to_sign, k)

    # Step 8: Authorization
    auth = f"{ALG_TGV1} Credential={access_key}/{scope}, SignedHeaders={signed_hdrs}, Signature={sig}"

    # Build output
    out = {
        "X-Tg-Algorithm": hv["X-Tg-Algorithm"],
        "X-Tg-Date": hv["X-Tg-Date"],
        "X-Tg-App-Id": hv["X-Tg-App-Id"],
        "X-Tg-Content-Sha256": hv["X-Tg-Content-Sha256"],
        "X-Tg-Signed-Headers": signed_hdrs,
        "Authorization": auth,
    }
    if "Content-Type" in hv:
        out["Content-Type"] = hv["Content-Type"]
    if "Content-Length" in hv:
        out["Content-Length"] = hv["Content-Length"]
    return out


def _sha256_hex(data: str) -> str:
    return hashlib.sha256(data.encode("utf-8")).hexdigest()


def _hmac_sha256(data: str, key: bytes) -> bytes:
    return hmac.new(key, data.encode("utf-8"), hashlib.sha256).digest()


def _hmac_sha256_hex(data: str, key: bytes) -> str:
    return hmac.new(key, data.encode("utf-8"), hashlib.sha256).hexdigest()


def _canonical_uri(p: str) -> str:
    p = p.strip()
    if len(p) > 1 and p.endswith("/"):
        p = p[:-1]
    return p


def _canonical_query(method: str, raw_query: str) -> str:
    if method in ("POST", "PUT", "PATCH"):
        return ""
    return raw_query.replace("+", "%20")
