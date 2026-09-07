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

// ---- full screen, for showing a nugget to a room ----
// The shots are 1200x703 and screens are not, so the full-screen stage keeps
// the picture's own ratio instead of filling the screen. That is the whole
// point of the following test: the highlight and the cursor are percentages of
// the *stage*, so a stage wider than the picture inside it puts the ring beside
// the button instead of on it — which is what the first build did, with the
// rectangle hanging off the left edge. Nothing throws when that happens; the
// ring simply points at nothing, in front of an audience.
async function frames(host) {
  return await host.evaluate((h) => {
    const img = h.querySelector(".nug-scene.on img");
    const hi = h.querySelector(".nug-hi");
    const r = (e) => { const b = e.getBoundingClientRect(); return { x: b.x, y: b.y, w: b.width, h: b.height }; };
    return { img: r(img), hi: hi.hidden ? null : r(hi), natural: img.naturalWidth / img.naturalHeight };
  });
}

test("full screen keeps the highlight on the picture, at every screen shape", async ({ page }) => {
  await page.goto("/handbuch.html");
  const nug = page.locator('[data-nugget="user"]');
  await nug.scrollIntoViewIfNeeded();
  // The button is only offered where the browser allows it, so its absence
  // here would mean the capability check itself is broken.
  await expect(nug.locator('[data-act="full"]')).toHaveCount(1);
  await nug.locator('[data-act="full"]').click();
  await expect.poll(() => page.evaluate(() => !!document.fullscreenElement)).toBe(true);
  // Somebody who went full screen is presenting: the first scene runs without
  // a second click.
  await expect(nug.locator('[data-act="toggle"]')).toHaveText("⏸");

  for (const size of [{ width: 1280, height: 720 }, { width: 1024, height: 768 }]) {
    await page.setViewportSize(size);
    await nug.locator('.nug-steps i[data-i="0"]').click();
    const { img, hi, natural } = await frames(nug);
    const where = `${size.width}x${size.height}`;
    expect(hi, `${where}: scene 0 lost its highlight`).toBeTruthy();
    expect(img.w, `${where}: no picture`).toBeGreaterThan(200);
    // This assertion has to come first, and it carries more than it looks like.
    // The rectangles below are the <img> element's box, and an element box is
    // not the visible picture: under object-fit:contain the box still fills its
    // stage while the picture is letterboxed inside it, so a ring "inside the
    // box" can still sit on grey. The two coincide only when the box already
    // carries the picture's own ratio — which is exactly what the full-screen
    // stage is shaped for, and what this checks. It also rules out the other
    // way to fill a stage of the wrong shape: stretching the screenshot.
    expect(img.w / img.h, `${where}: the screenshot is stretched`).toBeCloseTo(natural, 2);
    // Half a pixel of slack for the browser's own rounding; anything more is
    // the ring sitting outside the frame.
    expect(hi.x, `${where}: ring starts left of the picture`).toBeGreaterThanOrEqual(img.x - 0.5);
    expect(hi.y, `${where}: ring starts above the picture`).toBeGreaterThanOrEqual(img.y - 0.5);
    expect(hi.x + hi.w, `${where}: ring runs off the right edge`).toBeLessThanOrEqual(img.x + img.w + 0.5);
    expect(hi.y + hi.h, `${where}: ring runs off the bottom`).toBeLessThanOrEqual(img.y + img.h + 0.5);
  }
});

