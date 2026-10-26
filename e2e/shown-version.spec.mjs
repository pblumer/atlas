// End-to-end coverage for the Modeler's "what is open" chip and for Deploy saving the
// draft first (api/web/shown-version.js, api/web/editor.js;
// ADR-0452).
//
// The report this answers: a change was deployed, and the diagram an application then
// opened did not have it — with nothing on screen to say whether the Modeler showed an
// old version. A draft and a deployed version are separate records; the chip says which
// one is open and how it stands against the other, and the panel names who created and
// who last changed it. Deploy from a draft now saves it first, so the two cannot part
// that way again.
import { test, expect } from "@playwright/test";

const T0 = 1700000000;

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/shown-version-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test.afterEach(async ({ page }) => {
  expect(page.__errors, "the editor must not throw").toEqual([]);
});

const setState = (page, state) => page.evaluate((s) => Object.assign(window.__state, s), state);

// oneRow is the editor-bar spec's check: the bar must not wrap because of the chip.
const oneRow = (page) => page.locator(".editor-bar").evaluate((bar) => {
  const mids = [...bar.children]
    .filter((c) => c.offsetParent !== null)
    .map((c) => { const r = c.getBoundingClientRect(); return Math.round((r.top + r.bottom) / 2); });
  return new Set(mids).size;
});

test("a draft missing a newer deployed change says so, and names who to ask", async ({ page }) => {
  await setState(page, {
    comparison: {
      processId: "order", ownerName: "Alice Adams", savedAt: T0, savedByName: "Bob Brown",
      deployed: true, key: 2251799813685250, version: 2, latestVersion: 2,
      deployedAt: T0 + 3600, deployedByName: "Carol Clark",
      sameModel: false, difference: '<serviceTask id="t"> differs',
    },
  });
  await page.evaluate(() => window.__mountDraft());

  const chip = page.locator("#sv-chip");
  await expect(chip).toHaveText("Entwurf ≠ v2");
  await expect(chip).toHaveClass(/warn/);
  expect(await oneRow(page), "the chip must not push the bar into a second row").toBe(1);

  await chip.click();
  const pop = page.locator("#sv-pop");
  await expect(pop).toBeVisible();
  await expect(chip).toHaveAttribute("aria-expanded", "true");
  await expect(pop).toContainText("Alice Adams");
  await expect(pop).toContainText("Bob Brown");
  await expect(pop).toContainText("Carol Clark");
  await expect(pop.locator(".sv-note.warn")).toContainText("deployt, nachdem dieser Entwurf zuletzt gespeichert wurde");
  await expect(pop.locator(".sv-note.warn")).toContainText('<serviceTask id="t"> differs');
  await expect(pop.locator("a", { hasText: "v2 öffnen" })).toHaveAttribute("href", "#/modeler/d/2251799813685250");

  // It closes on an outside click and on Escape, like the bar's menu.
  await page.locator("#canvas").click({ position: { x: 20, y: 20 } });
  await expect(pop).toBeHidden();
  await chip.click();
  await expect(pop).toBeVisible();
  await chip.press("Escape");
  await expect(pop).toBeHidden();
  await expect(chip).toBeFocused();
});

