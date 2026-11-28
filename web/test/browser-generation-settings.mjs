import assert from 'node:assert/strict';
import { join } from 'node:path';

const { chromium } = await import(process.env.KIKO_PLAYWRIGHT_MODULE || 'playwright');
const [server, session, workspace, kind, base, model] = process.argv.slice(2);
const video = kind === 'video';
// Only the local test TLS supplier is eligible for this acceptance flow.
assert.equal(new URL(base).hostname, '127.0.0.1');
const key = video ? 'fixture-key' : 'test-only';
const browser = await chromium.launch({ channel: process.env.KIKO_PLAYWRIGHT_CHANNEL || 'chrome' });
try {
  const context = await browser.newContext({ viewport: { width: 1600, height: 1080 } });
  await context.addCookies([{ name: 'kiko_session', value: session, url: server, httpOnly: true }]);
  const page = await context.newPage();
  await page.goto(
    server + '/#/?' + new URLSearchParams({ view: 'settings', sub: 'platform-admin', ws: workspace }),
  );
  await page.getByRole('button', { name: '默认插件', exact: true }).click();
  if (video) await page.getByRole('button', { name: '文生视频', exact: true }).click();
  const card = page.locator('.' + kind + '-settings');
  const save = card.getByRole('button', { name: '保存配置' });
  if (video) assert.equal(await card.locator('#video-model-id').inputValue(), 'MiniMax-H3');
  await card.locator('#' + kind + '-service-url').fill(base);
  await card.locator('#' + kind + '-api-key').fill(key);
  await card.locator('#' + kind + '-model-id').fill(model);
  await card.locator('#' + kind + '-unit-price').fill(video ? '2' : '5');
  await card.getByRole('switch').click();
  assert.equal(await save.isDisabled(), true);
  const endpoint = server + '/api/v1/platform/default-plugins/' + kind + '-generation';
  const draft = { version: 0, base_url: base, upstream_model_id: model, api_key: key };
  const attempt = (data) => context.request.patch(endpoint, { headers: { 'X-CSRF-Protection': '1' }, data });
  const stale = await context.request.post(endpoint + '/test', {
    headers: { 'X-CSRF-Protection': '1' },
    data: { ...draft, version: draft.version + 1 },
  });
  assert.equal(stale.status(), 409);
  assert.equal((await stale.json()).error.code, 'version_conflict');
  const before = await attempt(draft);
  assert.equal(before.status(), 409);
  assert.equal((await before.json()).error.code, kind + '_test_required');
  const probe = page.waitForResponse(
    (r) => r.url() === endpoint + '/test' && r.request().method() === 'POST',
  );
  await card.getByRole('button', { name: video ? '测试视频生成' : '测试出图' }).click();
  const response = await probe;
  assert.equal(response.status(), 200);
  const result = await response.json();
  const wrong = await attempt({ ...draft, receipt: result.receipt, upstream_model_id: model + '-changed' });
  assert.equal(wrong.status(), 409);
  assert.equal((await wrong.json()).error.code, kind + '_test_required');
  await card.getByText('测试通过', { exact: false }).waitFor();
  if (video) {
    const advanced = await card.locator('video').evaluate(async (element) => {
      element.muted = true;
      await element.play();
      await new Promise((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('Playback stalled')), 8000);
        function tick() {
          if (element.currentTime < 0.1) return;
          clearTimeout(timeout);
          element.removeEventListener('timeupdate', tick);
          resolve();
        }
        element.addEventListener('timeupdate', tick);
      });
      element.pause();
      return [element.videoWidth, element.videoHeight, element.currentTime];
    });
    assert.deepEqual(advanced.slice(0, 2), [1344, 768]);
    assert.ok(advanced[2] >= 0.1);
  } else {
    assert.equal(await card.locator('img').evaluate((e) => e.complete && e.naturalWidth === 192), true);
  }
  const bounds = () =>
    card.evaluate((e) => {
      const a = e.querySelector('fieldset').getBoundingClientRect(),
        b = e.querySelector('aside').getBoundingClientRect();
      return {
        sideBySide: b.left >= a.right - 1,
        stacked: b.top >= a.bottom - 1,
        overflow: e.scrollWidth > e.clientWidth + 1,
      };
    });
  assert.equal((await bounds()).sideBySide, true);
  const directory = process.env.KIKO_TEST_SETTINGS_SCREENSHOTS;
  if (directory) await card.screenshot({ path: join(directory, kind + '-desktop.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  const menu = page.locator('.mobile-menu-btn');
  if ((await menu.getAttribute('aria-expanded')) === 'true') await page.locator('.mobile-close-btn').click();
  assert.deepEqual(await bounds(), { sideBySide: false, stacked: true, overflow: false });
  if (directory) await card.screenshot({ path: join(directory, kind + '-mobile.png') });
  await save.click();
  await card.getByText('配置已保存', { exact: true }).waitFor();
  const active = await (await context.request.get(endpoint)).json();
  assert.equal(active.upstream_model_id, model);
  assert.equal(active.enabled, true);
  assert.equal(active.credential_configured, true);
  assert.equal(await card.locator('#' + kind + '-api-key').inputValue(), '');
  console.log(
    kind + ' settings: preview decoded, desktop/mobile layout, receipt binding and saved config verified',
  );
} finally {
  await browser.close();
}
