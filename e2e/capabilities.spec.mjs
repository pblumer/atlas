// The business architecture in the Console (ADR-0305, roadmap B8).
//
// Four claims are worth holding in a browser rather than in Go. The landing view is the
// gap report, grouped by kind and said in the catalogue's words rather than the report's
// English detail (ADR-0267). The list is the shared sort/filter table (ADR-0286), with
// the two questions the register exists for — what does nothing do, what has nobody
// confirmed — one click away. A confirmation is made on the detail page and sends what
// the reader entered. And the value stream says which of its stages names a capability
// the map does not have.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/capabilities-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const mount = async (page, fn) => {
  await page.evaluate((name) => window[name](), fn);
  await expect(page.locator(".ba-root")).toBeVisible();
};

// No raw message id and no line of the server's English detail may reach the screen.
const saysNoRawKeys = async (page) => {
  const text = await page.locator("#view").innerText();
  expect(text).not.toMatch(/\bba\.[a-z]/);
  expect(text).not.toContain("english prose");
};

test("the landing view is the gap report, grouped by kind, in German", async ({ page }) => {
  await mount(page, "__mountMap");
  const gaps = page.locator('[data-panel="gaps"]');
  await expect(gaps).toBeVisible();
  await expect(page.locator('[data-panel="list"]')).toBeHidden();

  // One card per kind that has findings, in the server's order, with the count.
  const kinds = gaps.locator(".ba-kind");
  await expect(kinds).toHaveCount(6);
  await expect(kinds.nth(0)).toHaveAttribute("data-kind", "capability.unrealized");
  await expect(kinds.nth(0).locator("h2")).toContainText("Nicht umgesetzt");
  await expect(gaps.locator('[data-kind="capability.unconfirmed"] h2 .chip')).toHaveText("2");

  // The sentences are composed from coordinates: the Worker a realization names, the
  // process nobody claims, the stage naming a capability the map does not have.
  await expect(gaps.locator('[data-kind="realization.missing"]')).toContainText("mail-service-desk");
  await expect(gaps.locator('[data-kind="realization.missing"]')).toContainText("nicht konfiguriert");
  await expect(gaps.locator('[data-kind="process.unclaimed"]')).toContainText("dunning");
  await expect(gaps.locator('[data-kind="stage.unknown"]')).toContainText("shipping");
  // A capability is named by its name and links to its page.
  await expect(gaps.locator('[data-kind="capability.unrealized"] a')).toHaveAttribute("href", "#/panorama/capabilities/claims-intake");
  await expect(gaps.locator('[data-kind="capability.unrealized"] a')).toHaveText("Schadenaufnahme");
  // Never confirmed and lapsed are different work, and say so.
  await expect(gaps.locator('[data-kind="capability.unconfirmed"]')).toContainText("noch nie bestätigt");
  await expect(gaps.locator('[data-kind="capability.unconfirmed"]')).toContainText("zuletzt bestätigt am");

  // What was checked, and what the caller could not see, is said rather than hidden.
  await expect(gaps.locator(".ba-checked")).toContainText("Capabilities 3");
  await expect(gaps.locator(".ba-checked")).toContainText("Ein Verweis zeigt auf eine Anwendung, die Sie nicht sehen dürfen");
  // A kind with nothing in it is named once, not dropped.
  await expect(gaps.locator(".ba-clean")).toContainText("Nicht deklarierter Aufruf");
  await saysNoRawKeys(page);
  expect(page.__errors).toEqual([]);
});

test("the list is the shared table, with the backlog and the review one click away", async ({ page }) => {
  await mount(page, "__mountMap");
  await page.locator('.ba-tabs [data-tab="list"]').click();
  const list = page.locator('[data-panel="list"]');
  await expect(list).toBeVisible();
  const table = list.locator("table[data-dt-key='ba-capabilities']");
  await expect(table).toHaveAttribute("data-dt-enhanced", "1");
  await expect(table.locator("tbody tr")).toHaveCount(3);

  // What nothing realizes: the adoption backlog.
  await list.locator('[data-filter="unrealized"]').check();
  await expect(list.locator("table tbody tr")).toHaveCount(1);
  await expect(list.locator("table tbody tr")).toContainText("Schadenaufnahme");
  await list.locator('[data-filter="unrealized"]').uncheck();

  // What is due for a reading.
  await list.locator('[data-filter="stale"]').check();
  await expect(list.locator("table tbody tr")).toHaveCount(2);
  await list.locator('[data-filter="stale"]').uncheck();

  // Tags are the only grouping there is.
  await list.locator('[data-filter="tag"]').selectOption("platform");
  await expect(list.locator("table tbody tr")).toHaveCount(1);
  await expect(list.locator("table tbody tr")).toContainText("Benachrichtigen");
  await expect(list.locator('[data-role="count"]')).toHaveText("1 Capability");
  await saysNoRawKeys(page);
  expect(page.__errors).toEqual([]);
});

