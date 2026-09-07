// The handbook's training nuggets (api/web/handbuch.html, "Schulungsnuggets"):
// an animated click-through over screenshots of the real Atlas UI.
//
// The stages were drawn once, and that was wrong in a way only a learner would
// have discovered: a drawing has to guess the layout, and it guessed a sidebar
// where Atlas puts its navigation across the top. Somebody who learnt the
// drawing would look in the wrong place in the product. The pictures are now
// captures of the running app, which moves the failure mode: a scene can point
// at a button that has moved, or name an image that is not shipped, and either
// way nothing throws and the page still renders.
import { test, expect } from "@playwright/test";
import { readFileSync, readdirSync } from "node:fs";

const handbook = () => readFileSync(new URL("../api/web/handbuch.html", import.meta.url), "utf8");

function catalogue() {
  const s = handbook();
  const i = s.indexOf('<script type="application/json" id="nug-data">');
  const open = s.indexOf(">", i) + 1;
  return JSON.parse(s.slice(open, s.indexOf("</script>", open)));
}
const shipped = () =>
  readdirSync(new URL("../api/web/nuggets/", import.meta.url))
    .filter((f) => f.endsWith(".webp")).map((f) => f.slice(0, -5));

test("every nugget in the catalogue has a place in the chapter, and the other way round", () => {
  const inPage = [...handbook().matchAll(/data-nugget="([a-z]+)"/g)].map((m) => m[1]);
  const inData = catalogue().nuggets.map((n) => n.id);
  expect(inData.length).toBeGreaterThan(1);
  expect([...inData].sort()).toEqual([...inPage].sort());
  // The roles are the ones Atlas actually has (ADR-0209): a path for a role
  // nobody can be granted would send a reader looking for a permission that
  // does not exist.
  for (const role of ["user", "modeler", "operator", "admin"]) expect(inData).toContain(role);
});

test("every scene names an image that ships, and every shipped image is used", () => {
  const have = new Set(shipped());
  const used = new Set();
  for (const nug of catalogue().nuggets) {
    for (const [i, sc] of nug.scenes.entries()) {
      expect(sc.img, `${nug.id}#${i} has no image`).toBeTruthy();
      expect(have.has(sc.img), `${nug.id}#${i} wants nuggets/${sc.img}.webp, which is not shipped`).toBe(true);
      used.add(sc.img);
    }
  }
  // The other direction keeps dead weight out of the binary: every one of these
  // is embedded via //go:embed web, so an unused shot is shipped to every
  // operator forever.
  expect([...have].filter((h) => !used.has(h))).toEqual([]);
});

test("every scene carries both languages, and every highlight is inside its image", () => {
  for (const nug of catalogue().nuggets) {
    expect(nug.scenes.length, nug.id).toBeGreaterThan(0);
    for (const [i, sc] of nug.scenes.entries()) {
      const where = `${nug.id}#${i}`;
      expect(sc.cap.de, where).toBeTruthy();
      expect(sc.cap.en, where).toBeTruthy();
      // Identical strings are a forgotten translation, not a coincidence:
      // these are prose sentences, not labels.
      expect(sc.cap.de, where).not.toBe(sc.cap.en);
      expect(sc.t, where).toBeGreaterThan(2000);
      // A highlight or cursor outside the frame points at nothing, and looks
      // exactly like one that points at something.
      for (const [k, v] of Object.entries(sc.focus || {})) {
        expect(v, `${where} focus.${k}`).toBeGreaterThanOrEqual(0);
        expect(v, `${where} focus.${k}`).toBeLessThanOrEqual(100);
      }
      if (sc.focus) {
        expect(sc.focus.x + sc.focus.w, `${where} focus runs off the right edge`).toBeLessThanOrEqual(100);
        expect(sc.focus.y + sc.focus.h, `${where} focus runs off the bottom`).toBeLessThanOrEqual(100);
      }
      if (sc.at) {
        expect(sc.at.x, `${where} cursor x`).toBeGreaterThan(0);
        expect(sc.at.x, `${where} cursor x`).toBeLessThan(100);
        expect(sc.at.y, `${where} cursor y`).toBeGreaterThan(0);
        expect(sc.at.y, `${where} cursor y`).toBeLessThan(100);
      }
      // A tap without a cursor animates a click nobody can see.
      if (sc.tap) expect(sc.at, `${where} taps with no cursor`).toBeTruthy();
    }
  }
});

test("the player mounts, and nothing plays until asked", async ({ page }) => {
  await page.goto("/handbuch.html");
  const rh = page.locator('[data-nugget="roundhouse"]');
  await expect(rh.locator(".nug-stage")).toHaveCount(1);
  await expect(rh.locator(".nug-steps i")).toHaveCount(catalogue().nuggets[0].scenes.length);
  // Motion nobody asked for is an accessibility problem, and it would also
  // fetch every screenshot of every nugget on page load.
  await expect(rh.locator(".nug-stage.idle")).toHaveCount(1);
  await expect(rh.locator(".nug-scene")).toHaveCount(0);
});

