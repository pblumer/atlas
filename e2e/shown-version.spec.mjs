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

// The chip's panel opens the whole list of differences, not only the first one, and
// on a version the comparison with any other version (ADR-0530).
test("a draft that differs opens every difference against the version", async ({ page }) => {
  await setState(page, {
    comparison: {
      processId: "order", savedAt: T0, deployed: true, key: 42, version: 2, latestVersion: 2,
      deployedAt: T0 + 3600, sameModel: false, difference: '<serviceTask id="t"> differs',
      counts: { added: 1, removed: 0, changed: 1 },
      changes: [
        { kind: "changed", id: "t", type: "serviceTask", name: "Versenden", parent: "order",
          details: [{ field: "name", kind: "changed", before: "Senden", after: "Versenden" }] },
        { kind: "added", id: "t2", type: "userTask", name: "Prüfen", parent: "order" },
      ],
    },
  });
  await page.evaluate(() => window.__mountDraft());
  await page.locator("#sv-chip").click();
  await page.locator("#sv-pop button", { hasText: "Alle Unterschiede anzeigen" }).click();

  const modal = page.locator(".vc-modal");
  await expect(modal.locator("h2")).toHaveText("Entwurf gegenüber v2");
  await expect(modal.locator(".vc-summary")).toHaveText("1 neu · 1 geändert");
  await expect(modal.locator(".vc-item")).toHaveCount(2);
  await expect(modal.locator(".vc-item").first()).toContainText("Senden");
  await expect(page.locator("#sv-pop")).toBeHidden();
  await page.keyboard.press("Escape");
  await expect(modal).toHaveCount(0);
});

test("a draft that is the deployed model offers no list of differences", async ({ page }) => {
  await setState(page, {
    comparison: {
      processId: "order", savedAt: T0, deployed: true, key: 42, version: 2, latestVersion: 2,
      deployedAt: T0 - 60, sameModel: true, changes: [], counts: { added: 0, removed: 0, changed: 0 },
    },
  });
  await page.evaluate(() => window.__mountDraft());
  await page.locator("#sv-chip").click();
  await expect(page.locator("#sv-pop")).toBeVisible();
  await expect(page.locator("#sv-pop button", { hasText: "Alle Unterschiede anzeigen" })).toHaveCount(0);
});

test("a version opens the comparison with the version before it", async ({ page }) => {
  await setState(page, {
    processes: [
      { key: 41, processId: "order", name: "Bestellung", version: 1, deployedAt: T0 },
      { key: 42, processId: "order", name: "Bestellung", version: 2, deployedAt: T0 + 60 },
    ],
    versionComparison: {
      processId: "order", sameModel: false, counts: { added: 0, removed: 1, changed: 0 },
      changes: [{ kind: "removed", id: "old", type: "task", name: "Ablegen", parent: "order" }],
    },
  });
  await page.evaluate(() => window.__mountVersion(42));
  await page.locator("#sv-chip").click();
  await page.locator("#sv-pop button", { hasText: "Mit anderer Version vergleichen" }).click();

  const modal = page.locator(".vc-modal");
  await expect(modal.locator("h2")).toHaveText("Versionen vergleichen: Bestellung");
  await expect(modal.locator("#vc-from")).toHaveValue("41");
  await expect(modal.locator("#vc-to")).toHaveValue("42");
  await expect(modal.locator(".vc-item.vc-removed")).toContainText("Ablegen");
  await expect.poll(() => page.evaluate(() =>
    window.__writes.some((w) => w.url === "/api/v1/processes/42/comparison?with=41"))).toBe(true);
});

test("a process with one version offers no comparison of versions", async ({ page }) => {
  await setState(page, {
    processes: [{ key: 41, processId: "order", version: 1, deployedAt: T0 }],
  });
  await page.evaluate(() => window.__mountVersion(41));
  await page.locator("#sv-chip").click();
  await expect(page.locator("#sv-pop")).toBeVisible();
  await expect(page.locator("#sv-pop button", { hasText: "Mit anderer Version vergleichen" })).toHaveCount(0);
});

// Walking the versions from the chip, and restoring one as the draft
// (ADR-draft-modeler-walks-and-restores-versions). The author's question: step
// between the deployed models of a process, take any of them back as the draft, and
// deploy it again as a new version.

