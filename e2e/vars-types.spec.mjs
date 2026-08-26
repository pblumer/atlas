// End-to-end coverage for what the Modeler's Variables panel says a variable *holds*
// (api/web/editor.js).
//
// The panel listed a name and who writes it, and nothing else. That answers "does this
// variable exist"; it does not answer the two questions an author actually has in front
// of a connector result — what type is this, and what is inside it. The type is knowable
// before anything runs, from three places: a start variable declares its own, a form
// field's component type is a type, and what a connector kind writes is a fact about the
// kind (a query returns rows, "query one" a row, an execute a count). What is *inside*
// is not knowable that way — so the panel reads it off the last real run of this process
// and opens the structure where it stands, which is the only way to see at design time
// that a row carries `kundennr` and not `id`.
//
// Nothing declares a FEEL script's result: it is whatever the expression evaluates to.
// That row must carry no badge at all. A badge reads as knowledge, and a guessed one
// would be worse than the blank the panel had before.
import { test, expect } from "@playwright/test";

const open = async (page, opts) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/vars-types-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((o) => window.__mount(o), opts || {});
  await page.locator("#vars-toggle").click();
  await expect(page.locator('#vars-list .var-row[data-var="kundennr"]')).toBeVisible();
};

const row = (page, name) => page.locator(`#vars-list .var-row[data-var="${name}"]`);
// innerText reflects the badge's CSS text-transform, so compare case-insensitively.
const badge = async (page, name) => (await row(page, name).locator(".vtag").innerText()).toLowerCase();

test("a start variable's declared type is a badge, not a word inside its origin", async ({ page }) => {
  await open(page, { ran: false });
  expect(await badge(page, "kundennr")).toBe("number");
  // It used to read "start variable · number" — the type is its own column now, so the
  // origin line says only where the variable comes from.
  await expect(row(page, "kundennr").locator(".var-meta")).toHaveText(/^start variable · Start$/);
  expect(page.__errors).toEqual([]);
});

test("a connector's result type comes from the catalog, before anything has run", async ({ page }) => {
  await open(page, { ran: false });
  // PostgreSQL, operation "query": rows. Nothing ran, nothing was deployed — the kind
  // itself is what knows this.
  expect(await badge(page, "kunden")).toBe("array");
  await expect(row(page, "kunden").locator(".var-meta")).toContainText("connector result");
  expect(page.__errors).toEqual([]);
});

test("a variable nothing declares carries no badge", async ({ page }) => {
  await open(page, { ran: false });
  // A FEEL script's result is whatever `=1 + 1` evaluates to. Silence beats a guess.
  await expect(row(page, "geschrieben").locator(".vtag")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a diagram that never ran shows its declarations and no values", async ({ page }) => {
  await open(page, { ran: false });
  await expect(page.locator("#vars-sample")).toBeHidden();
  await expect(page.locator("#vars-list .var-val")).toHaveCount(0);
  await expect(page.locator("#vars-list .var-peek")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the last run's values are shown, and the panel says which run", async ({ page }) => {
  await open(page);
  await expect(row(page, "kundennr").locator(".var-val")).toHaveText("42");
  // A value with no run named beside it reads as "what this is" rather than "what this
  // was, once" — on a finished instance from last week that difference is the story.
  const head = page.locator("#vars-sample");
  await expect(head).toBeVisible();
  await expect(head).toContainText("281474976710657");
  await expect(head).toContainText("completed");
  expect(page.__errors).toEqual([]);
});

test("an observed value's type wins over the declaration, and names both", async ({ page }) => {
  await open(page);
  // The script's result had no badge at all a moment ago; the run gives it one.
  expect(await badge(page, "geschrieben")).toBe("number");
  await expect(row(page, "geschrieben").locator(".vtag"))
    .toHaveAttribute("title", /held on the run/);
  // Where both exist the tooltip carries the declaration too, so a run that contradicts
  // it is visible rather than quietly overwritten.
  await expect(row(page, "kundennr").locator(".vtag"))
    .toHaveAttribute("title", /held on the run.*declares number/);
  expect(page.__errors).toEqual([]);
});

test("a structure opens where it stands, and shows what is inside a row", async ({ page }) => {
  await open(page);
  const kunden = row(page, "kunden");
  const peek = kunden.locator(".var-peek");
  // Collapsed, the summary carries the brackets: a list and one of its elements read
  // alike once their text is truncated, and the bracket is the difference.
  await expect(peek).toContainText("[2 items]");
  await expect(kunden.locator(".var-json")).toBeHidden();

  await peek.click();
  const body = kunden.locator(".var-json");
  await expect(body).toBeVisible();
  // The point of opening it: the column names, which no amount of naming the variable
  // would have told the author.
  await expect(body).toContainText("nachname");
  await expect(body).toContainText("Meier");

  await peek.click();
  await expect(body).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("an open structure survives an edit to the diagram", async ({ page }) => {
  await open(page);
  await row(page, "kunden").locator(".var-peek").click();
  await expect(row(page, "kunden").locator(".var-json")).toBeVisible();
  // The list is rebuilt on every diagram change; an expansion that lived only in the
  // markup would close under the reader on the next keystroke.
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    const el = m.get("elementRegistry").get("rechnen");
    m.get("modeling").updateProperties(el, { name: "Alter berechnen v2" });
  });
  await expect(row(page, "kunden").locator(".var-json")).toBeVisible();
  expect(page.__errors).toEqual([]);
});
