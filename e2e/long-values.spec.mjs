// End-to-end coverage for long variable values in Operations (api/web/editor.js).
//
// The case behind it: a process that imports a CSV upload keeps the whole file in one
// string variable. The overview's instance card printed every record of it, wrapped
// over the full height of the panel, and the next instance started a screen further
// down. A value that long is not something to read in a card: the card says what it is
// and how big, and the whole value is one click away in a window that can be scrolled,
// copied and saved. The same holds for the single-instance view and the replay's
// Variables tab, which used to print the long text just as whole.
import { test, expect } from "@playwright/test";

const errorsOf = (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  return errors;
};

const openLive = async (page, instance) => {
  const errors = errorsOf(page);
  await page.goto("/long-values-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((i) => window.__mountLive(i), instance ?? null);
  return errors;
};

const bigCard = (page) => page.locator("#var-panel .vp-inst", { hasText: "281475062011074" });
const smallCard = (page) => page.locator("#var-panel .vp-inst", { hasText: "281475062011090" });
const chips = (card) => card.locator(".vp-inst-vars [data-var]");
const win = (page) => page.locator("#var-modal-ov");

test.describe("the overview's instance card", () => {
  test("shows a long text as one line with its size, not as every record", async ({ page }) => {
    const errors = await openLive(page);
    const card = bigCard(page);
    await expect(card).toBeVisible();

    const chip = card.locator('.vp-inst-vars [data-var="csvText"]');
    await expect(chip).toBeVisible();
    // The header line leads the preview, and the size says what was left out.
    await expect(chip).toContainText("personalnummer;vorname");
    await expect(chip.locator(".vp-chip-size")).toContainText("16");
    await expect(chip.locator(".vp-chip-size")).toContainText(/KiB/);
    // The last record is nowhere in the card: not on screen, and not hidden in the
    // markup either, where a title attribute used to carry the whole value.
    await expect(card).not.toContainText("Chiara");
    expect(await card.innerHTML()).not.toContain("Chiara");
    // One line high: the card no longer grows with the file.
    const box = await chip.boundingBox();
    expect(box.height).toBeLessThan(40);
    expect(errors).toEqual([]);
  });

  test("opens the whole value in a window that copies and saves it", async ({ page }) => {
    const errors = await openLive(page);
    await bigCard(page).locator('.vp-inst-vars [data-var="csvText"]').click();

    await expect(win(page)).toBeVisible();
    await expect(page.locator("#var-modal-title")).toHaveText("csvText");
    await expect(page.locator("#var-modal-body")).toContainText("P-1015;Chiara;Roth");
    // Copy carries the value exactly as stored, line breaks included.
    const csv = await page.evaluate(() => window.__CSV);
    await expect(page.locator("#var-modal-copy")).toHaveAttribute("data-copy", csv);

    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.locator("#var-modal-dl").click(),
    ]);
    expect(download.suggestedFilename()).toBe("csvText.txt");

    await page.keyboard.press("Escape");
    await expect(win(page)).toBeHidden();
    expect(errors).toEqual([]);
  });

  test("shows a list as its size, and opens it as formatted JSON", async ({ page }) => {
    const errors = await openLive(page);
    const chip = bigCard(page).locator('.vp-inst-vars [data-var="pruefungen"]');
    await expect(chip).toContainText("[15");
    expect(await chip.getAttribute("title")).not.toContain("Chiara");

    await chip.click();
    await expect(win(page)).toBeVisible();
    await expect(page.locator("#var-modal-title")).toHaveText("pruefungen");
    await expect(page.locator("#var-modal-body .j-key").first()).toBeVisible();
    await expect(page.locator("#var-modal-body")).toContainText("Chiara");
    await expect(page.locator("#var-modal-dl")).toBeVisible();
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.locator("#var-modal-dl").click(),
    ]);
    expect(download.suggestedFilename()).toBe("pruefungen.json");
    expect(errors).toEqual([]);
  });

  test("lists at most five values, the searchable ones first, and opens the rest on demand", async ({ page }) => {
    const errors = await openLive(page);
    const card = bigCard(page);
    await expect(chips(card)).toHaveCount(5);
    // importId is declared searchable on the process, and the server lists it last —
    // it is the value an operator identifies the case by, so it leads the card.
    await expect(chips(card).first()).toHaveAttribute("data-var", "importId");

    const more = card.locator(".vp-more-vars");
    await expect(more).toHaveText(/\+\s*4/);
    await more.click();
    await expect(chips(card)).toHaveCount(9);

    // The panel is rebuilt by a 1.5 s poll; an opened card stays open across it.
    await page.waitForTimeout(2000);
    await expect(chips(card)).toHaveCount(9);

    await card.locator(".vp-more-vars").click();
    await expect(chips(card)).toHaveCount(5);
    expect(errors).toEqual([]);
  });

  test("a card with few values offers nothing more to open", async ({ page }) => {
    const errors = await openLive(page);
    await expect(chips(smallCard(page))).toHaveCount(2);
    await expect(smallCard(page).locator(".vp-more-vars")).toHaveCount(0);
    expect(errors).toEqual([]);
  });
});

test.describe("the single-instance view", () => {
  test("shows the start of a long text and opens the whole of it in the window", async ({ page }) => {
    const errors = await openLive(page, "281475062011074");
    const field = page.locator('#var-panel .vfield[data-var="csvText"]');
    await expect(field).toBeVisible();
    await expect(field.locator(".vv")).toContainText("personalnummer;vorname");
    await expect(field.locator(".vv")).not.toContainText("Chiara");

    await field.locator(".v-show-all").click();
    await expect(win(page)).toBeVisible();
    await expect(page.locator("#var-modal-body")).toContainText("P-1015;Chiara;Roth");
    expect(errors).toEqual([]);
  });

  test("a short text is shown whole, with nothing to open", async ({ page }) => {
    const errors = await openLive(page, "281475062011074");
    const field = page.locator('#var-panel .vfield[data-var="completedBy"]');
    await expect(field.locator(".vv")).toHaveText("usr_251f4602587fc474f04e1866");
    await expect(field.locator(".v-show-all")).toHaveCount(0);
    expect(errors).toEqual([]);
  });
});

test.describe("the replay's Variables tab", () => {
  test("shows the start of a long text and opens the whole of it in the window", async ({ page }) => {
    const errors = errorsOf(page);
    await page.goto("/long-values-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.evaluate(() => window.__mountReplay());
    await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();
    await page.locator("#rp-tabs button[data-tab='variables']").click();

    const row = page.locator("#tab-variables .vt tbody tr", { has: page.locator("td.c-name", { hasText: "csvText" }) });
    await expect(row).toBeVisible();
    await expect(row.locator(".c-val")).toContainText("personalnummer;vorname");
    await expect(row.locator(".c-val")).not.toContainText("Chiara");

    await row.locator(".v-show-all").click();
    await expect(win(page)).toBeVisible();
    await expect(page.locator("#var-modal-title")).toHaveText("csvText");
    await expect(page.locator("#var-modal-body")).toContainText("P-1015;Chiara;Roth");
    expect(errors).toEqual([]);
  });
});
