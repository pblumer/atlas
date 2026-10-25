// End-to-end coverage for deleting finished instances (api/web/historypurge.js) and the
// storage card's reclaim section (api/web/storage.js)
// (ADR-0486).
//
// The server already refuses a purge that does not repeat the process id and already
// passes over what the exporter does not have. What these tests are about is whether an
// administrator reads the count before anything goes, is stopped from deleting by a
// slip, and can follow — and stop — a purge of many batches; and whether the card that
// shows the disk says how to give it back and asks before it compacts.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/history-purge-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const calls = (page) => page.evaluate(() => window.__calls.map((c) => c.method + " " + c.url));

test("the preview counts every version, and only the process id arms the delete", async ({ page }) => {
  await page.locator("#purge").click();
  const modal = page.locator(".purge-modal");
  await expect(modal).toBeVisible();

  // All versions are the default: three and two finished, four running on v2.
  await expect(modal.locator(".purge-count")).toContainText("5 Instanzen");
  await expect(modal.locator(".purge-versions")).toContainText("v1: 3");
  await expect(modal.locator(".purge-versions")).toContainText("v2: 2");
  await expect(modal.locator(".purge-preview")).toContainText("4 laufende Instanzen bleiben unberührt");
  // The three things nobody expects are said before anything is deleted.
  await expect(modal).toContainText("WAL-Kompaktierung");
  await expect(modal).toContainText("Finished");
  await expect(modal).toContainText("atlas:historyTtl");

  const go = modal.locator("[data-purge-go]");
  await expect(go).toBeDisabled();
  await modal.locator("#purge-confirm").fill("Bulk");
  await expect(go).toBeDisabled();
  await modal.locator("#purge-confirm").fill("bulk");
  await expect(go).toBeEnabled();

  // Nothing has been written: two previews, one per version, with one cut-off.
  const got = await calls(page);
  expect(got).toHaveLength(2);
  expect(got.every((c) => c.startsWith("GET /api/v1/processes/10"))).toBe(true);
  const before = new URL(got[0].split(" ")[1], "http://x").searchParams.get("before");
  expect(before).toMatch(/^\d{4}-\d{2}-\d{2}T/);
  expect(got[1]).toContain(encodeURIComponent(before));

  expect(page.__errors, "page errors").toEqual([]);
});

test("the batches run until nothing remains, in every version, with the cut-off and the cursor repeated", async ({ page }) => {
  await page.locator("#purge").click();
  const modal = page.locator(".purge-modal");
  await expect(modal.locator(".purge-count")).toContainText("5");
  const before = await page.evaluate(() => new URL(window.__calls[0].url, location.origin).searchParams.get("before"));
  await modal.locator("#purge-confirm").fill("bulk");
  await modal.locator("[data-purge-go]").click();

  await expect(modal.locator(".purge-progress")).toContainText("5 Instanzen gelöscht");
  await expect(modal.locator(".purge-progress")).toContainText("Gelöscht: 5 von 5");
  await expect(modal.locator(".purge-progress")).toContainText("kompaktiere");

  const posts = (await page.evaluate(() => window.__calls)).filter((c) => c.method === "POST").map((c) => new URL(c.url, "http://x"));
  // Newest version first, as the picker lists them: v2 holds two, one batch; v1 holds
  // three, two batches.
  expect(posts.map((u) => u.pathname)).toEqual([
    "/api/v1/processes/102/history-purge",
    "/api/v1/processes/101/history-purge",
    "/api/v1/processes/101/history-purge",
  ]);
  for (const u of posts) {
    expect(u.searchParams.get("confirm")).toBe("bulk");
    expect(u.searchParams.get("before")).toBe(before);
  }
  expect(posts[0].searchParams.get("after")).toBeNull();
  expect(posts[1].searchParams.get("after")).toBeNull();
  expect(posts[2].searchParams.get("after")).toBe("1002:1012");

  await modal.locator("[data-purge-close]").click();
  await expect(modal).toHaveCount(0);
  expect(await page.evaluate(() => window.__result)).toBe(5);
  expect(await page.evaluate(() => window.__reloaded)).toBe(true);
  expect(page.__errors, "page errors").toEqual([]);
});

test("a server that switched the purge off says so, and the delete stays disarmed", async ({ page }) => {
  await page.locator("#purge-off").click();
  const modal = page.locator(".purge-modal");
  await expect(modal.locator(".purge-preview")).toContainText("--instance-purge=false");
  await modal.locator("#purge-confirm").fill("bulk");
  await expect(modal.locator("[data-purge-go]")).toBeDisabled();
  expect(page.__errors, "page errors").toEqual([]);
});

