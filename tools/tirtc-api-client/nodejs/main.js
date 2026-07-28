#!/usr/bin/env node
/**
 * tirtc-api-client: 服务端调用探鸽云 OpenAPI 示例
 *
 * Usage:
 *   export TIRTC_APP_ID=xxx TIRTC_ACCESS_KEY=xxx TIRTC_SECRET_KEY=xxx
 *   node main.js wxvoip | aichat | login | plans
 */

const { signRequest } = require('./tirtc_signing');
const https = require('https');
const { URL } = require('url');

const ENDPOINT_TOKEN = 'https://api-tirtc.tange365.com';
const ENDPOINT_OPENAPI = 'https://openapi-cn01.tange365.com';

const api = process.argv[2];
if (!api) {
    console.error('用法: node main.js <api>');
    console.error('  wxvoip  — POST /v1/token/wxvoip');
    console.error('  aichat  — POST /v1/token/aichat');
    console.error('  login   — POST /v2/user/login/user-id');
    console.error('  plans   — GET  /v2/cloud-service/plans');
    process.exit(1);
}

const appId = requireEnv('TIRTC_APP_ID');
const accessKey = requireEnv('TIRTC_ACCESS_KEY');
const secretKey = requireEnv('TIRTC_SECRET_KEY');

switch (api) {
    case 'wxvoip': runWxVoip(appId, accessKey, secretKey); break;
    case 'aichat': runAiChat(appId, accessKey, secretKey); break;
    case 'login':  runLogin(appId, accessKey, secretKey); break;
    case 'plans':  runPlans(appId, accessKey, secretKey); break;
    default:
        console.error(`未知 API: ${api}`);
        process.exit(1);
}

function runWxVoip(appId, accessKey, secretKey) {
    const body = {
        device_id: 'TESTDEVICE01',
        wx_session_key: 'test-session-key',
        wx_room_id: 'test-room-001',
        wx_session_token: 'test-server-token',
        wx_app_id: 'wx0123456789abcdef',
        wx_model_id: 'model-001',
        audio_rate: 8000,
        audio_channels: 1,
    };
    console.log('=== POST /v1/token/wxvoip (微信 VoIP) ===');
    console.log(`device_id: ${body.device_id}\n`);
    doPost(appId, accessKey, secretKey, ENDPOINT_TOKEN, '/v1/token/wxvoip', body).then(printResult);
}

function runAiChat(appId, accessKey, secretKey) {
    const body = { device_id: 'TESTDEVICE01', role_id: 'your-role-id' };
    console.log('=== POST /v1/token/aichat (AI 语音对话) ===');
    console.log(`device_id: ${body.device_id}`);
    console.log(`role_id:   ${body.role_id}\n`);
    doPost(appId, accessKey, secretKey, ENDPOINT_TOKEN, '/v1/token/aichat', body).then(printResult);
}

function runLogin(appId, accessKey, secretKey) {
    const body = { user_id: 'test-user-001' };
    console.log('=== POST /v2/user/login/user-id (用户登录) ===');
    console.log(`user_id: ${body.user_id}\n`);
    doPost(appId, accessKey, secretKey, ENDPOINT_OPENAPI, '/v2/user/login/user-id', body).then(printResult);
}

function runPlans(appId, accessKey, secretKey) {
    console.log('=== GET /v2/cloud-service/plans (套餐列表) ===\n');
    doGet(appId, accessKey, secretKey, ENDPOINT_OPENAPI, '/v2/cloud-service/plans').then(printResult);
}

async function doPost(appId, accessKey, secretKey, endpoint, uriPath, body) {
    return doRequest(appId, accessKey, secretKey, endpoint, 'POST', uriPath, '', JSON.stringify(body));
}

async function doGet(appId, accessKey, secretKey, endpoint, uriPath, rawQuery = '') {
    return doRequest(appId, accessKey, secretKey, endpoint, 'GET', uriPath, rawQuery, null);
}

async function doRequest(appId, accessKey, secretKey, endpoint, method, uriPath, rawQuery, bodyStr) {
    const headers = signRequest(accessKey, secretKey, appId, method, uriPath, rawQuery, bodyStr);
    endpoint = process.env.TIRTC_ENDPOINT || endpoint;

    let fullUrl = endpoint + uriPath;
    if (rawQuery) fullUrl += '?' + rawQuery;
    console.log(`→ ${method} ${fullUrl}`);

    const url = new URL(fullUrl);
    const options = {
        hostname: url.hostname,
        port: url.port || 443,
        path: url.pathname + url.search,
        method,
        headers,
        timeout: 15000,
    };

    return new Promise((resolve) => {
        const req = https.request(options, (res) => {
            let data = '';
            res.on('data', chunk => data += chunk);
            res.on('end', () => resolve([res.statusCode, data]));
        });
        req.on('error', (e) => {
            console.error(`❌ 请求失败: ${e.message}`);
            resolve([0, '']);
        });
        req.on('timeout', () => {
            req.destroy();
            console.error('❌ 请求超时');
            resolve([0, '']);
        });
        if (bodyStr) req.write(bodyStr);
        req.end();
    });
}

function printResult([statusCode, body]) {
    console.log(`HTTP ${statusCode}`);
    if (body) {
        try {
            const parsed = JSON.parse(body);
            console.log(JSON.stringify(parsed, null, 2));
            const code = parsed.code;
            if (code === 0 || code === 200) console.log('✅ 成功');
            else if (code === 401 || code === 40105) console.log('❌ 签名验证失败');
            else console.log(`⚠️  code=${code}, msg=${parsed.msg || ''}`);
        } catch {
            console.log(body);
        }
    }
}

function requireEnv(key) {
    const v = process.env[key];
    if (!v) {
        console.error(`缺少环境变量 ${key}`);
        process.exit(1);
    }
    return v;
}
