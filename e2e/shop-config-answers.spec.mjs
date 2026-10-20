// e2e for the answers an order carries (api/web/shop.js, against a fixture).
//
// What was reported: a product whose order form asked for a number of seats could
// be put in the basket, its form filled in and checked — and "order" failed with
// 400 "cannot unmarshal number into Go struct field placeReq.config of type
// string". The order takes each answer as text (POST /api/v1/orders, config is a
// map of strings, ADR-0358), and the page sent what the form held as it held it: a
// number field a number, a checkbox a boolean, a multiple choice a list. Every
// field type the form editor offers rendered, and three of them made the product
// impossible to order, with nothing on the way to say so.
//
// So the property is the request the page sends, read off the request itself: every
// answer is a string, and each one says what was entered. It loads the REAL
// shop.html and the real form runtime, with only the network replaced, because the
// values are whatever form-js hands back — a fixture that typed them itself would
// prove nothing about the page.
import { test, expect } from "@playwright/test";

// The form asks one question of each kind the editor offers for an answer: text,
// number, yes/no, one choice and several choices.
const SCHEMA = {
  type: "default",
  id: "raum-form",
  components: [
    { key: "standort", type: "textfield", label: "Standort", validate: { required: true } },
    { key: "plaetze", type: "number", label: "Plätze", validate: { required: true } },
    { key: "beamer", type: "checkbox", label: "Beamer" },
    {
      key: "groesse", type: "radio", label: "Grösse",
      values: [{ label: "Klein", value: "klein" }, { label: "Gross", value: "gross" }],
    },
    {
      key: "ausstattung", type: "checklist", label: "Ausstattung",
      values: [{ label: "Whiteboard", value: "whiteboard" }, { label: "Kamera", value: "kamera" }],
    },
  ],
};

function installFixture({ schema, orders }) {
  window.__unmatched = [];
  window.__placed = [];
  window.__corrected = [];
  const RELEASE = {
    id: "rel_c", catalogId: "cat_c",
    items: [{
      id: "raum", homeCatalog: "cat_c", state: "active",
      texts: { de: "Sitzungszimmer", en: "Meeting room" },
      category: "Raum", categoryTexts: { de: "Raum", en: "Room" },
      approval: { kind: "none" }, provisionProcess: "p", deprovisionProcess: "d",
      configForm: "raum-form",
    }],
    waves: [["raum"]],
    withoutApproval: ["raum"],
  };
  const ROUTES = {
    // A user with an id, because ordering needs somebody to be: without one the
    // page draws every "+" disabled and nothing reaches the basket.
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": {
      id: "cat_c", rank: 1, languages: ["de", "en"],
      texts: { de: "Räume", en: "Rooms" }, items: ["raum"],
    },
    "/api/v1/catalogs/cat_c/releases": [RELEASE],
    "/api/v1/orders": orders || [],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
    "/api/v1/forms/raum-form": { id: "raum-form", name: "raum-form", schema },
  };
  const json = (body, status) => Promise.resolve(new Response(JSON.stringify(body), {
    status: status || 200, headers: { "content-type": "application/json" },
  }));
  window.fetch = (input, init) => {
    const path = String(input).replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    const method = ((init && init.method) || "GET").toUpperCase();
    if (path === "/api/v1/orders" && method === "POST") {
      // What the page sent, kept as the server would read it: parsed from the
      // wire, so a value the page serialised as a number stays one here.
      window.__placed.push(JSON.parse(init.body));
      return json({ id: "ord_1", lines: [] }, 201);
    }
    if (path.endsWith("/details") && method === "POST") {
      window.__corrected.push({ path, body: JSON.parse(init.body) });
      return json({});
    }
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) return json(ROUTES[path]);
    // Recorded rather than swallowed: a route the page reads and this fixture
    // does not serve would otherwise let a test pass against a page that never
    // loaded its catalogue.
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installFixture, { schema: SCHEMA, orders: test.info().title.startsWith("a correction")
    ? [HELD] : [] });
  await page.goto("/shop.html");
  await page.waitForSelector(".cascade", { timeout: 10000 });
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
  const missed = await page.evaluate(() => window.__unmatched);
  expect(missed, "every route the page reads is served by the fixture").toEqual([]);
});

// HELD is an order of the room whose position has not started yet, answered with
// text the way the server keeps it — so the correction form opens on those answers.
const HELD = {
  id: "ord_9", releaseId: "rel_c", orderer: "usr_1", recipient: "usr_1",
  createdAt: 1792497050106980373, updatedAt: 1792497050106980373,
  waves: [["raum"]],
  lines: [{
    itemId: "raum", status: "pending", configForm: "raum-form",
    provisionProcess: "p", deprovisionProcess: "d", approval: { kind: "none" },
    config: { standort: "Ei1", plaetze: "3", beamer: "false", groesse: "klein", ausstattung: "whiteboard" },
  }],
};

// order puts the room in the basket, answers its form through the form's own
// controls and presses "order".
const order = async (page) => {
  await page.locator(".cell").filter({ has: page.getByText("Meeting room", { exact: true }) })
    .first().locator("button.sq", { hasText: "+" }).click();
  await page.getByRole("button", { name: /Add to basket/ }).click();
  await page.getByLabel("Standort").fill("Ei1 - 1. Stock 152");
  await page.getByLabel("Plätze").fill("5");
  await page.getByLabel("Beamer").check();
  await page.getByLabel("Gross").check();
  await page.getByLabel("Kamera").check();
  await page.getByRole("button", { name: /order >/ }).click();
  await expect.poll(() => page.evaluate(() => window.__placed.length)).toBe(1);
  return (await page.evaluate(() => window.__placed[0])).config.raum;
};

test("every answer an order carries is text", async ({ page }) => {
  const answers = await order(page);
  for (const [key, value] of Object.entries(answers)) {
    expect(typeof value, `the answer ${key} is sent as text`).toBe("string");
  }
});

test("each answer says what was entered", async ({ page }) => {
  expect(await order(page)).toEqual({
    standort: "Ei1 - 1. Stock 152",
    plaetze: "5",
    beamer: "true",
    groesse: "gross",
    ausstattung: "kamera",
  });
});

test("a correction of an order's answers is text as well", async ({ page }) => {
  await page.getByRole("button", { name: "My orders" }).first().click();
  await page.getByRole("button", { name: "Change the details" }).click();
  // The form opens on what the order holds, each answer in its field's own kind:
  // the number, the unticked box, the one choice and the several.
  await expect(page.getByLabel("Plätze")).toHaveValue("3");
  await expect(page.getByLabel("Beamer")).not.toBeChecked();
  await expect(page.getByLabel("Klein")).toBeChecked();
  await expect(page.getByLabel("Whiteboard")).toBeChecked();
  await expect(page.getByLabel("Kamera")).not.toBeChecked();
  await page.getByLabel("Plätze").fill("7");
  await page.getByLabel("Beamer").check();
  await page.getByLabel("Kamera").check();
  await page.getByRole("button", { name: "Save the details" }).click();
  await expect.poll(() => page.evaluate(() => window.__corrected.length)).toBe(1);
  const sent = await page.evaluate(() => window.__corrected[0]);
  expect(sent.path).toBe("/api/v1/orders/ord_9/lines/raum/details");
  expect(sent.body.config).toEqual({
    standort: "Ei1",
    plaetze: "7",
    beamer: "true",
    groesse: "klein",
    ausstattung: "whiteboard,kamera",
  });
});