test("a failure part-way says how many were already deleted", async ({ page }) => {
  await page.locator("#purge-fail").click();
  const modal = page.locator(".purge-modal");
  await expect(modal.locator(".purge-count")).toContainText("5");
  await modal.locator("#purge-confirm").fill("bulk");
  await modal.locator("[data-purge-go]").click();

  await expect(modal.locator(".purge-progress")).toContainText("Löschen fehlgeschlagen");
  await expect(modal.locator(".purge-progress")).toContainText("Gelöscht: 2 von 5");
  await expect(modal.locator("[data-purge-close]")).toBeEnabled();
  await modal.locator("[data-purge-close]").click();
  // What went before the failure is gone, so the caller re-reads its view.
  expect(await page.evaluate(() => window.__result)).toBe(2);
  expect(await page.evaluate(() => window.__reloaded)).toBe(true);
  expect(page.__errors, "page errors").toEqual([]);
});

test("a purge can be stopped between two batches, and cannot be closed while one runs", async ({ page }) => {
  await page.locator("#purge-slow").click();
  const modal = page.locator(".purge-modal");
  await expect(modal.locator(".purge-count")).toContainText("5");
  await modal.locator("#purge-confirm").fill("bulk");
  await modal.locator("[data-purge-go]").click();

  await expect(modal.locator("[data-purge-close]")).toBeDisabled();
  await modal.locator("[data-purge-stop]").click();
  await expect(modal.locator(".purge-progress")).toContainText("Angehalten");
  const posts = (await page.evaluate(() => window.__calls)).filter((c) => c.method === "POST");
  expect(posts.length).toBeLessThan(3);
  expect(page.__errors, "page errors").toEqual([]);
});

test("the storage card says compaction is off, asks before compacting once, and reports the pass", async ({ page }) => {
  await page.locator("#storage-off").click();
  await page.waitForFunction(() => window.__storageReady === true);
  const card = page.locator("#storage-card");
  await expect(card.locator(".storage-reclaim")).toContainText("ATLAS_COMPACT_WAL=true");
  // The dashboard reads the checkpoints without verifying them.
  expect(await calls(page)).toContain("GET /api/v1/checkpoints?verify=false");

  // Declined: nothing is asked of the server.
  await page.evaluate(() => { window.__confirmAnswer = false; });
  await card.locator('[data-reclaim="compact"]').click();
  expect(await page.evaluate(() => window.__confirms.length)).toBe(1);
  expect((await calls(page)).filter((c) => c.startsWith("POST"))).toEqual([]);

  await page.evaluate(() => { window.__confirmAnswer = true; });
  await page.locator('#storage-card [data-reclaim="compact"]').click();
  await expect(page.locator("#storage-card .storage-reclaim-result")).toContainText("140 WAL-Segmente entfernt");
  expect((await calls(page)).filter((c) => c.startsWith("POST"))).toEqual(["POST /api/v1/checkpoints?compact=true"]);
  expect(page.__errors, "page errors").toEqual([]);
});

test("a server that compacts on its own offers one button, and it asks nothing", async ({ page }) => {
  await page.locator("#storage-on").click();
  await page.waitForFunction(() => window.__storageReady === true);
  const card = page.locator("#storage-card");
  await expect(card.locator(".storage-reclaim")).toContainText("Takt: 5 Minuten");
  await expect(card.locator('[data-reclaim="compact"]')).toHaveCount(0);
  await card.locator('[data-reclaim="checkpoint"]').click();
  await expect(page.locator("#storage-card .storage-reclaim-result")).toContainText("Checkpoint bei Position 920");
  expect(await page.evaluate(() => window.__confirms.length)).toBe(0);
  expect((await calls(page)).filter((c) => c.startsWith("POST"))).toEqual(["POST /api/v1/checkpoints"]);
  expect(page.__errors, "page errors").toEqual([]);
});

test("the disk is shown even when the checkpoint status never answers", async ({ page }) => {
  await page.locator("#storage-hang").click();
  const card = page.locator("#storage-card");
  await expect(card).toBeVisible();
  await expect(card).toContainText("Davon Atlas");
  await expect(card).toContainText("Segmenten");
  // The reclaim section waits for its own answer and is simply not drawn without one.
  await expect(card.locator(".storage-reclaim")).toHaveCount(0);
  expect(page.__errors, "page errors").toEqual([]);
});
