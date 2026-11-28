import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';

const { chromium } = await import(process.env.KIKO_PLAYWRIGHT_MODULE || 'playwright');
const [server, session, workspace, task, digest] = process.argv.slice(2);
const browser = await chromium.launch({ channel: process.env.KIKO_PLAYWRIGHT_CHANNEL || 'chrome' });
try {
  const context = await browser.newContext();
  await context.addCookies([{ name: 'kiko_session', value: session, url: server, httpOnly: true }]);
  const page = await context.newPage();
  await page.goto(server + '/#/?' + new URLSearchParams({ view: 'tasks', ws: workspace, task }));
  const detail = page.getByLabel('任务详情', { exact: true });
  const image = detail.locator('figure img');
  await image.waitFor();
  const dimensions = await image.evaluate(async (img) => {
    await img.decode();
    return [img.naturalWidth, img.naturalHeight];
  });
  assert.deepEqual(dimensions, [1024, 1024]);
  const event = page.waitForEvent('download');
  await detail.getByRole('link', { name: '下载原图' }).click();
  const download = await event;
  const bytes = await readFile(await download.path());
  assert.equal(createHash('sha256').update(bytes).digest('hex'), digest);
  console.log('Chrome task detail rendered 1024×1024. Download matched the archived SHA-256.');
} finally {
  await browser.close();
}
