// The handbook's training nuggets (api/web/handbuch.html, "Schulungsnuggets"):
// an animated click-through played in the page. The scenes are data in the
// #nug-data block and the stages are markup, so almost everything here is a
// promise the data makes and the player has to keep.
//
// The failure worth testing for is not "the player crashed" — it is the quiet
// one: a scene points its cursor at a button, the stage changes, and the cursor
// now lands on empty space. The reader sees a click on nothing and learns the
// wrong place to press. Nothing throws, nothing logs, and a screenshot of the
// right frame still looks fine.
import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

const handbook = () => readFileSync(new URL("../api/web/handbuch.html", import.meta.url), "utf8");

function catalogue() {
  const s = handbook();
  const i = s.indexOf('<script type="application/json" id="nug-data">');
  const open = s.indexOf(">", i) + 1;
  return JSON.parse(s.slice(open, s.indexOf("</script>", open)));
}

test("every nugget in the catalogue has a place in the chapter, and the other way round", () => {
  const s = handbook();
  const inPage = [...s.matchAll(/data-nugget="([a-z]+)"/g)].map((m) => m[1]);
  const inData = catalogue().nuggets.map((n) => n.id);
  expect(inData.length).toBeGreaterThan(1);
  // Both directions: a nugget nobody placed is invisible, and a container with
  // no data is an empty box the player silently skips.
  expect([...inData].sort()).toEqual([...inPage].sort());
  // The roles are the ones Atlas actually has (ADR-0209), so a path for a role
  // that does not exist would send a reader looking for a permission nobody can
  // grant them.
  for (const role of ["user", "modeler", "operator", "admin"]) {
    expect(inData).toContain(role);
  }
});

test("every scene carries both languages and a stage", () => {
  for (const nug of catalogue().nuggets) {
    expect(nug.scenes.length, nug.id).toBeGreaterThan(0);
    for (const [i, sc] of nug.scenes.entries()) {
      const where = `${nug.id}#${i}`;
      expect(sc.cap.de, where).toBeTruthy();
      expect(sc.cap.en, where).toBeTruthy();
      // A caption that is the same string in both is a forgotten translation,
      // not a coincidence — these are prose sentences, not labels.
      expect(sc.cap.de, where).not.toBe(sc.cap.en);
      expect(sc.stage, where).toContain("nug-");
      expect(sc.t, where).toBeGreaterThan(2000);
    }
  }
});

test("the player mounts, and nothing plays until asked", async ({ page }) => {
  await page.goto("/handbuch.html");
  const rh = page.locator('[data-nugget="roundhouse"]');
  await expect(rh.locator(".nug-stage")).toHaveCount(1);
  await expect(rh.locator(".nug-steps i")).toHaveCount(catalogue().nuggets[0].scenes.length);
  // Motion nobody asked for is an accessibility problem, so the stage stays on
  // its poster until the play button is pressed.
  await expect(rh.locator(".nug-stage.idle")).toHaveCount(1);
  await expect(rh.locator(".nug-scene")).toHaveCount(0);
});

test("playing advances the scenes and pause stops the clock", async ({ page }) => {
  await page.goto("/handbuch.html");
  const rh = page.locator('[data-nugget="user"]');
  await rh.scrollIntoViewIfNeeded();
  await rh.locator(".nug-play").click();
  await expect(rh.locator(".nug-scene.on")).toHaveCount(1);
  await expect(rh.locator(".nug-steps i.on")).toHaveCount(1);

  await rh.locator('[data-act="next"]').click();
  await expect(rh.locator('.nug-steps i:nth-child(2)')).toHaveClass(/on/);

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
  // Two soundless animations running at once is still two things moving while
  // somebody reads one of them.
  await expect(a.locator('[data-act="toggle"]')).toHaveText("▶");
});

