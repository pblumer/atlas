// A person's picture, as it is uploaded from Console → Organization (ADR-0368,
// amended 2026-10-26; api/web/picturescale.js).
//
// The route takes at most the asset budget, half a megabyte by default, and a
// photograph straight off a phone is many times that — so the page scales the
// picture down before it uploads it, rather than passing on the server's refusal.
// What has to hold: what is PUT is small, at the size the circle can use and never
// enlarged; a photograph stays a JPEG and a picture with transparency stays a PNG;
// the file's orientation is applied and everything else the file carried beside its
// pixels is gone; a server that still finds it too large is asked again with a
// smaller one; and a file the browser cannot read is refused here, with nothing
// sent. Drives the real app shell against a mock of the users endpoints, like
// user-line-manager.spec.mjs.
import { test, expect } from "@playwright/test";
import { makeImage, sizeOf, pngHasTransparency } from "./pictures.mjs";

const LIMIT = 512 << 10; // the server's default asset budget

const ROSTER = [
  { id: "usr_1", username: "root", displayName: "Root", roles: ["admin"], disabled: false },
  { id: "usr_2", username: "mia", displayName: "Mia Keller", roles: ["user"], disabled: false },
];

// installMock answers like the server. A spec that sets state.limit makes the PUT
// refuse a larger body with the server's 413, which is how the page learns the limit.
function installMock(page) {
  const state = { puts: [], limit: Infinity };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "usr_1", username: "root", roles: ["admin"] } } });
    }
    if (path.endsWith("/avatar")) {
      if (req.method() === "PUT") {
        const body = req.postDataBuffer();
        state.puts.push({ path, type: req.headers()["content-type"], body });
        if (body.length > state.limit) {
          return route.fulfill({ status: 413, json: { error: `the picture exceeds the ${state.limit} byte limit` } });
        }
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

test("a picture with transparency stays a PNG, a photograph saved as one becomes a JPEG, a logo stays a PNG",
  async ({ page }) => {
    const state = installMock(page);
    await editUser(page, "usr_2");
    const again = () => page.locator('#user-rows tr[data-id="usr_2"] button[data-act="edit"]').click();

    const cut = await makeImage(page, { width: 800, height: 800, type: "image/png", transparent: true });
    let put = await upload(page, state, { name: "cut-out.png", mimeType: "image/png", buffer: cut });
    expect(put.type).toBe("image/png");
    expect(sizeOf(put.body)).toEqual({ type: "png", width: 256, height: 256 });
    expect(await pngHasTransparency(page, put.body)).toBe(true);

    await again();
    const photo = await makeImage(page, { width: 800, height: 800, type: "image/png" });
    put = await upload(page, state, { name: "photo.png", mimeType: "image/png", buffer: photo });
    expect(put.type).toBe("image/jpeg");
    expect(sizeOf(put.body)).toEqual({ type: "jpeg", width: 256, height: 256 });

    // Lossless, at a cost the browser's own PNG encoder keeps modest: a JPEG would
    // only add fringes to its edges.
    await again();
    const logo = await makeImage(page, { width: 800, height: 800, type: "image/png", flat: true });
    put = await upload(page, state, { name: "logo.png", mimeType: "image/png", buffer: logo });
    expect(put.type).toBe("image/png");
    expect(sizeOf(put.body)).toEqual({ type: "png", width: 256, height: 256 });
  });

test("a server that refuses every size gets four smaller ones, and then its own words are shown",
  async ({ page }) => {
    const state = installMock(page);
    state.limit = 100;
    await editUser(page, "usr_2");
    const photo = await makeImage(page, { width: 3000, height: 2000 });
    await page.locator(".avatar-file").setInputFiles({ name: "IMG_0413.jpg", mimeType: "image/jpeg", buffer: photo });
    await page.locator('.avatar-field button[data-avact="upload"]').click();

    await expect(page.locator("#toast")).toContainText("the picture exceeds the 100 byte limit");
    expect(state.puts).toHaveLength(5);
    // Each one three quarters of the one before.
    expect(state.puts.map((p) => sizeOf(p.body).height)).toEqual([256, 192, 144, 108, 81]);
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
