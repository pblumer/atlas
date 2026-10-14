// End-to-end coverage for the customer assistant (api/web/reisebuchung-kunde.html) on
// the public wizard of examples/reisebuchung (ADR-0449). The page used to sign a person
// in, list the instance's open tasks after every submit and render the first one it
// found, which could not tell "the engine is still working" from "this step belongs to
// somebody else". It now follows what the public routes answer: the start hands a pass
// and the first screen, each completion the next screen, "pending" or "done".
//
// The static e2e harness runs no Go server, so what is verified here is the wiring and
// the call sequence: which calls the page makes, that the pass travels only in the
// Authorization header, that the screen's documentation is shown as text, and that a
// reload resumes the sitting. The server side of the same walk is pinned against the
// real example files by TestTheTripBookingExample* in api/.
import { test, expect } from "@playwright/test";

const TOKEN = "b6f0c1d2e3a4";
const PASS = "281474990452515." + "ab".repeat(32);

const START_FORM = {
  type: "default",
  id: "reise-start",
  components: [{ type: "textfield", key: "ziel", label: "Ziel" }],
};
const VISUM = {
  taskKey: 11,
  title: "Visum-Angaben",
  documentation: "Dieser Schritt erscheint, weil die Entscheidungstabelle ReisePflichten für eine Fernreise ein Visum verlangt. <b>kein HTML</b>",
  schema: { type: "default", components: [{ type: "textfield", key: "staatsangehoerigkeit", label: "Staatsangehörigkeit" }] },
  data: {},
};
const BESTAETIGEN = {
  taskKey: 12,
  title: "Buchung bestätigen",
  documentation: "Der letzte Schritt, immer nötig.",
  schema: {
    type: "default",
    components: [
      { type: "number", key: "alter", label: "Alter am Stichtag (berechnet)", disabled: true },
      { type: "checkbox", key: "buchungBestaetigt", label: "Buchung verbindlich bestätigen" },
    ],
  },
  data: { alter: 16 },
};

// mock answers the public routes in the shapes the server sends, and records every
// call with the Authorization header it carried.
function mock(page, calls) {
  let current = VISUM; // the open screen of the sitting once it has started; null when done
  return page.route(/\/(public|api)\//, async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const auth = req.headers()["authorization"] || "";
    calls.push({ call: `${req.method()} ${url.pathname}`, auth, body: req.postData() || "" });

    if (url.pathname === `/public/forms/${TOKEN}/schema`) {
      return route.fulfill({ json: { processName: "Reisebuchung", schema: START_FORM } });
    }
    if (url.pathname === `/public/forms/${TOKEN}/start` && req.method() === "POST") {
      return route.fulfill({ json: { started: true, wizard: { pass: PASS, state: "step", step: VISUM } } });
    }
    if (auth !== "Bearer " + PASS) {
      return route.fulfill({ status: 404, json: { error: "no such sitting" } });
    }
    // Between the visa screen and the confirmation the engine is still working once.
    if (url.pathname === `/public/wizard/steps/${VISUM.taskKey}/complete`) {
      current = BESTAETIGEN;
      return route.fulfill({ json: { state: "pending" } });
    }
    if (url.pathname === "/public/wizard/step") {
      return route.fulfill({ json: current ? { state: "step", step: current } : { state: "done" } });
    }
    if (url.pathname === `/public/wizard/steps/${BESTAETIGEN.taskKey}/complete`) {
      current = null;
      return route.fulfill({ json: { state: "done" } });
    }
    return route.fulfill({ status: 404, body: "not mocked: " + url.pathname });
  });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

test("a visitor books a trip screen by screen with the pass, and nothing but the public routes is asked", async ({ page }) => {
  const calls = [];
  await mock(page, calls);
  await page.goto(`/reisebuchung-kunde.html?link=${TOKEN}`);

  // The start form, from the public schema route.
  await expect(page.locator(".steptitle")).toHaveText("Reisedaten");
  await page.fill(".fjs-input", "Thailand");
  await page.click("#next");

  // The start lands on the first screen of the sitting, and says why it is there in the
  // model's own words, as text.
  await expect(page.locator(".steptitle")).toHaveText("Visum-Angaben");
  await expect(page.locator("#why")).toHaveText(VISUM.documentation);
  await expect(page.locator("#why b")).toHaveCount(0);
  await expect(page.locator("#instHint")).toHaveText("#281474990452515 · Schritt 2");

  // The engine is still working after this screen: the page waits, asks again with the
  // pass, and shows what comes next.
  await page.fill(".fjs-input", "CH");
  await page.click("#next");
  await expect(page.locator(".steptitle")).toHaveText("Buchung bestätigen");
  await expect(page.locator(".steps li.done")).toHaveText(["Reisedaten", "Visum-Angaben"]);

  // The computed age is shown and is not the visitor's to send.
  const age = page.getByLabel("Alter am Stichtag (berechnet)");
  await expect(age).toHaveValue("16");
  await expect(age).toBeDisabled();
  await page.check('input[type="checkbox"]');
  await page.click("#next");
  await expect(page.locator(".banner")).toContainText("Buchung abgeschlossen");

  expect(calls.map((c) => c.call)).toEqual([
    `GET /public/forms/${TOKEN}/schema`,
    `POST /public/forms/${TOKEN}/start`,
    `POST /public/wizard/steps/${VISUM.taskKey}/complete`,
    "GET /public/wizard/step",
    `POST /public/wizard/steps/${BESTAETIGEN.taskKey}/complete`,
  ]);
  // The pass rides in the header of every wizard call and in no URL; the start has none.
  for (const c of calls) {
    expect(c.auth).toBe(c.call.includes("/public/wizard/") ? "Bearer " + PASS : "");
    expect(c.call).not.toContain(PASS.split(".")[1]);
  }
  expect(JSON.parse(calls[4].body)).toEqual({ variables: { buchungBestaetigt: true } });
  // The call log on the page shows the calls, never the pass.
  await expect(page.locator("#log")).not.toContainText(PASS.split(".")[1]);
});

test("a reload resumes the sitting from the pass the tab kept", async ({ page }) => {
  const calls = [];
  await mock(page, calls);
  await page.goto(`/reisebuchung-kunde.html?link=${TOKEN}`);
  await page.fill(".fjs-input", "Thailand");
  await page.click("#next");
  await expect(page.locator(".steptitle")).toHaveText("Visum-Angaben");

  calls.length = 0;
  await page.reload();
  await expect(page.locator(".steptitle")).toHaveText("Visum-Angaben");
  expect(calls.map((c) => c.call)).toEqual(["GET /public/wizard/step"]);
  expect(calls[0].auth).toBe("Bearer " + PASS);
});

test("a pass that opens nothing any more is forgotten, and the start form is offered", async ({ page }) => {
  const calls = [];
  await mock(page, calls);
  await page.addInitScript(([key]) => sessionStorage.setItem(key, "1.deadbeef"), [`atlas.reise.pass.${TOKEN}`]);
  await page.goto(`/reisebuchung-kunde.html?link=${TOKEN}`);
  await expect(page.locator(".steptitle")).toHaveText("Reisedaten");
  expect(calls.map((c) => c.call)).toEqual(["GET /public/wizard/step", `GET /public/forms/${TOKEN}/schema`]);
});

test("without a link the page says how to get one, and calls nothing", async ({ page }) => {
  const calls = [];
  await mock(page, calls);
  await page.goto("/reisebuchung-kunde.html");
  await expect(page.locator("#body")).toContainText("public-links");
  expect(calls).toEqual([]);
});
