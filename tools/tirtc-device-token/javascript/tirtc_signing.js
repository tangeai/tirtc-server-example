"use strict";

/**
 * TGV1-HMAC-SHA256 request signing for tange.ai TiRTC server APIs.
 *
 * Reference: https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg
 *
 * Usage:
 *   const { signRequest } = require('./tirtc_signing');
 *   const headers = signRequest(
 *     'your-access-key',
 *     'your-access-secret',
 *     'your-app-id',
 *     'POST',
 *     '/v2/device/info',
 *     '{"device_id":"TEST"}',
 *   );
 */

const crypto = require('crypto');

const TGV1_ALG = 'TGV1-HMAC-SHA256';
const CREDENTIAL_SCOPE_SUFFIX = 'tgv1_request';

/**
 * @param {Buffer|string} data
 * @returns {string}
 */
function sha256Hex(data) {
  return crypto.createHash('sha256').update(data).digest('hex');
}

/**
 * @param {string} data
 * @param {Buffer} key
 * @returns {Buffer}
 */
function hmacSha256Bytes(data, key) {
  return crypto.createHmac('sha256', key).update(data).digest();
}

/**
 * @param {string} data
 * @param {Buffer} key
 * @returns {string}
 */
function hmacSha256Hex(data, key) {
  return crypto.createHmac('sha256', key).update(data).digest('hex');
}

/**
 * @param {Date} signingTime
 * @returns {string}
 */
function buildCredentialScope(signingTime) {
  const d = signingTime.getUTCDate() + 7;
  const future = new Date(signingTime);
  future.setUTCDate(d);
  const yyyy = future.getUTCFullYear();
  const mm = String(future.getUTCMonth() + 1).padStart(2, '0');
  const dd = String(future.getUTCDate()).padStart(2, '0');
  return `${yyyy}${mm}${dd}/${CREDENTIAL_SCOPE_SUFFIX}`;
}

/**
 * Format Date as "YYYYMMDD" (UTC).
 * @param {Date} d
 * @returns {string}
 */
function formatDateYYYYMMDD(d) {
  const yyyy = d.getUTCFullYear();
  const mm = String(d.getUTCMonth() + 1).padStart(2, '0');
  const dd = String(d.getUTCDate()).padStart(2, '0');
  return `${yyyy}${mm}${dd}`;
}

/**
 * Format Date as "YYYYMMDDTHHmmssZ" (UTC).
 * @param {Date} d
 * @returns {string}
 */
function formatDateTg(d) {
  const yyyy = d.getUTCFullYear();
  const mm = String(d.getUTCMonth() + 1).padStart(2, '0');
  const dd = String(d.getUTCDate()).padStart(2, '0');
  const hh = String(d.getUTCHours()).padStart(2, '0');
  const min = String(d.getUTCMinutes()).padStart(2, '0');
  const ss = String(d.getUTCSeconds()).padStart(2, '0');
  return `${yyyy}${mm}${dd}T${hh}${min}${ss}Z`;
}

/**
 * @param {string} path
 * @returns {string}
 */
function canonicalURIPath(path) {
  path = path.trim();
  if (path.length > 1 && path.endsWith('/')) {
    path = path.slice(0, -1);
  }
  return path;
}

/**
 * @param {string} method
 * @param {string} rawQuery
 * @returns {string}
 */
function canonicalQuery(method, rawQuery) {
  const M = method.toUpperCase();
  if (M === 'POST' || M === 'PUT' || M === 'PATCH') {
    return '';
  }
  if (rawQuery.startsWith('?')) {
    rawQuery = rawQuery.slice(1);
  }
  return rawQuery.replace(/\+/g, '%20');
}

/**
 * Build TGV1-HMAC-SHA256 signed HTTP headers.
 *
 * @param {string} accessKey    - Appears in the Authorization Credential field.
 * @param {string} accessSecret - Used for HMAC key derivation (keep this secret).
 * @param {string} appId        - TiRTC application ID (X-Tg-App-Id header).
 * @param {string} method       - HTTP method (GET, POST, PUT, PATCH, DELETE).
 * @param {string} uriPath      - URI path, e.g. "/v2/device/info".
 * @param {string} [body=""]    - Request body string (empty for GET/DELETE).
 * @param {string} [rawQuery=""]- Raw query without leading "?" (ignored for POST/PUT/PATCH).
 * @param {Date}   [signingTime]- UTC Date; uses now if omitted.
 * @returns {Object<string,string>} HTTP header name -> value map.
 */
function signRequest(accessKey, accessSecret, appId, method, uriPath, body, rawQuery, signingTime) {
  method = method.toUpperCase();
  if (!signingTime) {
    signingTime = new Date();
  }
  body = body || '';
  rawQuery = rawQuery || '';

  const bodyBuf = Buffer.from(body, 'utf-8');
  const tgDate = formatDateTg(signingTime);
  const scope = buildCredentialScope(signingTime);
  const payloadHash = sha256Hex(bodyBuf);

  // Step 1: build header values map
  const hv = {};
  hv['x-tg-algorithm'] = TGV1_ALG;
  hv['x-tg-date'] = tgDate;
  hv['x-tg-app-id'] = appId.trim();
  hv['x-tg-content-sha256'] = payloadHash;
  if (method === 'POST' || method === 'PUT' || method === 'PATCH') {
    hv['content-type'] = 'application/json';
    hv['content-length'] = String(bodyBuf.length);
  }

  // Step 2: sorted lowercased header names
  const names = Object.keys(hv).map(k => k.toLowerCase()).sort();
  const signedHeaders = names.join(';');

  // Step 3: canonical header string
  const canonLines = names.map(name => name + ':' + hv[name].trim());
  const hCanon = canonLines.join('\n');

  // Step 4: canonical request
  const uriP = canonicalURIPath(uriPath);
  const qCanon = canonicalQuery(method, rawQuery);
  const canonicalReq = [method, uriP, qCanon, hCanon, signedHeaders, payloadHash].join('\n');

  // Step 5: string-to-sign
  const hashCanon = sha256Hex(Buffer.from(canonicalReq, 'utf-8'));
  const strToSign = [TGV1_ALG, tgDate, scope, hashCanon].join('\n');

  // Step 6: derive signing key
  let k = hmacSha256Bytes(formatDateYYYYMMDD(signingTime), Buffer.from('TGV1' + accessSecret, 'utf-8'));
  k = hmacSha256Bytes(uriP, k);
  k = hmacSha256Bytes(CREDENTIAL_SCOPE_SUFFIX, k);

  // Step 7: compute signature
  const sig = hmacSha256Hex(strToSign, k);

  // Step 8: build Authorization header
  const cred = accessKey + '/' + scope;
  const auth = `${TGV1_ALG} Credential=${cred}, SignedHeaders=${signedHeaders}, Signature=${sig}`;

  // Build output
  const result = {
    'X-Tg-Algorithm': hv['x-tg-algorithm'],
    'X-Tg-Date': hv['x-tg-date'],
    'X-Tg-App-Id': hv['x-tg-app-id'],
    'X-Tg-Content-Sha256': hv['x-tg-content-sha256'],
    'X-Tg-Signed-Headers': signedHeaders,
    'Authorization': auth,
  };
  if (hv['content-type']) {
    result['Content-Type'] = hv['content-type'];
  }
  if (hv['content-length']) {
    result['Content-Length'] = hv['content-length'];
  }
  return result;
}

module.exports = { signRequest, TGV1_ALG };
