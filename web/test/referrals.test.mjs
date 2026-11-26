import test from 'node:test';
import assert from 'node:assert/strict';
import esbuild from 'esbuild';

const build = await esbuild.build({
  stdin: { contents: `
    export { referralCodeFromPath, referralsApi } from './src/direction2/api/referrals';
    export { platformAuth } from './src/direction2/api/platform';
  `, resolveDir: process.cwd(), loader: 'ts' },
  bundle: true, write: false, platform: 'node', format: 'esm'
});
const { referralCodeFromPath, referralsApi, platformAuth } = await import(`data:text/javascript;base64,${Buffer.from(build.outputFiles[0].text).toString('base64')}`);
const code = '16000000-9000-4000-8000-000000000000';

test('only the dedicated referral path supplies a referral code', () => {
  assert.equal(referralCodeFromPath('/r/' + code), code);
  for (const path of ['/r/', '/invite/' + code, '/tasks/' + code, '/r/' + code + '/', '/r/invalid', '/r/' + code.toUpperCase() + '%2f']) {
    assert.equal(referralCodeFromPath(path), '');
  }
});

test('auth transport preserves referral for both registration modes, never login', async () => {
  const calls = [];
  globalThis.fetch = async (path, init) => {
    calls.push([path, init.body && JSON.parse(init.body)]);
    if (path.endsWith('/email-codes')) return Response.json({ challenge_id: 'register-challenge', expires_at: '2099-01-01T00:00:00Z', resend_after: 60 });
    return Response.json({ user: { id: 'test', email: 'signup@example.test', name: 'Test' } });
  };
  const register = { email: 'signup@example.test', name: 'Test', password: 'test-password', code: '', referral_code: code };
  await platformAuth.registerWithEmail({ ...register, method: 'password' });
  assert.deepEqual(calls.at(-1), ['/api/v1/auth/registrations', {
    email: register.email, name: 'Test', password: 'test-password', method: 'password', referral_code: code
  }]);
  await platformAuth.sendEmailCode({ email: register.email, purpose: 'register' });
  await platformAuth.registerWithEmail({ ...register, method: 'code', code: '123456' });
  assert.equal(calls.at(-1)[1].referral_code, code);
  assert.equal(calls.at(-1)[1].challenge_id, 'register-challenge');
  await platformAuth.loginWithEmail({ email: register.email, password: register.password });
  assert.equal(calls.at(-1)[0], '/api/v1/auth/sessions');
  assert.equal('referral_code' in calls.at(-1)[1], false);
});

test('admin save is versioned and code creation is a protected write', async () => {
  const calls = [];
  globalThis.fetch = async (path, init) => {
    calls.push({ path, body: init.body && JSON.parse(init.body), method: init.method, headers: init.headers });
    return Response.json({ enabled: true, version: 4, code });
  };
  await referralsApi.link();
  assert.equal(calls.at(-1).path, '/api/v1/referrals/me');
  assert.equal(calls.at(-1).method, undefined);
  await referralsApi.create();
  assert.equal(calls.at(-1).method, 'POST');
  assert.equal(calls.at(-1).headers.get('X-CSRF-Protection'), '1');
  await referralsApi.saveSettings({ version: 3, enabled: true });
  assert.equal(calls.at(-1).method, 'PATCH');
  assert.deepEqual(calls.at(-1).body, { expected_version: 3, enabled: true });
});
