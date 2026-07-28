//! TGV1-HMAC-SHA256 request signing for tange.ai TiRTC server APIs.
//!
//! Reference: <https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg>
//!
//! Usage:
//! ```rust
//! use tirtc_signing::sign_request;
//! use std::collections::HashMap;
//!
//! let headers: HashMap<String, String> = sign_request(
//!     "your-access-key",
//!     "your-access-secret",
//!     "your-app-id",
//!     "POST",
//!     "/v2/device/info",
//!     r#"{"device_id":"TEST"}"#,
//!     "",
//!     None,
//! );
//! ```

use chrono::{DateTime, Duration, Utc};
use hex::encode as hex_encode;
use hmac::{Hmac, Mac};
use sha2::{Digest, Sha256};
use std::collections::HashMap;

type HmacSha256 = Hmac<Sha256>;

const TGV1_ALG: &str = "TGV1-HMAC-SHA256";
const CREDENTIAL_SCOPE_SUFFIX: &str = "tgv1_request";

/// Build TGV1-HMAC-SHA256 signed HTTP headers.
///
/// # Arguments
///
/// * `access_key` - Appears in the Authorization Credential field.
/// * `access_secret` - Used for HMAC key derivation (keep this secret).
/// * `app_id` - TiRTC application ID (X-Tg-App-Id header).
/// * `method` - HTTP method (GET, POST, PUT, PATCH, DELETE).
/// * `uri_path` - URI path, e.g. "/v2/device/info".
/// * `body` - Request body string (empty for GET/DELETE).
/// * `raw_query` - Raw query without leading "?" (ignored for POST/PUT/PATCH).
/// * `signing_time` - UTC DateTime; uses Utc::now() if None.
///
/// # Returns
///
/// HashMap of HTTP header name to value.
pub fn sign_request(
    access_key: &str,
    access_secret: &str,
    app_id: &str,
    method: &str,
    uri_path: &str,
    body: &str,
    raw_query: &str,
    signing_time: Option<DateTime<Utc>>,
) -> HashMap<String, String> {
    let method = method.to_uppercase();
    let signing_time = signing_time.unwrap_or_else(Utc::now);

    let tg_date = signing_time.format("%Y%m%dT%H%M%SZ").to_string();
    let scope = build_credential_scope(signing_time);
    let payload_hash = sha256_hex(body.as_bytes());

    // Step 1: build header values map
    let mut hv: HashMap<String, String> = HashMap::new();
    hv.insert("x-tg-algorithm".to_string(), TGV1_ALG.to_string());
    hv.insert("x-tg-date".to_string(), tg_date.clone());
    hv.insert("x-tg-app-id".to_string(), app_id.trim().to_string());
    hv.insert("x-tg-content-sha256".to_string(), payload_hash.clone());

    let has_body = matches!(method.as_str(), "POST" | "PUT" | "PATCH");
    if has_body {
        hv.insert("content-type".to_string(), "application/json".to_string());
        hv.insert("content-length".to_string(), body.len().to_string());
    }

    // Step 2: sorted lowercased header names
    let mut names: Vec<String> = hv.keys().map(|k| k.to_lowercase()).collect();
    names.sort();
    let signed_headers = names.join(";");

    // Step 3: canonical header string
    let canon_lines: Vec<String> = names
        .iter()
        .map(|name| format!("{}:{}", name, hv[name].trim()))
        .collect();
    let h_canon = canon_lines.join("\n");

    // Step 4: canonical request
    let uri_p = canonical_uri_path(uri_path);
    let q_canon = canonical_query(&method, raw_query);
    let canonical_req = [
        method.as_str(),
        &uri_p,
        &q_canon,
        &h_canon,
        &signed_headers,
        &payload_hash,
    ]
    .join("\n");

    // Step 5: string-to-sign
    let hash_canon = sha256_hex(canonical_req.as_bytes());
    let str_to_sign = [TGV1_ALG, &tg_date, &scope, &hash_canon].join("\n");

    // Step 6: derive signing key
    let k = hmac_sha256_bytes(
        &signing_time.format("%Y%m%d").to_string(),
        format!("TGV1{}", access_secret).as_bytes(),
    );
    let k = hmac_sha256_bytes(&uri_p, &k);
    let k = hmac_sha256_bytes(CREDENTIAL_SCOPE_SUFFIX, &k);

    // Step 7: compute signature
    let sig = hmac_sha256_hex(&str_to_sign, &k);

    // Step 8: build Authorization header
    let cred = format!("{}/{}", access_key, scope);
    let auth = format!(
        "{} Credential={}, SignedHeaders={}, Signature={}",
        TGV1_ALG, cred, signed_headers, sig
    );

    // Build output
    let mut result: HashMap<String, String> = HashMap::new();
    result.insert("X-Tg-Algorithm".to_string(), hv["x-tg-algorithm"].clone());
    result.insert("X-Tg-Date".to_string(), hv["x-tg-date"].clone());
    result.insert("X-Tg-App-Id".to_string(), hv["x-tg-app-id"].clone());
    result.insert(
        "X-Tg-Content-Sha256".to_string(),
        hv["x-tg-content-sha256"].clone(),
    );
    result.insert("X-Tg-Signed-Headers".to_string(), signed_headers);
    result.insert("Authorization".to_string(), auth);
    if has_body {
        result.insert(
            "Content-Type".to_string(),
            hv["content-type"].clone(),
        );
        result.insert(
            "Content-Length".to_string(),
            hv["content-length"].clone(),
        );
    }
    result
}

