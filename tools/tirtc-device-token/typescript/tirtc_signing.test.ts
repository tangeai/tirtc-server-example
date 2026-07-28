import * as fs from 'fs';
import * as path from 'path';
import { signRequest, TGV1_ALG, SigningHeaders } from './tirtc_signing';

interface TestVector {
  description: string;
  accessKey: string;
  accessSecret: string;
  appId: string;
  method: string;
  uriPath: string;
  body: string;
  rawQuery: string;
  signingTime: string;
  expected: Record<string, string>;
}

function loadTestVectors(): TestVector[] {
  const p = path.join(__dirname, '..', 'test-vectors.json');
  return JSON.parse(fs.readFileSync(p, 'utf-8')).vectors;
}

const vectors = loadTestVectors();
const signing = new Date('2024-01-15T12:00:00Z');

function assertEqual(actual: string, expected: string, msg: string): void {
  if (actual !== expected) {
    throw new Error(`${msg}\n  expected: ${expected}\n  actual:   ${actual}`);
  }
}

// Deterministic
{
  const h1 = signRequest('access123', 'secret', 'app456', 'POST', '/v1/token/wxvoip', '{"k":"v"}', '', signing);
  const h2 = signRequest('access123', 'secret', 'app456', 'POST', '/v1/token/wxvoip', '{"k":"v"}', '', signing);
  assertEqual(h1.Authorization, h2.Authorization, 'Deterministic');
}

// Different secrets -> different sig
{
  const h1 = signRequest('access', 'secret1', 'app', 'POST', '/path', 'body', '', signing);
  const h2 = signRequest('access', 'secret2', 'app', 'POST', '/path', 'body', '', signing);
  if (h1.Authorization === h2.Authorization) {
    throw new Error('Different secrets must produce different sigs');
  }
}

// Contains algorithm
{
  const h = signRequest('access', 'secret', 'app', 'POST', '/v1/token/wxvoip', '{}', '', signing);
  if (!h.Authorization.startsWith(TGV1_ALG)) {
    throw new Error('Authorization must start with algorithm');
  }
}

// Required headers POST
{
  const h = signRequest('access', 'secret', 'app', 'POST', '/v1/token/wxvoip', '{}', '', signing);
  ['X-Tg-Algorithm', 'X-Tg-Date', 'X-Tg-App-Id', 'X-Tg-Content-Sha256', 'Content-Type'].forEach(name => {
    if (!(h as any)[name]) throw new Error(`Header ${name} should not be empty`);
  });
}

// GET no content-type
{
  const h = signRequest('access', 'secret', 'app', 'GET', '/v1/devices', '', 'status=online', signing);
  if (h['Content-Type'] || h['Content-Length']) {
    throw new Error('GET should not have Content-Type/Length');
  }
}

// URI trailing slash
{
  const h1 = signRequest('access', 'secret', 'app', 'POST', '/path/', '{}', '', signing);
  const h2 = signRequest('access', 'secret', 'app', 'POST', '/path', '{}', '', signing);
  assertEqual(h1.Authorization, h2.Authorization, 'URI trailing slash');
}

// --- Cross-language test vectors ---
vectors.forEach((v: TestVector) => {
  const h: SigningHeaders = signRequest(v.accessKey, v.accessSecret, v.appId, v.method, v.uriPath, v.body, v.rawQuery, signing);
  Object.keys(v.expected).forEach((key: string) => {
    assertEqual(
      (h as any)[key] || '',
      v.expected[key],
      `[${v.description}] header '${key}' mismatch`
    );
  });
});

console.log(`PASS: all tests passed (${vectors.length} vectors verified)`);
