// A person's picture, as it is uploaded from Console → Organization (ADR-0368,
// amended 2026-10-26; api/web/picturescale.js).
//
// The route takes at most the asset budget, half a megabyte by default, and a
// photograph straight off a phone is many times that — so the page scales the
// picture down before it uploads it, rather than passing on the server's refusal.
// What has to hold: what is PUT is small, at the size the circle can use and never
// enlarged; a photograph stays a JPEG and a picture with transparency stays a PNG;
// the file's orientation is applied and everything else the file carried beside its
// pixels is gone; and a file the browser cannot read is refused here, with nothing
// sent. Drives the real app shell against a mock of the users endpoints, like
// user-line-manager.spec.mjs.
import { test, expect } from "@playwright/test";

const LIMIT = 512 << 10; // the server's default asset budget

const ROSTER = [
  { id: "usr_1", username: "root", displayName: "Root", roles: ["admin"], disabled: false },
  { id: "usr_2", username: "mia", displayName: "Mia Keller", roles: ["user"], disabled: false },
];

function installMock(page) {
  const state = { puts: [] };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "usr_1", username: "root", roles: ["admin"] } } });
    }
    if (path.endsWith("/avatar")) {
      if (req.method() === "PUT") {
        state.puts.push({ path, type: req.headers()["content-type"], body: req.postDataBuffer() });
        return route.fulfill({ status: 204, body: "" });
      }
      return route.fulfill({ status: 404, json: { error: "this account has no picture" } });
    }
    if (path.endsWith("/users")) return route.fulfill({ json: ROSTER });
    return route.fulfill({ json: [] });
  });
  return state;
}

async function editUser(page, id) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await expect(page.locator("#user-rows tr")).toHaveCount(ROSTER.length);
  await page.locator(`#user-rows tr[data-id="${id}"] button[data-act="edit"]`).click();
  await expect(page.locator(".avatar-field")).toBeVisible();
}

// makeImage draws a picture in the page and returns its bytes. Noise, because noise
// is what a JPEG cannot compress: it is how a few hundred kilobytes of pixels become
// a file the size a camera writes. Optionally with an Exif segment carrying an
// orientation and a marker the stored picture must not carry on.
async function makeImage(page, { width, height, type = "image/jpeg", transparent = false, orientation = 0, marker = "" }) {
  const b64 = await page.evaluate(async ({ width, height, type, transparent, orientation, marker }) => {
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext("2d");
    const img = ctx.createImageData(width, height);
    let seed = 2463534242;
    for (let i = 0; i < img.data.length; i += 4) {
      seed ^= seed << 13; seed ^= seed >>> 17; seed ^= seed << 5;
      img.data[i] = seed & 255;
      img.data[i + 1] = (seed >>> 8) & 255;
      img.data[i + 2] = (seed >>> 16) & 255;
      // A transparent picture is see-through on its left half.
      img.data[i + 3] = transparent && ((i / 4) % width) < width / 2 ? 0 : 255;
    }
    ctx.putImageData(img, 0, 0);
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, type, 0.95));
    let bytes = Array.from(new Uint8Array(await blob.arrayBuffer()));
    if (orientation) {
      // TIFF, big-endian: one IFD entry, Orientation (0x0112) as a SHORT, then the
      // marker in bytes no entry points at.
      const enc = new TextEncoder();
      const tiff = [0x4d, 0x4d, 0x00, 0x2a, 0, 0, 0, 8, 0, 1,
        0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, orientation, 0, 0, 0, 0, 0, 0, ...enc.encode(marker)];
      const payload = [...enc.encode("Exif"), 0, 0, ...tiff];
      const len = payload.length + 2;
      bytes = [bytes[0], bytes[1], 0xff, 0xe1, len >> 8, len & 255, ...payload, ...bytes.slice(2)];
    }
    let s = "";
    for (const b of bytes) s += String.fromCharCode(b);
    return btoa(s);
  }, { width, height, type, transparent, orientation, marker });
  return Buffer.from(b64, "base64");
}

// sizeOf reads a PNG's or a baseline/progressive JPEG's dimensions off its header.
function sizeOf(buf) {
  if (buf[0] === 0x89 && buf.toString("ascii", 1, 4) === "PNG") {
    return { type: "png", width: buf.readUInt32BE(16), height: buf.readUInt32BE(20) };
  }
  let i = 2;
  while (i < buf.length) {
    const marker = buf[i + 1];
    const len = buf.readUInt16BE(i + 2);
    if (marker >= 0xc0 && marker <= 0xc2) {
      return { type: "jpeg", height: buf.readUInt16BE(i + 5), width: buf.readUInt16BE(i + 7) };
    }
    i += 2 + len;
  }
  throw new Error("no frame header");
}

