import assert from 'node:assert/strict';
import test from 'node:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { App as AntApp } from 'antd';
import { createServer } from 'vite';

const mysqlProblem = {
  code: 'MYSQL_UNAVAILABLE',
  message: 'MySQL 连接检查失败',
  suggestions: ['确认 MySQL 地址和端口可从安装服务器访问', '检查 TLS、迁移账号密码和来源授权'],
};

test('setup API preserves customer guidance from a failed request', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    new Response(JSON.stringify({ code: 503, msg: mysqlProblem.message, data: mysqlProblem }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    });

  try {
    const setup = await vite.ssrLoadModule('/src/setup.tsx');
    await assert.rejects(setup.setupAPI('/preview'), (error: unknown) => {
      assert.equal(
        (error as { problem?: unknown }).problem &&
          (error as { problem: typeof mysqlProblem }).problem.code,
        mysqlProblem.code,
      );
      assert.deepEqual(
        (error as { problem: typeof mysqlProblem }).problem.suggestions,
        mysqlProblem.suggestions,
      );
      return true;
    });
  } finally {
    globalThis.fetch = originalFetch;
    await vite.close();
  }
});

test('setup request guidance remains visible in the page', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  try {
    const setup = await vite.ssrLoadModule('/src/setup.tsx');
    const html = renderToStaticMarkup(
      createElement(setup.SetupProblemAlert, { problem: mysqlProblem }),
    );

    assert.match(html, /MySQL 连接检查失败/);
    assert.match(html, /确认 MySQL 地址和端口可从安装服务器访问/);
    assert.match(html, /检查 TLS、迁移账号密码和来源授权/);
    assert.match(html, /role="alert"/);
  } finally {
    await vite.close();
  }
});

test('recovery page shows the failed service and its suggested resolution', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  try {
    const setup = await vite.ssrLoadModule('/src/setup.tsx');
    const portProblem = {
      code: 'PORT_IN_USE',
      message: '设备通话服务无法启动：端口 9005 已被占用',
      suggestions: ['停止占用端口的旧实例，然后重试'],
    };
    const html = renderToStaticMarkup(
      createElement(
        AntApp,
        null,
        createElement(setup.SetupPage, {
          initial: {
            mode: 'recovery',
            operation_id: 'operation-1',
            phase: 'starting_services',
            percent: 75,
            retryable: true,
            problem: portProblem,
            services: [{ name: 'call-server', state: 'failed', problem: portProblem }],
          },
        }),
      ),
    );

    assert.match(html, /设备通话服务（call-server）/);
    assert.match(html, /端口 9005 已被占用/);
    assert.match(html, /停止占用端口的旧实例，然后重试/);
    assert.match(html, /启动失败/);
  } finally {
    await vite.close();
  }
});