const THREE = [
  { key: 41, processId: "order", name: "Bestellung", version: 1, deployedAt: T0, deployedByName: "Alice Adams" },
  { key: 42, processId: "order", name: "Bestellung", version: 2, deployedAt: T0 + 60, deployedByName: "Bob Brown" },
  { key: 43, processId: "order", name: "Bestellung", version: 3, deployedAt: T0 + 120, deployedByName: "Carol Clark" },
];
// draftAhead is the draft against the newest version: saved after it, a different model.
const draftAhead = {
  processId: "order", savedAt: T0 + 500, savedByName: "Dana Dent", deployed: true,
  key: 43, version: 3, latestVersion: 3, deployedAt: T0 + 120, sameModel: false,
  difference: '<userTask id="p"> added', counts: { added: 1, removed: 0, changed: 0 },
  changes: [{ kind: "added", id: "p", type: "userTask", name: "Prüfen", parent: "order" }],
};

const posts = (page) => page.evaluate(() =>
  window.__writes.filter((w) => w.method === "POST").map((w) => w.url));

// edit changes the diagram on the canvas, so leaving it has something to lose.
const edit = (page) => page.evaluate(() => {
  const m = window.__atlasModeler;
  m.get("modeling").updateLabel(m.get("elementRegistry").get("Start_1"), "Eingang");
});

test("a version steps to the one before and after it, and lists every record", async ({ page }) => {
  await setState(page, { processes: THREE, comparison: draftAhead });
  await page.evaluate(() => window.__mountVersion(42));
  await expect(page.locator("#sv-chip")).toHaveText("v2 von 3");
  const older = page.locator(".sv-step.sv-older");
  const newer = page.locator(".sv-step.sv-newer");
  await expect(older).toHaveAttribute("href", "#/modeler/d/41");
  await expect(older).toHaveAttribute("aria-label", "Ältere Version: v1");
  await expect(newer).toHaveAttribute("href", "#/modeler/d/43");
  expect(await oneRow(page), "the arrows must not push the bar into a second row").toBe(1);

  await page.locator("#sv-chip").click();
  const items = page.locator("#sv-pop .sv-vlist a");
  await expect(items).toHaveCount(4);
  await expect(items.nth(0)).toContainText("Entwurf");
  await expect(items.nth(0)).toHaveAttribute("href", "#/modeler/draft/order");
  await expect(items.nth(1)).toContainText("v3");
  await expect(items.nth(1)).toContainText("neueste");
  await expect(items.nth(2)).toHaveAttribute("aria-current", "page");
  await expect(items.nth(3)).toContainText("Alice Adams");

  await older.click();
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/d/41");
});

test("the newest version steps on to the draft, and the draft back to it", async ({ page }) => {
  await setState(page, { processes: THREE, comparison: draftAhead });
  await page.evaluate(() => window.__mountVersion(43));
  await expect(page.locator(".sv-step.sv-newer")).toHaveAttribute("href", "#/modeler/draft/order");
  await expect(page.locator(".sv-step.sv-newer")).toHaveAttribute("aria-label", "Zum Entwurf");

  await page.evaluate(() => window.__mountDraft());
  await expect(page.locator("#sv-chip")).toHaveText("Entwurf · v3 + Änderungen");
  await expect(page.locator(".sv-step.sv-older")).toHaveAttribute("href", "#/modeler/d/43");
  await expect(page.locator(".sv-step.sv-newer")).toBeDisabled();
  expect(await oneRow(page)).toBe(1);
});

test("a draft never deployed, and a version that is all there is, show no arrows", async ({ page }) => {
  await setState(page, { comparison: { processId: "order", savedAt: T0, deployed: false, sameModel: false } });
  await page.evaluate(() => window.__mountDraft());
  await expect(page.locator("#sv-chip")).toHaveText("Entwurf · nicht deployt");
  await expect(page.locator(".sv-step")).toHaveCount(0);

  await setState(page, { processes: [THREE[0]], comparison: null });
  await page.evaluate(() => window.__mountVersion(41));
  await expect(page.locator("#sv-chip")).toHaveText("v1 · neueste");
  await expect(page.locator(".sv-step")).toHaveCount(0);
});