test("the chip never starts a second row: short of room it is cut, not wrapped", async ({ page }) => {
  // The bar has only a few dozen pixels to spare at 900px, and fewer with a wider system
  // font — CI's runner wrapped a label the local fonts fitted. So the room is made
  // scarce on purpose, whatever the font: the bar is padded until only 60px are free
  // without the chip, and the chip's label is wider than that.
  await setState(page, {
    comparison: { processId: "order", savedAt: T0, deployed: false, sameModel: false },
  });
  await page.evaluate(() => window.__mountDraft());
  const chip = page.locator("#sv-chip");
  await expect(chip).toHaveText("Entwurf · nicht deployt");
  await page.evaluate(() => {
    const bar = document.querySelector(".editor-bar");
    const host = document.querySelector("#shown-version");
    const spacer = [...bar.children].find((c) => c.getAttribute("style") === "flex:1");
    host.style.display = "none";
    const free = spacer.getBoundingClientRect().width;
    bar.style.paddingRight = `${10 + free - 60}px`;
    host.style.display = "";
  });
  expect(await oneRow(page), "a chip short of room must not push the bar into a second row").toBe(1);
  const fit = await chip.evaluate((c) => ({ shown: c.clientWidth, needs: c.scrollWidth }));
  expect(fit.shown, "the chip must still be there to click").toBeGreaterThan(20);
  expect(fit.shown, "the label is cut to the room there is").toBeLessThan(fit.needs);
  // Cut is not lost: the whole label is the chip's title's start, and the panel opens.
  await chip.click();
  await expect(page.locator("#sv-pop")).toBeVisible();
});

test("a draft that is the deployed model reads as that version", async ({ page }) => {
  await setState(page, {
    comparison: {
      processId: "order", savedAt: T0, deployed: true, key: 7, version: 1, latestVersion: 1,
      deployedAt: T0 + 5, sameModel: true,
    },
  });
  await page.evaluate(() => window.__mountDraft());
  await expect(page.locator("#sv-chip")).toHaveText("Entwurf = v1");
  await expect(page.locator("#sv-chip")).toHaveClass(/ok/);
  await page.locator("#sv-chip").click();
  // Nobody recorded is said as such, not left blank.
  await expect(page.locator("#sv-pop")).toContainText("nicht erfasst");
});

test("a draft with undeployed changes is not a warning", async ({ page }) => {
  await setState(page, {
    comparison: {
      processId: "order", savedAt: T0 + 100, deployed: true, key: 7, version: 1, latestVersion: 1,
      deployedAt: T0, sameModel: false, difference: "x",
    },
  });
  await page.evaluate(() => window.__mountDraft());
  await expect(page.locator("#sv-chip")).toHaveText("Entwurf · v1 + Änderungen");
  await expect(page.locator("#sv-chip")).not.toHaveClass(/warn/);
  expect(await oneRow(page)).toBe(1);
});

test("a deployed version says which of how many, who deployed it, and how the draft stands", async ({ page }) => {
  await setState(page, {
    processes: [
      { key: 41, processId: "order", version: 1, deployedAt: T0, deployedByName: "Alice Adams" },
      { key: 42, processId: "order", version: 2, deployedAt: T0 + 60, deployedByName: "Bob Brown",
        diagramUpdatedAt: T0 + 120, diagramUpdatedBy: "usr_1", diagramUpdatedByName: "Dana Dent" },
      { key: 43, processId: "order", version: 3, deployedAt: T0 + 180, deployedByName: "Carol Clark" },
      { key: 90, processId: "invoice", version: 9, deployedAt: T0 },
    ],
    comparison: {
      processId: "order", savedAt: T0 + 30, savedByName: "Alice Adams", deployed: true,
      key: 42, version: 2, latestVersion: 3, deployedAt: T0 + 60, sameModel: false, difference: "y",
    },
  });
  await page.evaluate(() => window.__mountVersion(42));

  const chip = page.locator("#sv-chip");
  await expect(chip).toHaveText("v2 von 3");
  await expect(chip).toHaveClass(/warn/);
  expect(await oneRow(page)).toBe(1);
  // The draft is compared with the version that is open, not with the newest.
  await expect.poll(() => page.evaluate(() =>
    window.__writes.some((w) => w.url === "/api/v1/drafts/order/comparison?key=42"))).toBe(true);

  await chip.click();
  const pop = page.locator("#sv-pop");
  await expect(pop).toContainText("Deployte Version 2 von 3");
  await expect(pop).toContainText("Bob Brown");
  await expect(pop).toContainText("Dana Dent");
  await expect(pop).toContainText("ein anderes Modell als v2");
  await expect(pop.locator("a", { hasText: "v3 öffnen" })).toHaveAttribute("href", "#/modeler/d/43");
  await expect(pop.locator("a", { hasText: "Den Entwurf öffnen" })).toHaveAttribute("href", "#/modeler/draft/order");
});

