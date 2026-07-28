<?php
/**
 * tirtc-api-client: 服务端调用探鸽云 OpenAPI 示例
 *
 * Usage:
 *   export TIRTC_APP_ID=xxx TIRTC_ACCESS_KEY=xxx TIRTC_SECRET_KEY=xxx
 *   php main.php wxvoip | aichat | login | plans
 */

require_once __DIR__ . '/TirtcSigning.php';

const ENDPOINT_TOKEN = 'https://api-tirtc.tange365.com';
const ENDPOINT_OPENAPI = 'https://openapi-cn01.tange365.com';

if ($argc < 2) {
    fwrite(STDERR, "用法: php main.php <api>\n");
    fwrite(STDERR, "  wxvoip  — POST /v1/token/wxvoip\n");
    fwrite(STDERR, "  aichat  — POST /v1/token/aichat\n");
    fwrite(STDERR, "  login   — POST /v2/user/login/user-id\n");
    fwrite(STDERR, "  plans   — GET  /v2/cloud-service/plans\n");
    exit(1);
}

$appId = requireEnv('TIRTC_APP_ID');
$accessKey = requireEnv('TIRTC_ACCESS_KEY');
$secretKey = requireEnv('TIRTC_SECRET_KEY');

switch ($argv[1]) {
    case 'wxvoip': runWxVoip($appId, $accessKey, $secretKey); break;
    case 'aichat': runAiChat($appId, $accessKey, $secretKey); break;
    case 'login':  runLogin($appId, $accessKey, $secretKey); break;
    case 'plans':  runPlans($appId, $accessKey, $secretKey); break;
    default:
        fwrite(STDERR, "未知 API: {$argv[1]}\n");
        exit(1);
}

function runWxVoip($appId, $accessKey, $secretKey) {
    $body = json_encode([
        'device_id' => 'TESTDEVICE01',
        'wx_session_key' => 'test-session-key',
        'wx_room_id' => 'test-room-001',
        'wx_session_token' => 'test-server-token',
        'wx_app_id' => 'wx0123456789abcdef',
        'wx_model_id' => 'model-001',
        'audio_rate' => 8000,
        'audio_channels' => 1,
    ]);
    echo "=== POST /v1/token/wxvoip (微信 VoIP) ===\n";
    echo "device_id: TESTDEVICE01\n\n";
    [$code, $resp] = doPost($appId, $accessKey, $secretKey, ENDPOINT_TOKEN, '/v1/token/wxvoip', $body);
    printResult($code, $resp);
}

function runAiChat($appId, $accessKey, $secretKey) {
    $body = json_encode(['device_id' => 'TESTDEVICE01', 'role_id' => 'your-role-id']);
    echo "=== POST /v1/token/aichat (AI 语音对话) ===\n";
    echo "device_id: TESTDEVICE01\nrole_id:   your-role-id\n\n";
    [$code, $resp] = doPost($appId, $accessKey, $secretKey, ENDPOINT_TOKEN, '/v1/token/aichat', $body);
    printResult($code, $resp);
}

function runLogin($appId, $accessKey, $secretKey) {
    $body = json_encode(['user_id' => 'test-user-001']);
    echo "=== POST /v2/user/login/user-id (用户登录) ===\n";
    echo "user_id: test-user-001\n\n";
    [$code, $resp] = doPost($appId, $accessKey, $secretKey, ENDPOINT_OPENAPI, '/v2/user/login/user-id', $body);
    printResult($code, $resp);
}

function runPlans($appId, $accessKey, $secretKey) {
    echo "=== GET /v2/cloud-service/plans (套餐列表) ===\n\n";
    [$code, $resp] = doGet($appId, $accessKey, $secretKey, ENDPOINT_OPENAPI, '/v2/cloud-service/plans');
    printResult($code, $resp);
}

function doPost($appId, $accessKey, $secretKey, $endpoint, $uriPath, $body) {
    return doRequest($appId, $accessKey, $secretKey, $endpoint, 'POST', $uriPath, '', $body);
}

function doGet($appId, $accessKey, $secretKey, $endpoint, $uriPath, $rawQuery = '') {
    return doRequest($appId, $accessKey, $secretKey, $endpoint, 'GET', $uriPath, $rawQuery, null);
}

function doRequest($appId, $accessKey, $secretKey, $endpoint, $method, $uriPath, $rawQuery, $bodyStr) {
    $headers = TirtcSigning::signRequest($accessKey, $secretKey, $appId, $method, $uriPath, $rawQuery, $bodyStr);
    $endpoint = getenv('TIRTC_ENDPOINT') ?: $endpoint;

    $fullUrl = $endpoint . $uriPath;
    if ($rawQuery) $fullUrl .= '?' . $rawQuery;
    echo "→ $method $fullUrl\n";

    $ch = curl_init($fullUrl);
    curl_setopt($ch, CURLOPT_CUSTOMREQUEST, $method);
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_TIMEOUT, 15);
    curl_setopt($ch, CURLOPT_HTTPHEADER, array_map(function($k, $v) {
        return "$k: $v";
    }, array_keys($headers), $headers));
    if ($bodyStr) {
        curl_setopt($ch, CURLOPT_POSTFIELDS, $bodyStr);
    }

    $resp = curl_exec($ch);
    $httpCode = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    curl_close($ch);

    return [$httpCode, $resp ?: ''];
}

function printResult($statusCode, $body) {
    echo "HTTP $statusCode\n";
    if ($body) {
        $parsed = json_decode($body, true);
        if ($parsed) {
            echo json_encode($parsed, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE) . "\n";
            $code = $parsed['code'] ?? -1;
            if ($code === 0 || $code === 200) echo "✅ 成功\n";
            elseif ($code === 401 || $code === 40105) echo "❌ 签名验证失败\n";
            else echo "⚠️  code=$code, msg=" . ($parsed['msg'] ?? '') . "\n";
        } else {
            echo "$body\n";
        }
    }
}

function requireEnv($key) {
    $v = getenv($key);
    if (!$v) {
        fwrite(STDERR, "缺少环境变量 $key\n");
        exit(1);
    }
    return $v;
}
