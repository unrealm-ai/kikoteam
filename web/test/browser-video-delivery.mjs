import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";

const { chromium } = await import(
  process.env.KIKO_PLAYWRIGHT_MODULE || "playwright"
);
const [server, session, workspace, task, digest] = process.argv.slice(2);
const browser = await chromium.launch({
  channel: process.env.KIKO_PLAYWRIGHT_CHANNEL || "chrome",
});
try {
  const context = await browser.newContext();
  await context.addCookies([
    { name: "kiko_session", value: session, url: server, httpOnly: true },
  ]);
  const page = await context.newPage();
  await page.goto(
    server +
      "/#/?" +
      new URLSearchParams({ view: "tasks", ws: workspace, task }),
  );
  const detail = page.getByLabel("任务详情", { exact: true });
  const video = detail.locator("figure video");
  await video.waitFor();
  const metadata = await video.evaluate(async (element) => {
    element.muted = true;
    await element.play();
    await new Promise((resolve, reject) => {
      const timer = setTimeout(
        () => reject(new Error("video did not advance")),
        10000,
      );
      const advanced = () => {
        if (element.currentTime < 0.2) return;
        clearTimeout(timer);
        element.removeEventListener("timeupdate", advanced);
        resolve();
      };
      element.addEventListener("timeupdate", advanced);
    });
    element.pause();
    return [
      element.videoWidth,
      element.videoHeight,
      element.duration,
      element.currentTime,
    ];
  });
  assert.deepEqual(metadata.slice(0, 2), [768, 1344]);
  assert.ok(metadata[2] >= 3.5 && metadata[2] <= 5);
  const event = page.waitForEvent("download");
  await detail.getByRole("link", { name: /下载视频/ }).click();
  const download = await event;
  const bytes = await readFile(await download.path());
  assert.equal(createHash("sha256").update(bytes).digest("hex"), digest);
  console.log("Chrome played", metadata, "download SHA-256 matched", digest);
} finally {
  await browser.close();
}