test("playing loads the real screenshot and advances the scenes", async ({ page }) => {
  await page.goto("/handbuch.html");
  const rh = page.locator('[data-nugget="user"]');
  await rh.scrollIntoViewIfNeeded();
  await rh.locator(".nug-play").click();
  const img = rh.locator(".nug-scene.on img");
  await expect(img).toHaveCount(1);
  await expect(img).toHaveAttribute("src", /^nuggets\/[a-z-]+\.webp$/);
  // naturalWidth is the honest check: a broken src still renders an <img>.
  await expect.poll(() => img.evaluate((e) => e.naturalWidth)).toBeGreaterThan(500);

  await rh.locator('[data-act="next"]').click();
  await expect(rh.locator(".nug-steps i:nth-child(2)")).toHaveClass(/on/);
  await rh.locator('[data-act="toggle"]').click();
  const stopped = await rh.locator(".nug-pos").textContent();
  await page.waitForTimeout(900);
  expect(await rh.locator(".nug-pos").textContent()).toBe(stopped);
});

test("starting one nugget stops the one already running", async ({ page }) => {
  await page.goto("/handbuch.html");
  const a = page.locator('[data-nugget="user"]');
  const b = page.locator('[data-nugget="modeler"]');
  await a.scrollIntoViewIfNeeded();
  await a.locator(".nug-play").click();
  await expect(a.locator('[data-act="toggle"]')).toHaveText("⏸");
  await b.scrollIntoViewIfNeeded();
  await b.locator(".nug-play").click();
  await expect(b.locator('[data-act="toggle"]')).toHaveText("⏸");
  await expect(a.locator('[data-act="toggle"]')).toHaveText("▶");
});

// Every screenshot has to be fetchable at the path the player builds. A 404
// here renders an empty frame with a caption under it, which reads as a slow
// image rather than a missing one.
test("every screenshot the nuggets reference is served", async ({ page }) => {
  const bad = [];
  for (const img of [...new Set(catalogue().nuggets.flatMap((n) => n.scenes.map((s) => s.img)))]) {
    const r = await page.request.get(`/nuggets/${img}.webp`);
    if (!r.ok()) bad.push(`${img}: HTTP ${r.status()}`);
  }
  expect(bad).toEqual([]);
});

test("the chapter and its nuggets read in both languages", async ({ page }) => {
  await page.goto("/handbuch.html");
  await expect(page.locator('#toc a[href="#nuggets"]')).toBeVisible();
  const chapter = page.locator("#nuggets");
  for (const [lang, text] of [["de", "Schulungsnuggets"], ["en", "Training nuggets"]]) {
    await page.click(`#lang-${lang}`);
    await expect(chapter.locator(`h2[data-l="${lang}"]`, { hasText: text })).toBeVisible();
    const rh = page.locator('[data-nugget="roundhouse"]');
    await rh.scrollIntoViewIfNeeded();
    await rh.locator('.nug-steps i[data-i="0"]').click();
    await expect(rh.locator(`.nug-cap [data-l="${lang}"]`).first()).toBeVisible();
  }
});

// The #nug-data block is generated from scripts/nuggets/scenes.mjs by
// scripts/nuggets/capture.mjs, which also takes the screenshots — the two are
// one artifact in two files. Editing the block by hand, or changing scenes.mjs
// without re-running the capture, splits them: the page then plays scenes the
// source no longer describes, and the next capture silently reverts whatever
// was hand-edited. This holds everything the source owns; the coordinates it
// does not, because those are measured from the live UI at capture time.
test("the generated data block still matches scripts/nuggets/scenes.mjs", async () => {
  const src = await import("../scripts/nuggets/scenes.mjs");
  const built = catalogue().nuggets;

  expect(built.map((n) => n.id)).toEqual(src.NUGGETS.map((n) => n.id));
  const shots = new Set(src.SHOTS.map((s) => s.id));

  for (const [i, want] of src.NUGGETS.entries()) {
    const got = built[i];
    expect(got.title, want.id).toEqual(want.title);
    expect(got.lead, want.id).toEqual(want.lead);
    expect(got.scenes.length, `${want.id}: scene count`).toBe(want.scenes.length);

    for (const [k, ws] of want.scenes.entries()) {
      const gs = got.scenes[k];
      const where = `${want.id}#${k}`;
      expect(gs.t, where).toBe(ws.t);
      expect(gs.img, where).toBe(ws.img);
      expect(gs.cap, where).toEqual(ws.cap);
      expect(shots.has(ws.img), `${where}: ${ws.img} is in no SHOT`).toBe(true);
      // A focus in the source has to have produced a measured rectangle, and a
      // scene with no focus must not have gained one.
      if (ws.focus) {
        expect(gs.focus, `${where}: focus "${ws.focus}" was never measured`).toBeTruthy();
        const shot = src.SHOTS.find((s) => s.id === ws.img);
        expect(Object.keys(shot.targets || {}), `${where}: ${ws.img} declares no target "${ws.focus}"`)
          .toContain(ws.focus);
      } else {
        expect(gs.focus, `${where} has a highlight the source does not ask for`).toBeUndefined();
      }
      expect(!!gs.tap, where).toBe(!!ws.tap);
    }
  }
});