test("a capability says what realizes it, what it measured, and takes a confirmation", async ({ page }) => {
  await mount(page, "__mountBilling");
  await expect(page.locator(".ba-head h1")).toContainText("Rechnungsstellung");
  await expect(page.locator(".ba-head h1")).toContainText("Aktiv");
  await expect(page.locator("#view")).toContainText("Rechnung stellen");
  await expect(page.locator("#view")).toContainText("7 laufende Instanzen");

  // The measurement is read over a window, 30 days unless the reader picks another.
  const figures = page.locator('[data-role="figures"]');
  await expect(figures).toContainText("40");
  await expect(figures).toContainText("95 %");
  await expect(figures).toContainText("Durchlaufzeit: nicht gemessen");
  let reads = await page.evaluate(() => window.__calls.filter((c) => c.url.includes("/measurement?")).map((c) => c.url));
  expect(reads).toEqual(["/api/v1/capabilities/billing/measurement?windowDays=30"]);
  await page.locator('[data-role="window"]').selectOption("7");
  await expect.poll(async () => page.evaluate(() =>
    window.__calls.filter((c) => c.url.includes("/measurement?")).length)).toBe(2);
  reads = await page.evaluate(() => window.__calls.filter((c) => c.url.includes("/measurement?")).map((c) => c.url));
  expect(reads[1]).toBe("/api/v1/capabilities/billing/measurement?windowDays=7");

  // A confirmation sends who was asked and what was found, and the page shows the answer.
  const confirm = page.locator('[data-role="confirm"]').first();
  await expect(confirm).toContainText("Bestätigt am");
  await confirm.locator('[data-field="with"]').fill("Ida Muster");
  await confirm.locator('[data-field="note"]').fill("Umfang stimmt");
  await confirm.locator('button[data-role="confirm"]').click();
  await expect(confirm).toContainText("nach Rücksprache mit Ida Muster");
  const post = await page.evaluate(() => window.__calls.find((c) => c.method === "POST"));
  expect(post.url).toBe("/api/v1/capabilities/billing/confirmation");
  expect(post.body).toEqual({ with: "Ida Muster", note: "Umfang stimmt" });
  await saysNoRawKeys(page);
  expect(page.__errors).toEqual([]);
});

test("a capability that does not exist says so", async ({ page }) => {
  await mount(page, "__mountMissing");
  await expect(page.locator("#view")).toContainText("Diese Capability gibt es nicht");
  expect(page.__errors).toEqual([]);
});

test("a value stream flags the stage naming a capability the map does not have", async ({ page }) => {
  await mount(page, "__mountStreams");
  await expect(page.locator("table[data-dt-key='ba-value-streams'] tbody tr")).toHaveCount(1);

  await mount(page, "__mountStream");
  const stages = page.locator(".ba-stage");
  await expect(stages).toHaveCount(2);
  await expect(stages.nth(0)).toContainText("Rechnungsstellung");
  await expect(stages.nth(1)).toContainText("shipping");
  await expect(stages.nth(1)).toContainText("nicht in der Karte");
  // Never confirmed, and due.
  await expect(page.locator('[data-role="confirm"]').first()).toContainText("Noch nie bestätigt");
  await expect(page.locator('[data-role="confirm"]').first()).toContainText("fällig");
  await saysNoRawKeys(page);
  expect(page.__errors).toEqual([]);
});

test("a second locale is the same screen in other words", async ({ page }) => {
  await page.evaluate(() => window.__locale("en"));
  await mount(page, "__mountMap");
  await expect(page.locator('[data-kind="capability.unrealized"] h2')).toContainText("Not realized");
  await expect(page.locator('[data-kind="realization.missing"]')).toContainText("is not configured on this server");
  await saysNoRawKeys(page);
  expect(page.__errors).toEqual([]);
});
