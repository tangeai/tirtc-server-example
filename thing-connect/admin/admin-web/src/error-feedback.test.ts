import assert from 'node:assert/strict';
import test from 'node:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { createServer } from 'vite';
import {
  ErrorFeedbackStore,
  problemFromAPIResponse,
  problemFromRequestFailure,
} from './error-feedback.ts';

test('invalid administrator credentials include safe login guidance', () => {
  const problem = problemFromAPIResponse('/auth/login', 401, {
    code: 40101,
    msg: '账号、密码或验证码错误',
    data: null,
  });

  assert.deepEqual(problem, {
    message: '账号、密码或验证码错误',
    code: 40101,
    status: 401,
    suggestions: [
      '确认使用管理员邮箱登录，普通用户账号不能登录管理后台',
      '重新输入密码；已启用 MFA 时，同时检查验证码或恢复码',
    ],
  });
});

test('service failures preserve server guidance and add a safe recovery action', () => {
  const problem = problemFromAPIResponse('/configs', 503, {
    code: 503,
    msg: 'MySQL 连接检查失败',
    data: {
      suggestions: ['检查地址、TLS、迁移账号密码和来源授权'],
    },
  });

  assert.deepEqual(problem, {
    message: 'MySQL 连接检查失败',
    code: 503,
    status: 503,
    suggestions: [
      '检查地址、TLS、迁移账号密码和来源授权',
      '检查 Admin Server 及其必需依赖的就绪状态，然后重试',
    ],
  });
});

test('permission failures tell the administrator how to resolve access', () => {
  const problem = problemFromAPIResponse('/admin-users', 403, {
    code: 40301,
    msg: '无权执行此操作',
    data: null,
  });

  assert.deepEqual(problem.suggestions, ['联系超级管理员确认当前账号的角色和权限']);
});

test('common API failures carry a status-specific next step', () => {
  const cases = [
    [400, '检查表单中的必填项、格式和取值范围后重试'],
    [401, '登录状态已失效，请重新登录'],
    [404, '刷新页面确认对象仍然存在，或返回列表重新选择'],
    [409, '刷新页面获取最新数据后重试，避免覆盖他人修改'],
    [413, '缩小上传文件后重试'],
    [429, '等待片刻后重试，避免连续提交'],
  ] as const;

  for (const [status, suggestion] of cases) {
    const problem = problemFromAPIResponse('/configs', status, {
      code: status,
      msg: '操作失败',
      data: null,
    });
    assert.ok(problem.suggestions.includes(suggestion), `HTTP ${status}: ${suggestion}`);
  }
});

test('network failures do not expose raw browser errors and tell the user what to check', () => {
  const problem = problemFromRequestFailure(
    'network',
    new TypeError('Failed to fetch /secret-url'),
  );

  assert.deepEqual(problem, {
    message: '无法连接 Admin Server',
    suggestions: ['检查网络连接和 Admin Server 是否正在运行，然后重试'],
  });
});

test('failed API responses throw the structured customer-facing problem', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  const response = new Response(
    JSON.stringify({ code: 40101, msg: '账号、密码或验证码错误', data: null }),
    { status: 401, headers: { 'Content-Type': 'application/json' } },
  );

  try {
    const apiResponse = await vite.ssrLoadModule('/src/api-response.ts');
    await assert.rejects(apiResponse.readAPIResponse('/auth/login', response), (error: any) => {
      assert.ok(error instanceof apiResponse.AdminAPIError);
      assert.equal(error.problem.code, 40101);
      assert.match(error.problem.suggestions.join('\n'), /管理员邮箱/);
      return true;
    });
  } finally {
    await vite.close();
  }
});

test('non-JSON API failures are converted without exposing the proxy response body', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  const response = new Response('<html>bad gateway internal detail</html>', {
    status: 502,
    headers: { 'Content-Type': 'text/html' },
  });

  try {
    const apiResponse = await vite.ssrLoadModule('/src/api-response.ts');
    await assert.rejects(apiResponse.readAPIResponse('/configs', response), (error: any) => {
      assert.equal(error.problem.message, 'Admin Server 返回了无法识别的响应（HTTP 502）');
      assert.match(error.problem.suggestions.join('\n'), /反向代理.*Admin Server 日志/);
      assert.doesNotMatch(error.message, /bad gateway|internal detail/i);
      return true;
    });
  } finally {
    await vite.close();
  }
});