test("an edited version is not left for another without asking", async ({ page }) => {
  await setState(page, { processes: THREE, comparison: draftAhead });
  await page.evaluate(() => window.__mountVersion(42));
  await expect(page.locator(".sv-step.sv-older")).toBeVisible();
  await edit(page);

  page.once("dialog", (d) => d.dismiss());
  await page.locator(".sv-step.sv-older").click();
  await page.waitForTimeout(200);
  expect(await page.evaluate(() => location.hash)).not.toBe("#/modeler/d/41");

  page.once("dialog", (d) => d.accept());
  await page.locator(".sv-step.sv-older").click();
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/d/41");
  expect(await posts(page), "a version's edits are given up, not saved as a draft").toEqual([]);
});

test("Restore as draft says what the draft loses, replaces it, and opens the draft", async ({ page }) => {
  await setState(page, { processes: THREE, comparison: draftAhead, draftExists: true });
  page.on("dialog", (d) => { throw new Error(`no browser dialog expected: ${d.message()}`); });
  await page.evaluate(() => window.__mountVersion(41));
  await page.locator("#sv-chip").click();
  await page.locator("#sv-pop button", { hasText: "v1 als Entwurf übernehmen" }).click();

  const modal = page.locator(".sv-restore");
  await expect(modal.locator("h2")).toHaveText("v1 als Entwurf übernehmen?");
  await expect(modal).toContainText("Alice Adams");
  await expect(modal).toContainText("Dana Dent");
  const warn = modal.locator("#sv-restore-warn");
  await expect(warn).toContainText("nicht das Modell der neuesten Version v3");
  await expect(warn).toContainText("geht verloren");
  await expect(warn).toContainText("1 neu");

  // The differences open over the dialog, and Escape closes them first.
  await warn.locator("button", { hasText: "Alle Unterschiede anzeigen" }).click();
  await expect(page.locator(".vc-modal .vc-item")).toContainText("Prüfen");
  await page.keyboard.press("Escape");
  await expect(page.locator(".vc-modal")).toHaveCount(0);
  await expect(modal).toBeVisible();

  const ok = modal.locator("[data-sv-restore-ok]");
  await expect(ok).toHaveText("Entwurf durch v1 ersetzen");
  await expect(ok).toHaveClass(/danger/);
  await ok.click();
  await expect(modal).toHaveCount(0);
  // The first save is refused because the draft exists; the dialog's yes answers it.
  expect(await posts(page)).toEqual(["/api/v1/drafts?from=", "/api/v1/drafts?from=order"]);
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/draft/order");
});

test("Restore when the process has no draft makes the version its draft, without a warning", async ({ page }) => {
  await setState(page, { processes: THREE, comparison: null });
  await page.evaluate(() => window.__mountVersion(42));
  await page.locator("#sv-chip").click();
  await page.locator("#sv-pop button", { hasText: "v2 als Entwurf übernehmen" }).click();
  const modal = page.locator(".sv-restore");
  await expect(modal).toContainText("Dieser Prozess hat keinen Entwurf");
  await expect(modal.locator("#sv-restore-warn")).toHaveCount(0);
  await expect(modal.locator("[data-sv-restore-ok]")).toHaveText("v2 zum Entwurf machen");
  await expect(modal.locator("[data-sv-restore-ok]")).not.toHaveClass(/danger/);

  // Cancel leaves everything as it was.
  await modal.locator("[data-sv-restore-cancel]").click();
  await expect(modal).toHaveCount(0);
  expect(await posts(page)).toEqual([]);
});

test("a version the draft already is offers no restore", async ({ page }) => {
  await setState(page, {
    processes: THREE,
    comparison: { ...draftAhead, key: 42, version: 2, sameModel: true, changes: [], counts: {} },
  });
  await page.evaluate(() => window.__mountVersion(42));
  await page.locator("#sv-chip").click();
  await expect(page.locator("#sv-pop")).toBeVisible();
  await expect(page.locator("#sv-pop button", { hasText: "als Entwurf übernehmen" })).toHaveCount(0);
  await expect(page.locator("#sv-pop .sv-vlist a.current")).toContainText("= Entwurf");
});
