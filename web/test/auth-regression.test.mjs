import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import esbuild from 'esbuild';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const ROOT = path.resolve(__dirname, '..');

async function loadTsModule(relEntry) {
  const result = esbuild.buildSync({
    entryPoints: [path.join(ROOT, relEntry)],
    bundle: true,
    write: false,
    format: 'esm',
    platform: 'node'
  });
  const code = result.outputFiles[0].text;
  return import(`data:text/javascript;base64,${Buffer.from(code, 'utf-8').toString('base64')}`);
}

const { D2MockBackend } = await loadTsModule('src/direction2/mock-server/handler.ts');

function readRel(relPath) {
  return fs.readFileSync(path.join(ROOT, relPath), 'utf-8');
}

async function registerFixture(backend) {
  const email = 'auth-fixture@example.test';
  const sent = await backend.handle('POST', '/api/auth/email/send-code', JSON.stringify({ email, purpose: 'register' }));
  assert.equal(sent.status, 200);
  const registered = await backend.handle('POST', '/api/auth/register', JSON.stringify({
    email, name: 'Auth fixture', password: 'FixturePassword123', code: sent.body.debugCode
  }));
  assert.equal(registered.status, 201);
}

test('Local demo serves legacy routes but not task-center APIs', async () => {
  const backend = new D2MockBackend();
  await registerFixture(backend);
  const login = await backend.handle('POST', '/api/auth/login', JSON.stringify({
    email: 'auth-fixture@example.test', password: 'FixturePassword123'
  }));
  const headers = { authorization: `Bearer ${login.body.token}` };
  const workspaces = await backend.handle('GET', '/api/workspaces', '', headers);
  const id = workspaces.body[0].id;
  assert.equal((await backend.handle('GET', `/api/workspaces/${id}/dashboard/overview`, '', headers)).status, 200);
  for (const path of ['/tasks', '/work-items', '/task-spaces', '/human-requests/summary', '/human-requests/history']) {
    const response = await backend.handle('GET', `/api/v1/workspaces/${id}${path}`, '', headers);
    assert.equal(response.status, 404, path);
    assert.equal(response.body.error.code, 'NOT_FOUND', path);
  }
});

test('Mock starts with no login accounts or current identity', async () => {
  const { createInitialMockDatabase } = await loadTsModule('src/direction2/mock-server/seed-data.ts');
  const db = createInitialMockDatabase();
  assert.deepEqual(db.authUsers, []);
  assert.equal(db.currentUser, null);
  const backend = new D2MockBackend();
  for (const email of ['youzai@kiko.corp', 'linxiao@kiko.corp']) {
    const result = await backend.handle('POST', '/api/auth/login', JSON.stringify({ email, password: 'Kiko123456' }));
    assert.equal(result.status, 401);
  }
  assert.equal((await backend.handle('GET', '/api/auth/me')).status, 401);
  await registerFixture(backend);
});

test('Auth 1: unauthenticated /api/auth/me returns 401; email registration requires valid verification code and creates workspace', async () => {
  const backend = new D2MockBackend();

  const unauthMe = await backend.handle('GET', '/api/auth/me');
  assert.equal(unauthMe.status, 401);
  assert.equal(unauthMe.body.error.code, 'UNAUTHORIZED');

  // Cannot register without requesting verification code
  const regNoCode = await backend.handle(
    'POST',
    '/api/auth/register',
    JSON.stringify({ name: '陈明', email: 'chenming@corp.test', code: '123456', password: 'Password123' })
  );
  assert.equal(regNoCode.status, 400);
  assert.equal(regNoCode.body.error.code, 'INVALID_VERIFICATION_CODE');

  await registerFixture(backend);

  // Duplicate existing email cannot send register code
  const dupSend = await backend.handle(
    'POST',
    '/api/auth/email/send-code',
    JSON.stringify({ email: 'auth-fixture@example.test', purpose: 'register' })
  );
  assert.equal(dupSend.status, 409);
  assert.equal(dupSend.body.error.code, 'EMAIL_ALREADY_EXISTS');

  // Send registration code
  const sendRes = await backend.handle(
    'POST',
    '/api/auth/email/send-code',
    JSON.stringify({ email: 'chenming@corp.test', purpose: 'register' })
  );
  assert.equal(sendRes.status, 200);
  assert.equal(sendRes.body.sent, true);
  assert.match(sendRes.body.debugCode, /^\d{6}$/);

  // Immediate resend is rate limited
  const resendBlocked = await backend.handle(
    'POST',
    '/api/auth/email/send-code',
    JSON.stringify({ email: 'chenming@corp.test', purpose: 'register' })
  );
  assert.equal(resendBlocked.status, 429);
  assert.equal(resendBlocked.body.error.code, 'RATE_LIMITED');

  // Wrong code is rejected
  const wrongCodeRes = await backend.handle(
    'POST',
    '/api/auth/register',
    JSON.stringify({
      name: '陈明',
      email: 'chenming@corp.test',
      code: sendRes.body.debugCode === '000000' ? '999999' : '000000',
      password: 'Password123'
    })
  );
  assert.equal(wrongCodeRes.status, 400);
  assert.equal(wrongCodeRes.body.error.code, 'INVALID_VERIFICATION_CODE');

  // Valid code succeeds and creates personal workspace + session
  const regOk = await backend.handle(
    'POST',
    '/api/auth/register',
    JSON.stringify({
      name: '陈明',
      email: 'chenming@corp.test',
      code: sendRes.body.debugCode,
      password: 'Password123'
    })
  );
  assert.equal(regOk.status, 201);
  assert.ok(regOk.body.token);
  assert.equal(regOk.body.user.email, 'chenming@corp.test');
  assert.equal(regOk.body.user.authProvider, 'email');
  assert.equal(regOk.body.user.emailVerified, true);
  assert.ok(regOk.body.workspaces.length >= 1);

  // Subsequent /api/auth/me with Bearer token works
  const meOk = await backend.handle('GET', '/api/auth/me', undefined, {
    authorization: `Bearer ${regOk.body.token}`
  });
  assert.equal(meOk.status, 200);
  assert.equal(meOk.body.email, 'chenming@corp.test');

  // Logout invalidates the session
  const logoutRes = await backend.handle('POST', '/api/auth/logout', undefined, {
    authorization: `Bearer ${regOk.body.token}`
  });
  assert.equal(logoutRes.status, 200);

  const meAfterLogout = await backend.handle('GET', '/api/auth/me', undefined, {
    authorization: `Bearer ${regOk.body.token}`
  });
  assert.equal(meAfterLogout.status, 401);
});

