// End-to-end coverage for the "Held back" card on Operations → Workers (ADR-0340).
//
// This card is the whole reason the breaker is allowed to exist. It turns an outage from
// a flood of incidents into *silence* — work that simply does not happen — and the record
// is explicit that silence is the one failure mode an operator cannot diagnose. A queue
// that is deep because nobody serves it and a queue that is deep because Atlas has
// stopped serving it look identical everywhere else on this page.
import { test, expect } from "@playwright/test";

// A breaker that tripped a minute ago and next probes in ten seconds — the state an
// operator lands on while an SMTP host is down.
const heldRow = () => ({
  jobType: "io.atlas.mail.send",
  connector: "Patrick Blumer",
  state: "open",
  trippedAt: (Date.now() - 60_000) * 1e6,
  reason: 'dial tcp 10.0.0.9:587: connect: connection refused',
  probeAt: (Date.now() + 10_000) * 1e6,
  cooldown: 10e9,
  refused: 412,
});

function installMock(page, { breakers = [], onClose = null } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/workers/breakers/close")) {
      if (onClose) onClose(req.postDataJSON() || {});
      return route.fulfill({ json: { jobType: "io.atlas.mail.send", connector: "Patrick Blumer", closed: true } });
    }
    if (path.endsWith("/api/v1/workers")) {
      return route.fulfill({
        json: {
          workers: [], supervised: [], unservedConnectors: [], jobTypeCollisions: [],
          breakers,
          types: [{
            type: "io.atlas.mail.send", index: 12, servedInProcess: true, builtIn: true,
            leasable: false, parked: 412, inFlight: 0, incidents: 3, processes: [],
          }],
        },
      });
    }
    return route.fulfill({ json: [] });
  });
}

const goto = async (page, hash) => {
  await page.evaluate((h) => { location.hash = h; }, hash);
  await page.waitForTimeout(400);
};

async function bootApp(page) {
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
}

test("names the held target, since when, when it will try again, and why", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, { breakers: [heldRow()] });
  await bootApp(page);
  await goto(page, "#/operations/workers");

  const card = page.locator("#wk-breakers");
  await expect(card).toBeVisible();
  await expect(card).toContainText("Held back");
  // The four questions an operator is about to ask, in one row.
  await expect(card).toContainText("Patrick Blumer");
  await expect(card).toContainText("io.atlas.mail.send");
  await expect(card).toContainText("1m ago");
  await expect(card).toContainText("in 10s");
  await expect(card).toContainText("connection refused");
  // And the sentence that stops the row being read as a failure: the work is waiting.
  await expect(card).toContainText(/waiting, not failed/i);
  expect(errors).toEqual([]);
});

test("stays out of the way when nothing is held", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, { breakers: [] });
  await bootApp(page);
  await goto(page, "#/operations/workers");

  // Hidden rather than "all clear": a healthy server should not grow a table of green
  // rows nobody needs to read.
  await expect(page.locator("#wk-breakers")).toBeHidden();
  expect(errors).toEqual([]);
});

test("Close now releases exactly the target on its row", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const closed = [];
  installMock(page, { breakers: [heldRow()], onClose: (body) => closed.push(body) });
  await bootApp(page);
  await goto(page, "#/operations/workers");

  await page.locator("#wk-breakers button", { hasText: "Close now" }).click();
  await page.waitForTimeout(600);

  expect(closed).toEqual([{ jobType: "io.atlas.mail.send", connector: "Patrick Blumer" }]);
  expect(errors).toEqual([]);
});

test("a probing breaker says so, because it is a different answer to 'what is happening'", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  installMock(page, { breakers: [{ ...heldRow(), state: "probing" }] });
  await bootApp(page);
  await goto(page, "#/operations/workers");

  await expect(page.locator("#wk-breakers")).toContainText("probing");
  expect(errors).toEqual([]);
});
