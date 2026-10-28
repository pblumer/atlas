// Browser-level contract for the atlas hub Console application-Blueprint module: only
// operator-verified API releases are shown, never imported on preview/GET, and
// a matching If-Match digest and explicit confirmation precede ADR-0467 import.
import { test, expect } from "@playwright/test";
import zlib from "node:zlib";

const release = {
  blueprintId: "org.example.purchase", publisherId: "org.example", version: "1.0.0",
  title: "Purchase approvals", description: '<img src=x onerror="alert(1)">', license: "Apache-2.0",
  needs: { workerTypes: [], serverConstants: [] },
};
const digest = "sha256:" + "a".repeat(64);
const index = { schemaVersion: 1, registryId: "org.example.community", releases: [release] };
const preview = {
  ...release, applicationKey: "purchase", applicationName: "Purchase",
  digest, files: [{ Path: "atlas.json", SizeBytes: 80 }],
  catalogues: [{ id: "cat-requests", items: ["badge", "access"] }, { id: "cat-department", items: ["badge"] }],
  products: [{ id: "badge", homeCatalog: "cat-requests" }, { id: "access", homeCatalog: "cat-requests" }],
};

function tarGz(files) {
  const blocks = [];
  for (const [name, content] of files) {
    const data = Buffer.from(content);
    const h = Buffer.alloc(512);
    h.write(name, 0, 100);
    h.write("0000644\0", 100);
    h.write(data.length.toString(8).padStart(11, "0") + "\0", 124);
    h.write("0", 156);
    h.write("ustar\0", 257);
    blocks.push(h, data, Buffer.alloc((512 - data.length % 512) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return zlib.gzipSync(Buffer.concat(blocks));
}
const pkg = tarGz([["atlas.json", JSON.stringify({
  formatVersion: 1, key: "purchase", name: "Purchase", processes: [], forms: [], decisions: [],
})]]);

async function stubRegistry(page, { status = 200, downloadStatus = 200, previewBody = preview } = {}) {
  const requests = [];
  await page.route("**/api/v1/blueprints**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    requests.push({ path, ifMatch: request.headers()["if-match"] });
    if (path === "/api/v1/blueprints") {
      await route.fulfill({ status, contentType: "application/json",
        body: JSON.stringify(status === 200 ? index : { error: "Registry not configured" }) });
    } else if (path.endsWith("/preview")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(previewBody) });
    } else if (path.endsWith("/package")) {
      await route.fulfill(downloadStatus === 200
        ? { status: 200, contentType: "application/gzip", body: pkg }
        : { status: downloadStatus, contentType: "application/json", body: JSON.stringify({ error: "Digest changed" }) });
    } else {
      await route.fulfill({ status: 404, body: "not found" });
    }
  });
  return requests;
}

test.beforeEach(async ({ page }) => {
  await page.goto("/blueprint-marketplace-harness.html");
  await page.waitForFunction(() => window.__ready === true);
});

test("only signed registry metadata is rendered and safe escaped; preview does not import", async ({ page }) => {
  const requests = await stubRegistry(page);
  await page.evaluate(() => window.__open());
  await expect(page.locator(".modal-ov")).toBeVisible();
  await expect(page.locator(".modal-ov h2")).toHaveText("atlas hub");
  await expect(page.locator(".modal-ov")).toContainText("Purchase approvals");
  await expect(page.locator(".modal-ov")).toContainText("<img src=x");
  await expect(page.locator(".modal-ov img")).toHaveCount(0);
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  await expect(page.locator(".modal-ov")).toContainText(digest);
  await expect(page.locator(".modal-ov")).toContainText("cat-requests");
  await expect(page.locator(".modal-ov")).toContainText("cat-department");
  await expect(page.locator(".modal-ov")).toContainText("badge");
  await expect(page.locator(".modal-ov")).toContainText("access");
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
  expect(requests.map((r) => r.path).every((p) => !p.endsWith("/package"))).toBe(true);
  await page.locator("[data-back]").click();
  await page.locator("[data-close]").first().click();
  await expect(page.locator(".modal-ov")).toHaveCount(0);
});

test("untrusted catalogue and offered product names cannot inject markup", async ({ page }) => {
  const hostile = '<img src=x onerror="alert(1)">';
  await stubRegistry(page, {
    previewBody: {
      ...preview,
      catalogues: [{ id: hostile, items: [hostile] }],
      products: [{ id: hostile, homeCatalog: hostile }],
    },
  });
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator(".modal-ov")).toContainText(hostile);
  await expect(page.locator(".modal-ov img")).toHaveCount(0);
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("cancelled operator confirmation never downloads or imports", async ({ page }) => {
  const requests = await stubRegistry(page);
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.locator("[data-install]").click();
  expect(requests.filter((r) => r.path.endsWith("/package"))).toHaveLength(0);
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("explicit confirmation and matching digest reuses existing dry-run and import", async ({ page }) => {
  const requests = await stubRegistry(page);
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator("[data-install]").click();
  await expect(page.locator("#result")).toContainText("ok:");
  const downloaded = requests.filter((r) => r.path.endsWith("/package"));
  expect(downloaded).toHaveLength(1);
  expect(downloaded[0].ifMatch).toBe('"' + digest + '"');
  const calls = await page.evaluate(() => window.__calls);
  expect(calls.map((x) => x.path)).toEqual([
    "/api/v1/applications/source?dryRun=true",
    "/api/v1/applications/source",
  ]);
  expect(await page.evaluate(() => window.__reloads)).toBe(1);
  await expect(page.locator(".modal-ov")).toHaveCount(0);
});

test("changed digest prevents import even when operator confirms", async ({ page }) => {
  await stubRegistry(page, { downloadStatus: 412 });
  await page.evaluate(() => window.__open());
  await page.locator('[data-preview="0"]').click();
  await expect(page.locator("[data-install]")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator("[data-install]").click();
  await expect(page.locator("#result")).toContainText("Digest changed");
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});

test("server without an operator registry shows a failure without import", async ({ page }) => {
  await stubRegistry(page, { status: 503 });
  await page.evaluate(() => window.__open());
  await expect(page.locator(".modal-ov")).toContainText("Registry not configured");
  expect(await page.evaluate(() => window.__calls)).toHaveLength(0);
});