test('Auth 2: email password login, email code login, and forgot/reset password with verification code', async () => {
  const backend = new D2MockBackend();

  await registerFixture(backend);

  // Wrong password rejected
  const badLogin = await backend.handle(
    'POST',
    '/api/auth/login',
    JSON.stringify({ email: 'auth-fixture@example.test', password: 'WrongPassword' })
  );
  assert.equal(badLogin.status, 401);
  assert.equal(badLogin.body.error.code, 'INVALID_CREDENTIALS');

  // Valid password login succeeds
  const okLogin = await backend.handle(
    'POST',
    '/api/auth/login',
    JSON.stringify({ email: 'auth-fixture@example.test', password: 'FixturePassword123' })
  );
  assert.equal(okLogin.status, 200);
  const oldToken = okLogin.body.token;
  assert.ok(oldToken);

  // Forgot password for unknown email returns 404
  const resetUnknown = await backend.handle(
    'POST',
    '/api/auth/email/send-code',
    JSON.stringify({ email: 'unknown@kiko.corp', purpose: 'reset_password' })
  );
  assert.equal(resetUnknown.status, 404);
  assert.equal(resetUnknown.body.error.code, 'ACCOUNT_NOT_FOUND');

  // Send reset_password code for existing account
  const resetCodeRes = await backend.handle(
    'POST',
    '/api/auth/email/send-code',
    JSON.stringify({ email: 'auth-fixture@example.test', purpose: 'reset_password' })
  );
  assert.equal(resetCodeRes.status, 200);
  assert.equal(resetCodeRes.body.purpose, 'reset_password');
  assert.match(resetCodeRes.body.debugCode, /^\d{6}$/);

  // Reset password with wrong code fails
  const resetBadCode = await backend.handle(
    'POST',
    '/api/auth/password/reset',
    JSON.stringify({
      email: 'auth-fixture@example.test',
      code: resetCodeRes.body.debugCode === '111111' ? '222222' : '111111',
      newPassword: 'NewPassword999'
    })
  );
  assert.equal(resetBadCode.status, 400);
  assert.equal(resetBadCode.body.error.code, 'INVALID_VERIFICATION_CODE');

  // Reset password with valid code succeeds and invalidates prior sessions
  const resetOk = await backend.handle(
    'POST',
    '/api/auth/password/reset',
    JSON.stringify({
      email: 'auth-fixture@example.test',
      code: resetCodeRes.body.debugCode,
      newPassword: 'NewPassword999'
    })
  );
  assert.equal(resetOk.status, 200);
  assert.equal(resetOk.body.ok, true);

  // Old session is invalidated
  const oldMe = await backend.handle('GET', '/api/auth/me', undefined, {
    authorization: `Bearer ${oldToken}`
  });
  assert.equal(oldMe.status, 401);

  // Old password rejected, new password accepted
  const oldPwLogin = await backend.handle(
    'POST',
    '/api/auth/login',
    JSON.stringify({ email: 'auth-fixture@example.test', password: 'FixturePassword123' })
  );
  assert.equal(oldPwLogin.status, 401);

  const newPwLogin = await backend.handle(
    'POST',
    '/api/auth/login',
    JSON.stringify({ email: 'auth-fixture@example.test', password: 'NewPassword999' })
  );
  assert.equal(newPwLogin.status, 200);
  assert.equal(newPwLogin.body.user.email, 'auth-fixture@example.test');

  // Email verification code login works
  const loginCodeSend = await backend.handle(
    'POST',
    '/api/auth/email/send-code',
    JSON.stringify({ email: 'auth-fixture@example.test', purpose: 'login' })
  );
  assert.equal(loginCodeSend.status, 200);
  const codeLoginRes = await backend.handle(
    'POST',
    '/api/auth/login/code',
    JSON.stringify({ email: 'auth-fixture@example.test', code: loginCodeSend.body.debugCode })
  );
  assert.equal(codeLoginRes.status, 200);
  assert.ok(codeLoginRes.body.token);
});

