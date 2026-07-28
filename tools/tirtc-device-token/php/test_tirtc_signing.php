<?php

require_once __DIR__ . '/TirtcSigning.php';

function loadTestVectors(): array
{
    $path = __DIR__ . '/../test-vectors.json';
    return json_decode(file_get_contents($path), true)['vectors'];
}

$vectors = loadTestVectors();
$signing = new DateTime('2024-01-15T12:00:00Z');

$passed = 0;
$failed = 0;

// Deterministic
$h1 = TirtcSigning::signRequest('access123', 'secret', 'app456', 'POST', '/v1/token/wxvoip', '{"k":"v"}', '', $signing);
$h2 = TirtcSigning::signRequest('access123', 'secret', 'app456', 'POST', '/v1/token/wxvoip', '{"k":"v"}', '', $signing);
assert($h1['Authorization'] === $h2['Authorization'], 'Deterministic');
$passed++;

// Different secrets -> different sig
$h1 = TirtcSigning::signRequest('access', 'secret1', 'app', 'POST', '/path', 'body', '', $signing);
$h2 = TirtcSigning::signRequest('access', 'secret2', 'app', 'POST', '/path', 'body', '', $signing);
assert($h1['Authorization'] !== $h2['Authorization'], 'Different secrets');
$passed++;

// Contains algorithm
$h = TirtcSigning::signRequest('access', 'secret', 'app', 'POST', '/v1/token/wxvoip', '{}', '', $signing);
assert(str_starts_with($h['Authorization'], TirtcSigning::TGV1_ALG), 'Contains algorithm');
$passed++;

// Required headers POST
$h = TirtcSigning::signRequest('access', 'secret', 'app', 'POST', '/v1/token/wxvoip', '{}', '', $signing);
foreach (['X-Tg-Algorithm', 'X-Tg-Date', 'X-Tg-App-Id', 'X-Tg-Content-Sha256', 'Content-Type'] as $name) {
    assert(!empty($h[$name]), "Required header $name");
}
$passed++;

// GET no content-type
$h = TirtcSigning::signRequest('access', 'secret', 'app', 'GET', '/v1/devices', '', 'status=online', $signing);
assert(!isset($h['Content-Type']), 'GET no Content-Type');
assert(!isset($h['Content-Length']), 'GET no Content-Length');
$passed++;

// URI trailing slash
$h1 = TirtcSigning::signRequest('access', 'secret', 'app', 'POST', '/path/', '{}', '', $signing);
$h2 = TirtcSigning::signRequest('access', 'secret', 'app', 'POST', '/path', '{}', '', $signing);
assert($h1['Authorization'] === $h2['Authorization'], 'URI trailing slash');
$passed++;

// --- Cross-language test vectors ---
foreach ($vectors as $v) {
    $h = TirtcSigning::signRequest(
        $v['accessKey'],
        $v['accessSecret'],
        $v['appId'],
        $v['method'],
        $v['uriPath'],
        $v['body'],
        $v['rawQuery'],
        $signing
    );
    foreach ($v['expected'] as $key => $expectedVal) {
        $actual = $h[$key] ?? '';
        assert(
            $actual === $expectedVal,
            sprintf(
                "[%s] header '%s' mismatch:\n  expected: %s\n  actual:   %s",
                $v['description'], $key, $expectedVal, $actual
            )
        );
    }
    $passed++;
}

echo "PASS: all tests passed ($passed tests, " . count($vectors) . " vectors verified)\n";