// pngHasTransparency asks the page, which has the decoder.
async function pngHasTransparency(page, buf) {
  return page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const bmp = await createImageBitmap(new Blob([bytes], { type: "image/png" }));
    const canvas = document.createElement("canvas");
    canvas.width = bmp.width;
    canvas.height = bmp.height;
    const ctx = canvas.getContext("2d");
    ctx.drawImage(bmp, 0, 0);
    const { data } = ctx.getImageData(0, 0, bmp.width, bmp.height);
    for (let i = 3; i < data.length; i += 4) if (data[i] < 255) return true;
    return false;
  }, buf.toString("base64"));
}

async function upload(page, state, file) {
  const before = state.puts.length;
  await page.locator(".avatar-file").setInputFiles(file);
  await page.locator('.avatar-field button[data-avact="upload"]').click();
  await expect.poll(() => state.puts.length).toBe(before + 1);
  return state.puts[before];
}

test("a photograph larger than the limit is scaled down and uploaded", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_2");
  const photo = await makeImage(page, { width: 3000, height: 2000 });
  expect(photo.length).toBeGreaterThan(LIMIT); // the case the server refused

  const put = await upload(page, state, { name: "IMG_0412.jpg", mimeType: "image/jpeg", buffer: photo });
  expect(put.path).toBe("/api/v1/users/usr_2/avatar");
  expect(put.type).toBe("image/jpeg");
  expect(put.body.length).toBeLessThan(LIMIT / 4);
  // The shorter side is 256; the longer keeps the proportion.
  expect(sizeOf(put.body)).toEqual({ type: "jpeg", width: 384, height: 256 });
  await expect(page.locator("#toast")).toContainText("Picture updated");
});

test("the orientation is applied and nothing else in the file is kept", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_2");
  // Stored landscape, tagged "rotate 90° clockwise": a portrait, as a phone writes one.
  const photo = await makeImage(page, { width: 600, height: 400, orientation: 6, marker: "GPS 47.37N 8.54E" });

  const put = await upload(page, state, { name: "portrait.jpg", mimeType: "image/jpeg", buffer: photo });
  expect(sizeOf(put.body)).toEqual({ type: "jpeg", width: 256, height: 384 });
  expect(put.body.includes("GPS 47.37N")).toBe(false);
  expect(put.body.includes("Exif")).toBe(false);
});

test("a small picture is not enlarged", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_2");
  const small = await makeImage(page, { width: 120, height: 90 });

  const put = await upload(page, state, { name: "small.jpg", mimeType: "image/jpeg", buffer: small });
  expect(sizeOf(put.body)).toEqual({ type: "jpeg", width: 120, height: 90 });
});

test("a picture with transparency stays a PNG, an opaque one becomes a JPEG", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_2");

  const cut = await makeImage(page, { width: 800, height: 800, type: "image/png", transparent: true });
  let put = await upload(page, state, { name: "cut-out.png", mimeType: "image/png", buffer: cut });
  expect(put.type).toBe("image/png");
  expect(sizeOf(put.body)).toEqual({ type: "png", width: 256, height: 256 });
  expect(await pngHasTransparency(page, put.body)).toBe(true);

  await page.locator('#user-rows tr[data-id="usr_2"] button[data-act="edit"]').click();
  const flat = await makeImage(page, { width: 800, height: 800, type: "image/png" });
  put = await upload(page, state, { name: "screenshot.png", mimeType: "image/png", buffer: flat });
  expect(put.type).toBe("image/jpeg");
  expect(sizeOf(put.body)).toEqual({ type: "jpeg", width: 256, height: 256 });
});

test("a file the browser cannot read as a picture is refused here and nothing is sent", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_2");
  await page.locator(".avatar-file").setInputFiles({
    name: "not-a-picture.jpg", mimeType: "image/jpeg", buffer: Buffer.from("this is not a JPEG"),
  });
  await page.locator('.avatar-field button[data-avact="upload"]').click();
  await expect(page.locator("#toast")).toContainText("could not be read as a picture");
  expect(state.puts).toHaveLength(0);
});
