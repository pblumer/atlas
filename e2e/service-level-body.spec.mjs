// e2e for the product's service level in the Console's product editor
// (api/web/catalog-admin.js, serviceLevelFrom through productBody;
// ADR-0515).
//
// A save is a full replace, so the field has to say three things exactly: no
// service level when both boxes are empty, the promise as typed when either is
// filled (a half-filled one is the publish's to refuse, with its reason), and the
// names in languages this catalogue does not declare kept as they were stored.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/catalog-product-body-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const build = (page, values, stored, langs) =>
  page.evaluate(([v, s, l]) => { window.set(v); return window.build(s, l); }, [values, stored, langs]);

test("empty boxes are no service level, even over a stored one", async ({ page }) => {
  const body = await build(page, { "t-de": "Notebook", slName: "", slDays: "" },
    { serviceLevel: { name: "Standard", deliveryDays: 3 } }, ["de"]);
  expect(body.serviceLevel).toBeUndefined();
});

test("a name and a delivery time are sent as typed", async ({ page }) => {
  const body = await build(page, { "t-de": "Notebook", slName: " Express ", slDays: "2" }, {}, ["de"]);
  expect(body.serviceLevel).toEqual({ name: "Express", deliveryDays: 2 });
});

test("a half-filled service level is sent, for the publish to refuse with its reason", async ({ page }) => {
  const body = await build(page, { "t-de": "Notebook", slName: "Standard", slDays: "" }, {}, ["de"]);
  expect(body.serviceLevel).toEqual({ name: "Standard", deliveryDays: 0 });
});

test("names per declared language are read, others survive", async ({ page }) => {
  const body = await build(page,
    { "t-de": "Notebook", slName: "Standard", slDays: "3", "sl-de": "Standard", "sl-fr": "Standard FR" },
    { serviceLevel: { name: "Standard", deliveryDays: 5, nameTexts: { it: "Standard IT", fr: "alt" } } },
    ["de", "fr"]);
  expect(body.serviceLevel).toEqual({
    name: "Standard", deliveryDays: 3,
    nameTexts: { de: "Standard", fr: "Standard FR", it: "Standard IT" },
  });
});