// The one that guards the actual teaching: where a scene says it clicks, the
// cursor has to be. This is checked against the rendered stage rather than the
// data, because the whole failure mode is that the two drift apart.
test("every cursor lands inside the element its scene points at", async ({ page }) => {
  await page.goto("/handbuch.html");
  const misses = await page.evaluate(async () => {
    const data = JSON.parse(document.getElementById("nug-data").textContent);
    const bad = [];
    let checked = 0;
    for (const nug of data.nuggets) {
      const host = document.querySelector(`[data-nugget="${nug.id}"]`);
      host.scrollIntoView();
      for (let i = 0; i < nug.scenes.length; i++) {
        const at = nug.scenes[i].at;
        if (!at) continue;
        checked++;
        host.querySelector(`.nug-steps i[data-i="${i}"]`).click();
        await new Promise((r) => setTimeout(r, 700));
        const tgt = host.querySelector(".nug-scene.on " + at);
        if (!tgt) { bad.push(`${nug.id}#${i}: no element matches ${at}`); continue; }
        const c = host.querySelector(".nug-cursor").getBoundingClientRect();
        const t = tgt.getBoundingClientRect();
        // The pointer's tip, not its box: the arrow is drawn from the top-left.
        const x = c.left + 3, y = c.top + 3;
        if (x < t.left || x > t.right || y < t.top || y > t.bottom) {
          bad.push(`${nug.id}#${i}: cursor outside ${at}`);
        }
      }
    }
    return { bad, checked };
  });
  expect(misses.checked).toBeGreaterThan(5);
  expect(misses.bad).toEqual([]);
});

test("the chapter and its nuggets read in both languages", async ({ page }) => {
  await page.goto("/handbuch.html");
  await expect(page.locator('#toc a[href="#nuggets"]')).toBeVisible();
  const chapter = page.locator("#nuggets");
  for (const [lang, text] of [["de", "Schulungsnuggets"], ["en", "Training nuggets"]]) {
    await page.click(`#lang-${lang}`);
    await expect(chapter.locator(`h2[data-l="${lang}"]`, { hasText: text })).toBeVisible();
    // And the running animation follows the toggle: the caption of a played
    // scene is rendered in both languages, so the page's own CSS switches it.
    const rh = page.locator('[data-nugget="roundhouse"]');
    await rh.scrollIntoViewIfNeeded();
    await rh.locator('.nug-steps i[data-i="0"]').click();
    await expect(rh.locator(`.nug-cap [data-l="${lang}"]`).first()).toBeVisible();
  }
});

// The landscape scene draws edges between nodes, and getting that wrong is
// invisible to every other test here: the picture still renders, the scene still
// advances, no selector breaks. The first version rotated a div by an angle
// computed from percentages — x a share of the width, y a share of the height —
// so on a 745x280 stage an edge came out 22 degrees off and 41px too long and
// ran straight past the node it was supposed to reach.
test("every edge in the landscape ends on the nodes it connects", async ({ page }) => {
  await page.goto("/handbuch.html");
  const result = await page.evaluate(async () => {
    const data = JSON.parse(document.getElementById("nug-data").textContent);
    // Find the scene that draws a mesh, whichever nugget and index it sits at.
    for (const nug of data.nuggets) {
      const i = nug.scenes.findIndex((s) => s.stage.includes("nug-mesh"));
      if (i < 0) continue;
      const host = document.querySelector(`[data-nugget="${nug.id}"]`);
      host.scrollIntoView();
      host.querySelector(`.nug-steps i[data-i="${i}"]`).click();
      await new Promise((r) => setTimeout(r, 700));
      const mesh = host.querySelector(".nug-scene.on .nug-mesh");
      const box = mesh.getBoundingClientRect();
      const nodes = [...mesh.querySelectorAll("i")].map((n) => {
        const b = n.getBoundingClientRect();
        return { x: b.left + b.width / 2, y: b.top + b.height / 2 };
      });
      const near = (p) => nodes.some((n) => Math.hypot(n.x - p.x, n.y - p.y) < 12);
      const bad = [];
      for (const ln of mesh.querySelectorAll("line")) {
        // viewBox is 0..100 on both axes with preserveAspectRatio="none", so a
        // coordinate is a share of the box on its own axis.
        const at = (cx, cy) => ({
          x: box.left + (+ln.getAttribute(cx)) / 100 * box.width,
          y: box.top + (+ln.getAttribute(cy)) / 100 * box.height,
        });
        if (!near(at("x1", "y1"))) bad.push("start of an edge sits on no node");
        if (!near(at("x2", "y2"))) bad.push("end of an edge sits on no node");
      }
      return { edges: mesh.querySelectorAll("line").length, bad };
    }
    return { edges: 0, bad: ["no scene draws a mesh"] };
  });
  expect(result.edges).toBeGreaterThan(2);
  expect(result.bad).toEqual([]);
});
