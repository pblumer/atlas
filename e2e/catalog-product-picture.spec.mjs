// A product's picture, as it is uploaded from the catalogue's product editor
// (ADR-0391, amended 2026-10-26; api/web/picturescale.js).
//
// The route takes at most the asset budget, half a megabyte by default, and a
// photograph of the product off a phone is many times that — so the editor scales a
// raster picture before it uploads it, rather than reporting "the product was saved,
// but its picture was not". What has to hold: a photograph is sent at what the shop
// can show, 600 pixels on its shorter side; an SVG is sent exactly as it was picked;
// a logo stays a lossless PNG; a cut-out with transparency stays a PNG and, when the
// server still finds it too large, is sent again smaller until it is taken; and a
// file the browser cannot read is reported with nothing sent. Drives the real
// catalog-admin.js in catalog-editor-harness.html, like catalog-editor-layout.spec.mjs.
import { test, expect } from "@playwright/test";
import { makeImage, sizeOf, pngHasTransparency } from "./pictures.mjs";

const LIMIT = 512 << 10; // the server's default asset budget

async function open(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.setViewportSize({ width: 1600, height: 800 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.waitForSelector(".product-list tbody tr");
  await page.click('.product-list tbody tr:nth-child(2) button[data-act="edit"]');
  await expect(page.locator(".product-editor .product-form")).toBeVisible();
}

// save picks the file and saves the product as a draft, then returns every upload
// the save made, oldest first, as bytes.
async function save(page, file) {
  await page.locator('.product-editor input[name="picture"]').setInputFiles(file);
  await page.click('.product-editor button[data-publish="no"]');
  await expect.poll(() => page.evaluate(() =>
    window.__sent.some((c) => c.method === "POST" && /\/catalog-products$/.test(c.url)))).toBe(true);
  return sent(page);
}

const sent = async (page) => (await page.evaluate(async () => Promise.all(window.__bytes.map(async (b) => {
  const bytes = new Uint8Array(await b.body.arrayBuffer());
  let s = "";
  for (const x of bytes) s += String.fromCharCode(x);
  return { method: b.method, url: b.url, type: b.body.type, b64: btoa(s) };
})))).map((b) => ({ ...b, body: Buffer.from(b.b64, "base64") }));

const toasts = (page) => page.evaluate(() => window.__toasts);

test("a photograph larger than the limit is scaled to what the shop shows and uploaded", async ({ page }) => {
  await open(page);
  const photo = await makeImage(page, { width: 4000, height: 3000 });
  expect(photo.length).toBeGreaterThan(LIMIT); // the case the server refused

  await save(page, { name: "laptop.jpg", mimeType: "image/jpeg", buffer: photo });
  await expect.poll(async () => (await sent(page)).length).toBe(1);
  const [put] = await sent(page);
  expect(put.method).toBe("PUT");
  expect(put.url).toBe("/api/v1/catalog-products/p2/picture");
  expect(put.type).toBe("image/jpeg");
  expect(put.body.length).toBeLessThan(LIMIT / 2);
  // The shorter side is 600, three times the 200 pixels the shop draws it at.
  expect(sizeOf(put.body)).toEqual({ type: "jpeg", width: 800, height: 600 });
  expect((await toasts(page)).filter((t) => t.kind === "err")).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("an SVG is uploaded exactly as it was picked", async ({ page }) => {
  await open(page);
  const svg = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="4000" height="1000">' +
    '<rect width="4000" height="1000" fill="#0b6e4f"/></svg>');

  await save(page, { name: "vendor.svg", mimeType: "image/svg+xml", buffer: svg });
  await expect.poll(async () => (await sent(page)).length).toBe(1);
  const [put] = await sent(page);
  expect(put.type).toBe("image/svg+xml");
  expect(put.body.equals(svg)).toBe(true);
});

test("a logo stays a lossless PNG, and a banner is held to twice the shop's height", async ({ page }) => {
  await open(page);
  const logo = await makeImage(page, { width: 3000, height: 1000, type: "image/png", flat: true });

  await save(page, { name: "logo.png", mimeType: "image/png", buffer: logo });
  await expect.poll(async () => (await sent(page)).length).toBe(1);
  const [put] = await sent(page);
  expect(put.type).toBe("image/png");
  // The longer side decides here: 1200, not the 1800 the shorter one would allow.
  expect(sizeOf(put.body)).toEqual({ type: "png", width: 1200, height: 400 });
});

test("a cut-out the server still finds too large is sent again, smaller, until it is taken", async ({ page }) => {
  await open(page);
  await page.evaluate(() => { window.__bytesLimit = 200 * 1024; });
  const cut = await makeImage(page, { width: 1600, height: 1600, type: "image/png", transparent: true });

  await save(page, { name: "cut-out.png", mimeType: "image/png", buffer: cut });
  await expect.poll(async () => {
    const all = await sent(page);
    return all.length > 0 && all[all.length - 1].body.length <= 200 * 1024;
  }).toBe(true);
  const all = await sent(page);
  expect(all.length).toBeGreaterThan(1);
  const sides = all.map((p) => sizeOf(p.body).width);
  expect(sides[0]).toBe(600);
  // Each one three quarters of the one before, and every one keeps its transparency.
  for (let i = 1; i < sides.length; i++) expect(sides[i]).toBe(Math.round(600 * 0.75 ** i));
  for (const p of all) expect(p.type).toBe("image/png");
  expect(await pngHasTransparency(page, all[all.length - 1].body)).toBe(true);
  expect((await toasts(page)).filter((t) => t.kind === "err")).toEqual([]);
});

test("a file the browser cannot read is reported, with the product saved and nothing uploaded", async ({ page }) => {
  await open(page);
  await save(page, { name: "not-a-picture.jpg", mimeType: "image/jpeg", buffer: Buffer.from("this is not a JPEG") });
  await expect.poll(async () => (await toasts(page)).map((t) => t.msg).join("\n"))
    .toContain("The product was saved, but its picture was not: this file could not be read as a picture");
  expect(await sent(page)).toEqual([]);
});
