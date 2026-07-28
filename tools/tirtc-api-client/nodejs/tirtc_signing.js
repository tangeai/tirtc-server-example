/**
 * TGV1-HMAC-SHA256 request signing for tange.ai server APIs.
 *
 * Usage:
 *   const { signRequest } = require('./tirtc_signing');
 *   const headers = signRequest(accessKey, accessSecret, appId, 'POST', '/v2/user/login/user-id', '{"user_id":"test"}');
 */

const crypto = require('crypto');

const ALG_TGV1 = 'TGV1-HMAC-SHA256';

/**
 * @param {string} accessKey
 * @param {string} accessSecret
 * @param {string} appId
 * @param {string} method
 * @param {string} uriPath
 * @param {string} rawQuery
 * @param {string|null} body
 * @param {Date|null} signingTime
 * @returns {Record<string, string>}
 */
function signRequest(accessKey, accessSecret, appId, method, uriPath, rawQuery = '', body = null, signingTime = null) {
    method = method.toUpperCase();
    const now = signingTime || new Date();
    const tgDate = formatTgDate(now);
    const bodyStr = body || '';
    const payloadHash = sha256Hex(bodyStr);

    // Step 1: build header map
    const hv = {
        'X-Tg-Algorithm': ALG_TGV1,
        'X-Tg-Date': tgDate,
        'X-Tg-App-Id': appId.trim(),
        'X-Tg-Content-Sha256': payloadHash,
    };
    // Signed headers: only x-tg-app-id, x-tg-date (+ content-type, content-length for body methods)
    const signedNames = ['X-Tg-App-Id', 'X-Tg-Date'];
    if (['POST', 'PUT', 'PATCH'].includes(method)) {
        hv['Content-Type'] = 'application/json';
        hv['Content-Length'] = String(Buffer.byteLength(bodyStr, 'utf-8'));
        signedNames.unshift('Content-Length', 'Content-Type');
    }

    // Step 2: sorted signed header names
    const signedHdrs = signedNames.map(k => k.toLowerCase()).join(';');

    // Step 3: canonical headers string
    const lines = signedNames.map(k => `${k.toLowerCase()}:${hv[k]}`);
    const canonHdrs = lines.join('\n');

    // Step 4: canonical request
    const uri = canonicalURI(uriPath);
    const qCanon = canonicalQuery(method, rawQuery.replace(/^\?/, ''));
    const canonReq = [method, uri, qCanon, canonHdrs, signedHdrs, payloadHash].join('\n');

    // Step 5: string to sign
    const scopeDate = addDays(now, 7);
    const scope = `${scopeDate}/tgv1_request`;
    const strToSign = [ALG_TGV1, tgDate, scope, sha256Hex(canonReq)].join('\n');

    // Step 6: derive signing key
    let k = hmacSha256(formatDate(now), Buffer.from('TGV1' + accessSecret));
    k = hmacSha256(uri, k);
    k = hmacSha256('tgv1_request', k);

    // Step 7: signature
    const sig = hmacSha256Hex(strToSign, k);

    // Step 8: Authorization
    const auth = `${ALG_TGV1} Credential=${accessKey}/${scope}, SignedHeaders=${signedHdrs}, Signature=${sig}`;

    const out = {
        'X-Tg-Algorithm': hv['X-Tg-Algorithm'],
        'X-Tg-Date': hv['X-Tg-Date'],
        'X-Tg-App-Id': hv['X-Tg-App-Id'],
        'X-Tg-Content-Sha256': hv['X-Tg-Content-Sha256'],
        'X-Tg-Signed-Headers': signedHdrs,
        'Authorization': auth,
    };
    if (hv['Content-Type']) out['Content-Type'] = hv['Content-Type'];
    if (hv['Content-Length']) out['Content-Length'] = hv['Content-Length'];
    return out;
}

function sha256Hex(data) {
    return crypto.createHash('sha256').update(data, 'utf-8').digest('hex');
}

function hmacSha256(data, key) {
    return crypto.createHmac('sha256', key).update(data, 'utf-8').digest();
}

function hmacSha256Hex(data, key) {
    return crypto.createHmac('sha256', key).update(data, 'utf-8').digest('hex');
}

function canonicalURI(p) {
    p = p.trim();
    if (p.length > 1 && p.endsWith('/')) p = p.slice(0, -1);
    return p;
}

function canonicalQuery(method, rawQuery) {
    if (['POST', 'PUT', 'PATCH'].includes(method.toUpperCase())) return '';
    return rawQuery.replace(/\+/g, '%20');
}

function formatTgDate(d) {
    return d.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}Z/, 'Z');
}

function formatDate(d) {
    return d.toISOString().slice(0, 10).replace(/-/g, '');
}

function addDays(d, n) {
    const r = new Date(d);
    r.setUTCDate(r.getUTCDate() + n);
    return formatDate(r);
}

module.exports = { signRequest };
