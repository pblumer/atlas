// End-to-end coverage for the public start page carrying an anonymous visitor through
// a public wizard (api/web/public-form.html, ADR-0449). The start hands the visitor a
// pass; the page keeps it for the tab and sends it, in an Authorization header, with
// every later screen. Driven against the real page with the public routes mocked.
//
// The static e2e server serves the page at /public-form.html, so the token the page
// reads off its own path is "public-form.html".
import { test, expect } from "@playwright/test";

const PASS = "7." + "a".repeat(64);
const PASS_KEY = "atlas.pass.public-form.html";
const field = (key, label) => ({ type: "textfield", key, label, id: "f_" + key });
const START = { processName: "Reiseantrag", schema: { type: "default", components: [field("customer", "Name")] } };
const FIRST = { taskKey: 11, title: "Ihre Angaben", schema: { type: "default", components: [field("weiter", "Weiter")] } };
const SECOND = { taskKey: 12, title: "Reiseziel", schema: { type: "default", components: [field("ziel", "Ziel")] }, data: { ziel: "Rom" } };

// mock answers the public routes. completions maps a screen's key to what completing it
// answers; looks is what GET /public/wizard/step answers, one per call, the last repeated.
function mock(page, { start, completions = {}, looks = [] }) {
  const seen = [];
  page.route("**/api/v1/settings/**", (route) => route.fulfill({ status: 404, body: "" }));
  page.route("**/public/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    seen.push({ method: req.method(), path, auth: req.headers()["authorization"] || "", body: req.postData() || "" });
    if (path.endsWith("/schema")) return route.fulfill({ json: START });
    if (path.endsWith("/start")) return route.fulfill({ json: start });
    const done = path.match(/\/public\/wizard\/steps\/(\d+)\/complete$/);
    if (done) return route.fulfill({ json: completions[done[1]] });
    if (path === "/public/wizard/step") {
      const answer = looks.length > 1 ? looks.shift() : looks[0];
      return answer ? route.fulfill(answer.status ? { status: answer.status, json: { error: "gone" } } : { json: answer }) : route.fulfill({ status: 404, json: {} });
    }
    return route.fulfill({ status: 404, body: "" });
  });
  return seen;
}

async function fill(page, label, value) {
  await page.getByLabel(label).fill(value);
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

test("the start carries the visitor on, screen by screen, with the pass", async ({ page }) => {
  const seen = mock(page, {
    start: { started: true, wizard: { pass: PASS, state: "step", step: FIRST } },
    completions: { 11: { state: "step", step: SECOND }, 12: { state: "done" } },
  });
  await page.goto("/public-form.html");
  await fill(page, "Name", "Acme");
  await page.locator("#submit").click();

  await expect(page.locator("h1")).toHaveText("Ihre Angaben");
  expect(await page.evaluate((k) => sessionStorage.getItem(k), PASS_KEY)).toBe(PASS);
  await fill(page, "Weiter", "a");
  await page.locator("#submit").click();

  // The next screen comes up prefilled with what the instance holds for it.
  await expect(page.locator("h1")).toHaveText("Reiseziel");
  await expect(page.getByLabel("Ziel")).toHaveValue("Rom");
  await page.locator("#submit").click();

  await expect(page.locator("h1")).toHaveText("Thank you");
  expect(await page.evaluate((k) => sessionStorage.getItem(k), PASS_KEY)).toBeNull();
  const completions = seen.filter((s) => s.path.endsWith("/complete"));
  expect(completions.map((s) => s.auth)).toEqual(["Bearer " + PASS, "Bearer " + PASS]);
  expect(JSON.parse(completions[0].body)).toEqual({ variables: { weiter: "a" } });
  // The start itself carries no pass: there is none yet.
  expect(seen.find((s) => s.path.endsWith("/start")).auth).toBe("");
  expect(page.__errors).toEqual([]);
});

test("a start into no wizard thanks the visitor as before", async ({ page }) => {
  mock(page, { start: { started: true } });
  await page.goto("/public-form.html");
  await fill(page, "Name", "Acme");
  await page.locator("#submit").click();
  await expect(page.locator("h1")).toHaveText("Thank you");
  expect(page.__errors).toEqual([]);
});

test("a reload resumes the sitting from the pass the tab kept", async ({ page }) => {
  await page.addInitScript(([k, v]) => sessionStorage.setItem(k, v), [PASS_KEY, PASS]);
  const seen = mock(page, { looks: [{ state: "step", step: SECOND }] });
  await page.goto("/public-form.html");
  await expect(page.locator("h1")).toHaveText("Reiseziel");
  expect(seen.some((s) => s.path.endsWith("/schema"))).toBe(false);
  expect(seen.find((s) => s.path === "/public/wizard/step").auth).toBe("Bearer " + PASS);
  expect(page.__errors).toEqual([]);
});

test("while the next screen is prepared, the page waits and then shows it", async ({ page }) => {
  mock(page, {
    start: { started: true, wizard: { pass: PASS, state: "step", step: FIRST } },
    completions: { 11: { state: "pending" } },
    looks: [{ state: "pending" }, { state: "step", step: SECOND }],
  });
  await page.goto("/public-form.html");
  await fill(page, "Name", "Acme");
  await page.locator("#submit").click();
  await fill(page, "Weiter", "b");
  await page.locator("#submit").click();

  await expect(page.locator("h1")).toHaveText("One moment");
  await expect(page.locator("h1")).toHaveText("Reiseziel", { timeout: 15000 });
  expect(page.__errors).toEqual([]);
});

test("a pass that opens nothing any more is forgotten, and the start form is offered", async ({ page }) => {
  await page.addInitScript(([k, v]) => sessionStorage.setItem(k, v), [PASS_KEY, PASS]);
  mock(page, { looks: [{ status: 404 }] });
  await page.goto("/public-form.html");
  await expect(page.locator("h1")).toHaveText("Reiseantrag");
  expect(await page.evaluate((k) => sessionStorage.getItem(k), PASS_KEY)).toBeNull();
  expect(page.__errors).toEqual([]);
});