test('Auth 3: Google OAuth flow validates state, handles cancellation, and supports new & existing accounts', async () => {
  const backend = new D2MockBackend();

  const startRes = await backend.handle(
    'GET',
    '/api/auth/oauth/google/url?redirectUri=http%3A%2F%2F127.0.0.1%3A15173%2F'
  );
  assert.equal(startRes.status, 200);
  assert.equal(startRes.body.provider, 'google');
  assert.ok(startRes.body.state);

  // Invalid state rejected
  const badStateRes = await backend.handle(
    'POST',
    '/api/auth/oauth/google/callback',
    JSON.stringify({
      code: 'gauth_123',
      state: 'invalid_state',
      redirectUri: 'http://127.0.0.1:15173/'
    })
  );
  assert.equal(badStateRes.status, 400);
  assert.equal(badStateRes.body.error.code, 'INVALID_OAUTH_STATE');

  // Cancelled OAuth consumes state and returns error
  const cancelRes = await backend.handle(
    'POST',
    '/api/auth/oauth/google/callback',
    JSON.stringify({
      code: 'access_denied',
      state: startRes.body.state,
      redirectUri: 'http://127.0.0.1:15173/'
    })
  );
  assert.equal(cancelRes.status, 400);
  assert.equal(cancelRes.body.error.code, 'OAUTH_CANCELLED');

  // Reusing consumed state fails
  const replayState = await backend.handle(
    'POST',
    '/api/auth/oauth/google/callback',
    JSON.stringify({
      code: 'gauth_123',
      state: startRes.body.state,
      redirectUri: 'http://127.0.0.1:15173/'
    })
  );
  assert.equal(replayState.status, 400);
  assert.equal(replayState.body.error.code, 'INVALID_OAUTH_STATE');

  // Fresh OAuth flow creates new Google account and logs in
  const startRes2 = await backend.handle(
    'GET',
    '/api/auth/oauth/google/url?redirectUri=http%3A%2F%2F127.0.0.1%3A15173%2F'
  );
  const okOAuth = await backend.handle(
    'POST',
    '/api/auth/oauth/google/callback',
    JSON.stringify({
      code: 'gauth_ok_456',
      state: startRes2.body.state,
      redirectUri: 'http://127.0.0.1:15173/',
      mockAccount: { email: 'new.google@gmail.com', name: 'New Google User' }
    })
  );
  assert.equal(okOAuth.status, 200);
  assert.ok(okOAuth.body.token);
  assert.equal(okOAuth.body.user.email, 'new.google@gmail.com');
  assert.equal(okOAuth.body.user.authProvider, 'google');
  assert.ok(okOAuth.body.workspaces.length >= 1);

  const meRes = await backend.handle('GET', '/api/auth/me', undefined, {
    cookie: `kiko_session=${encodeURIComponent(okOAuth.body.token)}`
  });
  assert.equal(meRes.status, 200);
  assert.equal(meRes.body.email, 'new.google@gmail.com');
});

test('Auth 4: AuthView and Direction2App wire email login, registration verification code, forgot/reset password, Google OAuth, and logout', () => {
  const authView = readRel('src/direction2/views/AuthView.vue');
  assert.match(authView, /handleRegister/);
  assert.match(authView, /handleSendVerificationCode\('register'\)/);
  assert.match(authView, /handleSendVerificationCode\('reset_password'\)/);
  assert.match(authView, /handleResetPassword/);
  assert.match(authView, /重新发送验证码/);
  assert.match(authView, /忘记密码？/);
  assert.match(authView, /handleGoogleOAuthLogin/);
  assert.match(authView, /使用 Google 账号继续/);
  assert.ok(
    authView.indexOf('</NTabs>') < authView.indexOf('class="oauth-section"'),
    'Google OAuth section must be placed below the email login/register tabs'
  );

  const appShell = readRel('src/direction2/Direction2App.vue');
  assert.match(appShell, /<AuthView/);
  assert.match(appShell, /shellStatus === 'unauthenticated'/);
  assert.match(appShell, /requestLogout/);
  assert.match(appShell, /退出登录/);
});
