// Which process applications may command a product (ADR-0429 §10, decision 1).
//
// A process deployed into an application may ask a held position of a product for an
// action with a Shop send task in mode "command" — but only when the product names
// that application. The list is the product's `commandedBy`: the applications'
// portable keys, empty by default, and empty means nobody may command it. The form
// offers the applications this server has by name and stores their key, keeps a key
// no application here carries (a catalogue moves between servers), and falls back to
// typing keys when the list of applications cannot be read.
//
// A save replaces the product, so the field has to round-trip: what is stored is
// shown, and what is shown is sent back. Driven through the real catalog-admin.js
// against the editor harness's mock api.
import { test, expect } from "@playwright/test";

const HR = { id: "app_hr", name: "HR Leavers", key: "hr-leavers" };
const IT = { id: "app_it", name: "IT Maintenance", key: "it-maintenance" };
// An application created before keys existed has none until one is derived, so it
// cannot be named by key and is not offered as a box.
const KEYLESS = { id: "app_old", name: "Old Intranet" };

// open mounts the catalogue with these applications (null: the read fails), reshapes
// one product, and opens that product's editor with the fulfilment section unfolded.
const open = async (page, { id = "p24", name = "Produkt 24", patch, apps = [HR, IT, KEYLESS] } = {}) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(([pid, p, a]) => {
    window.__applications = a;
    if (p) window.__patchItems = (items) => items.map((i) => (i.id === pid ? { ...i, ...p } : i));
  }, [id, patch || null, apps]);
  await page.evaluate(() => window.__mount());
  await page.waitForSelector(".product-list tbody tr");
  const row = page.locator(".product-list tbody tr", { has: page.locator('button[data-act="edit"]') })
    .filter({ hasText: name }).first();
  await row.locator('button[data-act="edit"]').click();
  await expect(page.locator(".product-editor .product-form")).toBeVisible();
  await page.evaluate(() => {
    const d = document.querySelector('.product-form details.form-group[data-sec="fulfil"]');
    if (d) d.open = true;
  });
};

// lastSave is the body of the newest product save the page sent.
const lastSave = (page) => page.evaluate(() => {
  const saves = window.__sent.filter((c) => c.method === "POST" && /\/catalog-products$/.test(c.url));
  return saves.length ? saves[saves.length - 1].body : null;
});

const saveCount = (page) => page.evaluate(() =>
  window.__sent.filter((c) => c.method === "POST" && /\/catalog-products$/.test(c.url)).length);

const save = async (page) => {
  const before = await saveCount(page);
  await page.locator('.product-form button[data-publish="no"]').click();
  await expect.poll(() => saveCount(page)).toBe(before + 1);
  return lastSave(page);
};

const box = (page, key) => page.locator(`.product-form input[name="commandedBy"][value="${key}"]`);

// rowOf is the label a key's box sits in, which is where its name is written.
const rowOf = (page, key) => page.locator(".product-form .commanded-by label", {
  has: page.locator(`input[name="commandedBy"][value="${key}"]`),
});

test("the applications are offered by name, and the stored one is ticked", async ({ page }) => {
  await open(page, { patch: { commandedBy: ["hr-leavers"] } });
  const field = page.locator(".product-form .commanded-by");
  await expect(field).toBeVisible();
  await expect(box(page, "hr-leavers")).toBeChecked();
  await expect(box(page, "it-maintenance")).not.toBeChecked();
  // The name is what a reader recognises and the key is what is stored, so both are
  // on the row.
  const hr = rowOf(page, "hr-leavers");
  await expect(hr).toContainText("HR Leavers");
  await expect(hr).toContainText("hr-leavers");
  // An application with no key cannot be stored, so it is named and not offered.
  await expect(field.locator('input[name="commandedBy"]')).toHaveCount(2);
  await expect(field).toContainText("Old Intranet");
  // It belongs with the actions it gates: in the fulfilment section, after the grid.
  const placed = await page.evaluate(() => {
    const f = document.querySelector(".product-form .commanded-by");
    const grid = document.querySelector(".product-form .actgrid");
    return !!f.closest('details.form-group[data-sec="fulfil"]')
      && !!(grid.compareDocumentPosition(f) & Node.DOCUMENT_POSITION_FOLLOWING);
  });
  expect(placed).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("ticking one application and unticking another is what the save sends", async ({ page }) => {
  await open(page, { patch: { commandedBy: ["hr-leavers"] } });
  await box(page, "it-maintenance").check();
  expect((await save(page)).commandedBy).toEqual(["hr-leavers", "it-maintenance"]);

  await box(page, "hr-leavers").uncheck();
  expect((await save(page)).commandedBy).toEqual(["it-maintenance"]);
  expect(page.__errors).toEqual([]);
});

test("unticking the last application clears the list instead of keeping it", async ({ page }) => {
  await open(page, { patch: { commandedBy: ["hr-leavers"] } });
  await box(page, "hr-leavers").uncheck();
  // The save replaces the product: an empty list is what stops every process
  // commanding it, and leaving the field out would keep the stored one.
  expect((await save(page)).commandedBy).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("a key no application here carries is shown, kept, and can be taken away", async ({ page }) => {
  await open(page, { patch: { commandedBy: ["hr-leavers", "finance-close"] } });
  await expect(box(page, "finance-close")).toBeChecked();
  await expect(rowOf(page, "finance-close")).toContainText("no application here");
  expect((await save(page)).commandedBy).toEqual(["hr-leavers", "finance-close"]);

  await box(page, "finance-close").uncheck();
  expect((await save(page)).commandedBy).toEqual(["hr-leavers"]);
  expect(page.__errors).toEqual([]);
});

test("a key typed for an application not listed is sent, without a blank or a repeat", async ({ page }) => {
  await open(page, { patch: { commandedBy: ["hr-leavers"] } });
  // Publishing refuses a blank entry and a duplicate, so the form sends neither.
  await page.locator('.product-form input[name="commandedBy-keys"]')
    .fill(" finance-close , , hr-leavers, finance-close ");
  expect((await save(page)).commandedBy).toEqual(["hr-leavers", "finance-close"]);
  expect(page.__errors).toEqual([]);
});

test("a product without the field opens with nothing ticked and saves an empty list", async ({ page }) => {
  await open(page, { id: "p10", name: "Produkt 10" });
  await expect(page.locator('.product-form input[name="commandedBy"]:checked')).toHaveCount(0);
  await expect(page.locator('.product-form input[name="commandedBy-keys"]')).toHaveValue("");
  const body = await save(page);
  expect(body.commandedBy).toEqual([]);
  // And nothing else about the save changed with it.
  expect(body.id).toBe("p10");
  expect(body.provisionProcess).toBe("proc_demo_10");
  expect(page.__errors).toEqual([]);
});

test("when the applications cannot be read, the keys are typed instead", async ({ page }) => {
  await open(page, { patch: { commandedBy: ["hr-leavers"] }, apps: null });
  const field = page.locator(".product-form .commanded-by");
  await expect(field.locator('input[name="commandedBy"]')).toHaveCount(0);
  await expect(field).toContainText("could not be read");
  const keys = field.locator('input[name="commandedBy-keys"]');
  await expect(keys).toHaveValue("hr-leavers");
  await keys.fill("hr-leavers, it-maintenance");
  expect((await save(page)).commandedBy).toEqual(["hr-leavers", "it-maintenance"]);
  expect(page.__errors).toEqual([]);
});