test("the newest version without a draft says so", async ({ page }) => {
  await setState(page, {
    processes: [{ key: 43, processId: "order", version: 3, deployedAt: T0 }],
    comparison: null,
  });
  await page.evaluate(() => window.__mountVersion(43));
  await expect(page.locator("#sv-chip")).toHaveText("v3 · neueste");
  await page.locator("#sv-chip").click();
  await expect(page.locator("#sv-pop")).toContainText("Keiner — Speichern macht aus dieser Version einen");
});

test("a draft that cannot be read is not passed off as no draft", async ({ page }) => {
  await setState(page, {
    processes: [{ key: 43, processId: "order", version: 3, deployedAt: T0 }],
    comparisonStatus: 500,
  });
  await page.evaluate(() => window.__mountVersion(43));
  await page.locator("#sv-chip").click();
  await expect(page.locator("#sv-pop")).toContainText("Liess sich nicht lesen");
  await expect(page.locator("#sv-pop")).not.toContainText("Keiner");
});

test("Deploy from a draft saves the draft first, and only once", async ({ page }) => {
  await setState(page, {
    comparison: { processId: "order", savedAt: T0, deployed: false, sameModel: false },
  });
  await page.evaluate(() => window.__mountDraft());
  await expect(page.locator("#sv-chip")).toHaveText("Entwurf · nicht deployt");

  const writes = () => page.evaluate(() =>
    window.__writes.filter((w) => w.method === "POST").map((w) => w.url.split("?")[0]));

  await page.locator("#deploy").click();
  await expect(page.locator("#deploy-from-version")).toHaveCount(0);
  await page.locator("#deploy-only").click();
  await expect(page.locator(".deploy-success")).toBeVisible();
  expect(await writes()).toEqual(["/api/v1/drafts", "/api/v1/deployments"]);

  // Nothing changed since: a second deploy has nothing new to save.
  await page.locator("#deploy").click();
  await page.locator("#deploy").click();
  await page.locator("#deploy-only").click();
  await expect.poll(writes).toEqual(["/api/v1/drafts", "/api/v1/deployments", "/api/v1/deployments"]);
});

test("Deploy from a version opened read-only leaves the draft alone, and says so first", async ({ page }) => {
  await setState(page, {
    processes: [{ key: 43, processId: "order", version: 3, deployedAt: T0 }],
  });
  await page.evaluate(() => window.__mountVersion(43));
  await page.locator("#deploy").click();
  await expect(page.locator("#deploy-from-version")).toContainText("lässt den Entwurf, wie er ist");
  await page.locator("#deploy-only").click();
  await expect(page.locator(".deploy-success")).toBeVisible();
  const posts = await page.evaluate(() => window.__writes.filter((w) => w.method === "POST").map((w) => w.url));
  expect(posts).toEqual(["/api/v1/deployments"]);
});

test("a new diagram is not saved yet, and its first deploy saves it", async ({ page }) => {
  await page.evaluate(() => window.__mountNew());
  await expect(page.locator("#sv-chip")).toHaveText("Nicht gespeichert");
  await page.locator("#deploy").click();
  await page.locator("#deploy-only").click();
  await expect(page.locator(".deploy-success")).toBeVisible();
  const posts = await page.evaluate(() =>
    window.__writes.filter((w) => w.method === "POST").map((w) => w.url.split("?")[0]));
  expect(posts).toEqual(["/api/v1/drafts", "/api/v1/deployments"]);
  // The session now addresses the draft it saved, so the chip asks about that draft.
  await expect.poll(() => page.evaluate(() =>
    window.__writes.some((w) => w.url === "/api/v1/drafts/order/comparison"))).toBe(true);
});
