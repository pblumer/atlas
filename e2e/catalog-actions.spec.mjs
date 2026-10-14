// The product's actions in the catalogue editor (ADR-0429).
//
// A lifecycle product used to name three start events in three boxes — provision,
// deprovision, change. It now lists its actions: the order's two first, with their key
// and effect fixed, then every change or service somebody may ask for, with the
// message, who may ask and the label per language. A product that still carries the
// operation map opens as the actions it means and is saved as actions. Driven through
// the real catalog-admin.js against the editor harness's mock api.
import { test, expect } from "@playwright/test";

// p24 as a lifecycle product that still carries ADR-0425's operation map.
const LEGACY = {
  provisionProcess: "", deprovisionProcess: "", lifecycleProcess: "proc_demo_24",
  operations: { provision: "p24.provision", deprovision: "p24.deprovision", change: "p24.change" },
};

const open = async (page, product, patch) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.setViewportSize({ width: 1600, height: 900 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  if (patch) {
    await page.evaluate((p) => {
      window.__patchItems = (items) => items.map((i) => (i.id === "p24" ? { ...i, ...p } : i));
    }, patch);
  }
  await page.evaluate(() => window.__mount());
  await page.waitForSelector(".product-list tbody tr");
  const row = page.locator(".product-list tbody tr", { has: page.locator(`button[data-act="edit"]`) })
    .filter({ hasText: product }).first();
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

const save = async (page) => {
  page.once("dialog", (d) => d.accept());
  await page.locator('.product-form button[data-publish="no"]').click();
  await expect.poll(() => lastSave(page)).not.toBeNull();
};

test("an operation map opens as the actions it means, the order's two fixed", async ({ page }) => {
  await open(page, "Produkt 24", LEGACY);
  const f = page.locator(".product-form");
  await expect(f.locator('[name="act-0-key"]')).toHaveValue("provision");
  await expect(f.locator('[name="act-0-key"]')).toHaveAttribute("readonly", "");
  await expect(f.locator('[name="act-0-msg"]')).toHaveValue("p24.provision");
  await expect(f.locator('[name="act-1-key"]')).toHaveValue("deprovision");
  await expect(f.locator('[name="act-1-msg"]')).toHaveValue("p24.deprovision");
  await expect(f.locator('[name="act-2-key"]')).toHaveValue("change");
  await expect(f.locator('[name="act-2-msg"]')).toHaveValue("p24.change");
  await expect(f.locator('[name="act-2-trig-customer"]')).toBeChecked();
  // The provision is the order's, so nobody else is offered as its trigger.
  await expect(f.locator('[name^="act-0-trig-"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("saving sends actions in place of the operation map", async ({ page }) => {
  await open(page, "Produkt 24", LEGACY);
  await save(page);
  const body = await lastSave(page);
  expect(body.operations).toBeUndefined();
  expect(body.actions.map((a) => [a.key, a.message, a.effect])).toEqual([
    ["provision", "p24.provision", "provision"],
    ["deprovision", "p24.deprovision", "deprovision"],
    ["change", "p24.change", "change"],
  ]);
  expect(body.actions[2].triggers).toEqual(["customer", "operator"]);
  expect(page.__errors).toEqual([]);
});

test("an added action carries its effect, its triggers and a label per language", async ({ page }) => {
  await open(page, "Produkt 24", LEGACY);
  const f = page.locator(".product-form");
  await page.locator("[data-add-action]").click();
  const n = await f.locator('.actgrid input[name$="-key"]').evaluateAll((els) =>
    Math.max(...els.map((e) => Number(/^act-(\d+)-key$/.exec(e.name)[1]))));
  await f.locator(`[name="act-${n}-key"]`).fill("password-reset");
  await f.locator(`[name="act-${n}-msg"]`).fill("p24.password.reset");
  await f.locator(`[name="act-${n}-effect"]`).selectOption("service");
  await f.locator(`[name="act-${n}-trig-operator"]`).check();
  await f.locator(`[name="act-${n}-label-de"]`).fill("Passwort zurücksetzen");
  await save(page);
  const added = (await lastSave(page)).actions.find((a) => a.key === "password-reset");
  expect(added).toEqual({
    key: "password-reset", message: "p24.password.reset", effect: "service",
    triggers: ["operator"], labels: { de: "Passwort zurücksetzen" },
  });
  expect(page.__errors).toEqual([]);
});

test("clearing an action's key and message removes it", async ({ page }) => {
  await open(page, "Produkt 24", LEGACY);
  const f = page.locator(".product-form");
  await f.locator('[name="act-2-key"]').fill("");
  await f.locator('[name="act-2-msg"]').fill("");
  await save(page);
  expect((await lastSave(page)).actions.map((a) => a.key)).toEqual(["provision", "deprovision"]);
  expect(page.__errors).toEqual([]);
});

test("choosing a lifecycle process pre-fills the two messages after the product", async ({ page }) => {
  await open(page, "Produkt 10");
  const f = page.locator(".product-form");
  await expect(f.locator('[name="act-0-msg"]')).toHaveValue("");
  await f.locator('select[name="lifecycleProcess"]').selectOption("proc_demo_10");
  await expect(f.locator('[name="act-0-msg"]')).toHaveValue("p10.provision");
  await expect(f.locator('[name="act-1-msg"]')).toHaveValue("p10.deprovision");
  // The list of what publishing needs reads the two messages, and marks them there.
  await expect(page.locator(".publish-needs")).toContainText("✓ a message for the provision and the deprovision action");
  expect(page.__errors).toEqual([]);
});