test("in full screen the keyboard drives the nugget, and only there", async ({ page }) => {
  await page.goto("/handbuch.html");
  const nug = page.locator('[data-nugget="user"]');
  await nug.scrollIntoViewIfNeeded();
  await nug.locator(".nug-play").click();
  await nug.locator('[data-act="toggle"]').click();          // pause, so nothing advances on its own
  await expect(nug.locator('[data-act="toggle"]')).toHaveText("▶");

  // Outside full screen the arrows belong to the page. Binding them anyway
  // would take scrolling away from anyone reading the handbook.
  await nug.evaluate((h) => h.focus());
  await page.keyboard.press("ArrowRight");
  await expect(nug.locator('.nug-steps i[data-i="0"]')).toHaveClass(/on/);

  await nug.locator('[data-act="full"]').click();
  await expect.poll(() => page.evaluate(() => !!document.fullscreenElement)).toBe(true);
  await page.keyboard.press("ArrowRight");
  await expect(nug.locator('.nug-steps i[data-i="1"]')).toHaveClass(/on/);
  await page.keyboard.press("ArrowLeft");
  await expect(nug.locator('.nug-steps i[data-i="0"]')).toHaveClass(/on/);
  await page.keyboard.press("End");
  const last = catalogue().nuggets.find((n) => n.id === "user").scenes.length - 1;
  await expect(nug.locator(`.nug-steps i[data-i="${last}"]`)).toHaveClass(/on/);
  await page.keyboard.press("Home");
  await expect(nug.locator('.nug-steps i[data-i="0"]')).toHaveClass(/on/);
  await page.keyboard.press(" ");
  await expect(nug.locator('[data-act="toggle"]')).toHaveText("⏸");
  await page.keyboard.press(" ");
  await expect(nug.locator('[data-act="toggle"]')).toHaveText("▶");
});

// The full-screen surround is dark, and the page around it is not: anything in
// a caption that carries its own background from the light theme comes along
// unchanged. The role nuggets say their role as <code>, so this is not a corner
// case — it is the one word the scene is about, and it went light-on-light in
// the first build.
test("full screen leaves nothing in the caption unreadable", async ({ page }) => {
  await page.goto("/handbuch.html");
  const nug = page.locator('[data-nugget="user"]');
  await nug.scrollIntoViewIfNeeded();
  await nug.locator('[data-act="full"]').click();
  await expect.poll(() => page.evaluate(() => !!document.fullscreenElement)).toBe(true);
  await nug.locator('.nug-steps i[data-i="0"]').click();

  const worst = await nug.evaluate((h) => {
    // WCAG relative luminance, so "readable" is a number rather than a look.
    const lum = (c) => {
      const [r, g, b] = c.match(/[\d.]+/g).slice(0, 3).map((v) => {
        const x = v / 255;
        return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * r + 0.7152 * g + 0.0722 * b;
    };
    const ratio = (a, b) => {
      const [x, y] = [lum(a), lum(b)].sort((m, n) => n - m);
      return (x + 0.05) / (y + 0.05);
    };
    const bg = (e) => {
      for (let n = e; n; n = n.parentElement) {
        const c = getComputedStyle(n).backgroundColor;
        if (c && !/rgba\(0, 0, 0, 0\)|transparent/.test(c)) return c;
      }
      return "rgb(255, 255, 255)";
    };
    let low = { sel: "(nichts)", r: Infinity };
    for (const el of h.querySelectorAll(".nug-cap, .nug-cap *, .nug-head, .nug-head *")) {
      if (!el.textContent.trim()) continue;
      const cs = getComputedStyle(el);
      const r = ratio(cs.color, bg(el));
      if (r < low.r) low = { sel: el.tagName.toLowerCase() + "." + (el.className || "-"), r };
    }
    return low;
  });
  // 4.5:1 is the WCAG AA threshold for body text; the failure this guards
  // against scored about 1.05.
  expect(worst.r, `${worst.sel} has a contrast of ${worst.r.toFixed(2)}:1`).toBeGreaterThan(4.5);
});

// Escape is the usual way out of full screen, and it does not pass through the
// page. A nugget that kept running after it would go on animating behind
// whatever the presenter switched to.
test("leaving full screen stops the run", async ({ page }) => {
  await page.goto("/handbuch.html");
  const nug = page.locator('[data-nugget="operator"]');
  await nug.scrollIntoViewIfNeeded();
  await nug.locator('[data-act="full"]').click();
  await expect.poll(() => page.evaluate(() => !!document.fullscreenElement)).toBe(true);
  await expect(nug.locator('[data-act="toggle"]')).toHaveText("⏸");
  await page.evaluate(() => document.exitFullscreen());
  await expect.poll(() => page.evaluate(() => !!document.fullscreenElement)).toBe(false);
  await expect(nug.locator('[data-act="toggle"]')).toHaveText("▶");
  const stopped = await nug.locator(".nug-pos").textContent();
  await page.waitForTimeout(900);
  expect(await nug.locator(".nug-pos").textContent()).toBe(stopped);
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