test('reported errors remain available to the UI and the same failure is not duplicated', () => {
  const store = new ErrorFeedbackStore();
  const error = {
    problem: {
      message: '保存失败',
      code: 40901,
      status: 409,
      suggestions: ['刷新页面后重试'],
    },
  };

  store.report(error);
  store.report(error);

  assert.equal(store.getSnapshot().length, 1);
  assert.equal(store.getSnapshot()[0].problem.message, '保存失败');
});

test('raw fetch failures are sanitized when a page reports them directly', () => {
  const store = new ErrorFeedbackStore();

  store.report(new TypeError('Failed to fetch https://internal.example/secret'));

  assert.deepEqual(store.getSnapshot()[0].problem, {
    message: '无法连接 Admin Server',
    suggestions: ['检查网络连接和 Admin Server 是否正在运行，然后重试'],
  });
});

test('separate failures with the same customer-facing problem are collapsed', () => {
  const store = new ErrorFeedbackStore();

  store.report(new TypeError('Failed to fetch setup status'));
  store.report(new TypeError('Failed to fetch session refresh'));

  assert.equal(store.getSnapshot().length, 1);
});

test('setup problem codes remain visible even when the server has no suggestions', () => {
  const store = new ErrorFeedbackStore();

  store.report({ problem: { code: 'SETUP_BUSY', message: '安装任务正在执行' } });

  assert.deepEqual(store.getSnapshot()[0].problem, {
    code: 'SETUP_BUSY',
    message: '安装任务正在执行',
    suggestions: [],
  });
});

test('the customer-facing problem renders as a persistent accessible alert', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  try {
    const feedback = await vite.ssrLoadModule('/src/error-feedback-view.tsx');
    const html = renderToStaticMarkup(
      createElement(feedback.AdminProblemAlert, {
        problem: {
          message: '账号、密码或验证码错误',
          code: 40101,
          status: 401,
          suggestions: ['确认使用管理员邮箱登录'],
        },
      }),
    );

    assert.match(html, /role="alert"/);
    assert.match(html, /账号、密码或验证码错误/);
    assert.match(html, /确认使用管理员邮箱登录/);
    assert.match(html, /业务码：40101/);
    assert.match(html, /HTTP 401/);
  } finally {
    await vite.close();
  }
});

test('every failed Admin request is reported before it reaches the page', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  let reported: unknown;
  const fetcher = async () =>
    new Response(JSON.stringify({ code: 40301, msg: '无权执行此操作', data: null }), {
      status: 403,
      headers: { 'Content-Type': 'application/json' },
    });

  try {
    const apiResponse = await vite.ssrLoadModule('/src/api-response.ts');
    await assert.rejects(
      apiResponse.executeAdminRequest('/admin-users', {}, fetcher, (error: unknown) => {
        reported = error;
      }),
      (error: unknown) => error === reported && error instanceof apiResponse.AdminAPIError,
    );
  } finally {
    await vite.close();
  }
});

test('failed downloads use the same visible error reporting channel', async () => {
  const vite = await createServer({
    appType: 'custom',
    logLevel: 'silent',
    server: { middlewareMode: true },
  });
  let reported: any;
  const fetcher = async () =>
    new Response(JSON.stringify({ code: 503, msg: '导出服务暂不可用', data: null }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    });

  try {
    const apiResponse = await vite.ssrLoadModule('/src/api-response.ts');
    await assert.rejects(
      apiResponse.executeAdminDownload('/jobs/1/download', fetcher, (error: unknown) => {
        reported = error;
      }),
      (error: unknown) => error === reported,
    );
    assert.equal(reported.problem.message, '导出服务暂不可用');
    assert.match(reported.problem.suggestions.join('\n'), /必需依赖/);
  } finally {
    await vite.close();
  }
});