// --- helpers ---

fn sha256_hex(data: &[u8]) -> String {
    let mut hasher = Sha256::new();
    hasher.update(data);
    hex_encode(hasher.finalize())
}

fn hmac_sha256_bytes(data: &str, key: &[u8]) -> Vec<u8> {
    let mut mac = HmacSha256::new_from_slice(key).expect("HMAC can take key of any size");
    mac.update(data.as_bytes());
    mac.finalize().into_bytes().to_vec()
}

fn hmac_sha256_hex(data: &str, key: &[u8]) -> String {
    let raw = hmac_sha256_bytes(data, key);
    hex_encode(&raw)
}

fn build_credential_scope(signing_time: DateTime<Utc>) -> String {
    let future = signing_time + Duration::days(7);
    format!("{}/{}", future.format("%Y%m%d"), CREDENTIAL_SCOPE_SUFFIX)
}

fn canonical_uri_path(path: &str) -> String {
    let mut p = path.trim().to_string();
    if p.len() > 1 && p.ends_with('/') {
        p.pop();
    }
    p
}

fn canonical_query(method: &str, raw_query: &str) -> String {
    match method {
        "POST" | "PUT" | "PATCH" => String::new(),
        _ => {
            let q = raw_query.strip_prefix('?').unwrap_or(raw_query);
            q.replace('+', "%20")
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;
    use std::fs;

    fn signing() -> DateTime<Utc> {
        Utc.with_ymd_and_hms(2024, 1, 15, 12, 0, 0).unwrap()
    }

    fn load_vectors() -> serde_json::Value {
        let path = "../test-vectors.json";
        let data = fs::read_to_string(path).expect("Cannot read test-vectors.json");
        serde_json::from_str(&data).expect("Invalid JSON")
    }

    #[test]
    fn deterministic() {
        let s = signing();
        let h1 = sign_request("access123", "secret", "app456", "POST", "/v1/token/wxvoip", "{\"k\":\"v\"}", "", Some(s));
        let h2 = sign_request("access123", "secret", "app456", "POST", "/v1/token/wxvoip", "{\"k\":\"v\"}", "", Some(s));
        assert_eq!(h1["Authorization"], h2["Authorization"]);
    }

    #[test]
    fn different_secrets() {
        let s = signing();
        let h1 = sign_request("access", "secret1", "app", "POST", "/path", "body", "", Some(s));
        let h2 = sign_request("access", "secret2", "app", "POST", "/path", "body", "", Some(s));
        assert_ne!(h1["Authorization"], h2["Authorization"]);
    }

    #[test]
    fn contains_algorithm() {
        let s = signing();
        let h = sign_request("access", "secret", "app", "POST", "/v1/token/wxvoip", "{}", "", Some(s));
        assert!(h["Authorization"].starts_with(TGV1_ALG));
    }

    #[test]
    fn required_headers_post() {
        let s = signing();
        let h = sign_request("access", "secret", "app", "POST", "/v1/token/wxvoip", "{}", "", Some(s));
        for name in &["X-Tg-Algorithm", "X-Tg-Date", "X-Tg-App-Id", "X-Tg-Content-Sha256", "Content-Type"] {
            assert!(!h[*name].is_empty(), "Header {} should not be empty", name);
        }
    }

    #[test]
    fn get_no_content_type() {
        let s = signing();
        let h = sign_request("access", "secret", "app", "GET", "/v1/devices", "", "status=online", Some(s));
        assert!(!h.contains_key("Content-Type"));
        assert!(!h.contains_key("Content-Length"));
    }

    #[test]
    fn uri_trailing_slash() {
        let s = signing();
        let h1 = sign_request("access", "secret", "app", "POST", "/path/", "{}", "", Some(s));
        let h2 = sign_request("access", "secret", "app", "POST", "/path", "{}", "", Some(s));
        assert_eq!(h1["Authorization"], h2["Authorization"]);
    }

    #[test]
    fn cross_language_vectors() {
        let s = signing();
        let data = load_vectors();
        let vectors = data["vectors"].as_array().expect("vectors array");

        for v in vectors {
            let desc = v["description"].as_str().unwrap();
            let h = sign_request(
                v["accessKey"].as_str().unwrap(),
                v["accessSecret"].as_str().unwrap(),
                v["appId"].as_str().unwrap(),
                v["method"].as_str().unwrap(),
                v["uriPath"].as_str().unwrap(),
                v["body"].as_str().unwrap_or(""),
                v["rawQuery"].as_str().unwrap_or(""),
                Some(s),
            );

            let expected = v["expected"].as_object().expect("expected object");
            for (key, val) in expected {
                let actual = h.get(key).map(|s| s.as_str()).unwrap_or("");
                let expected_val = val.as_str().unwrap();
                assert_eq!(
                    actual, expected_val,
                    "[{desc}] header '{key}' mismatch"
                );
            }
        }
    }
}
