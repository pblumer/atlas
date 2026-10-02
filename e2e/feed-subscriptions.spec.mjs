// End-to-end coverage for push delivery of the event feed in the Console
// (ADR-draft-the-event-feed-is-pushed-to-a-cloudevents-endpoint): a CloudEvents endpoint
// Worker's Feed… panel lists what the worker is sent and how delivery stands, and
// subscribes, pauses, rewinds and ends a subscription.
import { test, expect } from "@playwright/test";

function installMock(page) {
  const state = {
    subs: [
      {
        id: "fs-held", workerId: "w-billing", workerName: "billing", reach: [], cursor: 41, enabled: true,
        hold: { failures: 3, failingSince: "2026-10-02T08:00:00Z", retryAt: "2026-10-02T08:01:10Z", lastError: "the endpoint answered 503 Service Unavailable" },
      },
    ],
    requests: [],
  };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    const method = req.method();
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/connectors") || path.endsWith("/configured-workers")) {
      return route.fulfill({ json: [{ id: "w-billing", name: "billing", kind: "cloudevents", role: "owner", endpoint: "https://billing.example/events", credentialsRef: "billing_feed_token", enabled: true }] });
    }
    if (path.endsWith("/catalogs")) {
      return route.fulfill({ json: [{ id: "cat-hr", texts: { de: "Personal" }, languages: ["de"] }] });
    }
    if (path.endsWith("/feed-subscriptions") && method === "GET") return route.fulfill({ json: state.subs });
    if (path.endsWith("/feed-subscriptions") && method === "POST") {
      const body = req.postDataJSON();
      state.requests.push({ method, body });
      const sub = { id: "fs-new", workerId: body.workerId, reach: body.reach, cursor: 57, enabled: true };
      state.subs = [...state.subs, sub];
      return route.fulfill({ status: 201, json: sub });
    }
    const one = path.match(/\/feed-subscriptions\/([^/]+)$/);
    if (one && method === "PATCH") {
      const body = req.postDataJSON();
      state.requests.push({ method, id: one[1], body });
      state.subs = state.subs.map((s) => (s.id === one[1] ? { ...s, ...("enabled" in body ? { enabled: body.enabled } : {}), hold: undefined } : s));
      return route.fulfill({ json: state.subs.find((s) => s.id === one[1]) });
    }
    if (one && method === "DELETE") {
      state.requests.push({ method, id: one[1] });
      state.subs = state.subs.filter((s) => s.id !== one[1]);
      return route.fulfill({ status: 204, body: "" });
    }
    return route.fulfill({ json: [] });
  });
  return state;
}

async function bootApp(page) {
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
}

const goto = async (page, hash) => {
  await page.evaluate((h) => { location.hash = h; }, hash);
  await page.waitForTimeout(400);
};

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__state = installMock(page);
  await bootApp(page);
});

const openFeed = async (page) => {
  await goto(page, "#/console/workers");
  const row = page.locator("#worker-rows tr").first();
  await row.locator("td.row-actions .dropdown-toggle").click();
  await row.locator('td.row-actions .dropdown-menu button[data-act="feed"]').click();
  return page.locator("tr.subs-row");
};

test("a CloudEvents endpoint's Feed… panel shows a failing subscription as held, with why", async ({ page }) => {
  const panel = await openFeed(page);
  const held = panel.locator('tr[data-sid="fs-held"]');
  await expect(held).toContainText("the whole feed");
  await expect(held).toContainText("held");
  await expect(held).toContainText("503 Service Unavailable");
  await expect(held).toContainText("position 41");
  expect(page.__errors).toEqual([]);
});

test("subscribing narrows to the catalogues picked and starts where it is told", async ({ page }) => {
  const panel = await openFeed(page);
  await panel.locator('#feed-form select[name="reach"]').selectOption("cat-hr");
  await panel.locator('#feed-form select[name="from"]').selectOption("now");
  await panel.locator('#feed-form button[type="submit"]').click();
  const added = page.locator('tr.subs-row tr[data-sid="fs-new"]');
  await expect(added).toContainText("Personal");
  await expect(added).toContainText("nothing delivered yet");
  const post = page.__state.requests.find((r) => r.method === "POST");
  expect(post.body).toEqual({ workerId: "w-billing", reach: ["cat-hr"], from: "now", batchSize: 0 });
  expect(page.__errors).toEqual([]);
});

test("a subscription is paused, rewound and ended from its row", async ({ page }) => {
  const panel = await openFeed(page);
  const held = panel.locator('tr[data-sid="fs-held"]');
  await held.locator("button[data-ftoggle]").click();
  await expect(page.locator('tr.subs-row tr[data-sid="fs-held"]')).toContainText("off");
  await page.locator('tr.subs-row tr[data-sid="fs-held"] button[data-ffrom="oldest"]').click();
  await page.locator('tr.subs-row tr[data-sid="fs-held"] button[data-fdel]').click();
  await expect(page.locator("tr.subs-row")).toContainText("Not subscribed");
  const done = page.__state.requests.map((r) => [r.method, r.body]);
  expect(done).toEqual([["PATCH", { enabled: false }], ["PATCH", { from: "oldest" }], ["DELETE", undefined]]);
  expect(page.__errors).toEqual([]);
});

test("the New worker form explains a CloudEvents endpoint and links its runbook", async ({ page }) => {
  await goto(page, "#/console/workers");
  await page.click("#new-worker");
  await page.selectOption('.worker-form [name="kind"]', "cloudevents");
  const doc = page.locator(".conn-setup .wtdoc");
  await expect(doc.locator(".wtdoc-link")).toHaveAttribute("href", "/handbuch.html#runbook-cloudevents");
  await expect(doc.locator(".wtdoc-needs")).toContainText("CloudEvents batches over https");
  expect(page.__errors).toEqual([]);
});
