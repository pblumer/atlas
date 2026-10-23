// End-to-end coverage for the Operations page *Event log* (ADR-0472). Drives
// the REAL page against a mocked /api/v1: a send stands at the head of its group with
// the receptions it caused under it, a send nobody received is marked unless the buffer
// kept it, an expiry is marked, instances and definitions link to their pages, the
// filters travel as query parameters, "Older" pages back with `before`, and Live puts
// what came `after` the newest row on top. The Go suite decides what the route answers
// and to whom; this checks the wiring.
import { test, expect } from "@playwright/test";

const party = (o) => ({ ...o });

const PAGE = {
  rows: [
    { position: "40", cause: "39", at: "2026-10-22T10:00:04Z", kind: "message.sent", channel: "message", name: "lonely",
      correlationKey: "k-1", source: "publish", reached: 0, started: 0 },
    { position: "31", cause: "30", at: "2026-10-22T10:00:03Z", kind: "message.sent", channel: "message", name: "kept.one",
      source: "publish", reached: 0, started: 0 },
    { position: "29", cause: "30", at: "2026-10-22T10:00:03Z", kind: "message.kept", channel: "message", name: "kept.one",
      expiresAt: "2026-10-22T11:00:03Z" },
    { position: "22", cause: "20", at: "2026-10-22T10:00:02Z", kind: "signal.thrown", channel: "signal", name: "evt.ping",
      source: "process", reached: 1, started: 1,
      sender: party({ instanceKey: 501, definitionKey: 9, processId: "ping-thrower", processName: "Ping thrower", version: 2, elementId: "ping" }) },
    { position: "21", cause: "20", at: "2026-10-22T10:00:02Z", kind: "signal.started", channel: "signal", name: "evt.ping",
      sender: party({ instanceKey: 501 }), receiver: party({ definitionKey: 11, processId: "starter", version: 1, elementId: "s" }) },
    { position: "20", cause: "20", at: "2026-10-22T10:00:02Z", kind: "signal.received", channel: "signal", name: "evt.ping",
      sender: party({ instanceKey: 501 }),
      receiver: party({ instanceKey: 400, definitionKey: 8, processId: "ping-catcher", processName: "Ping catcher", version: 1, elementId: "wait" }) },
    { position: "12", cause: "11", at: "2026-10-22T10:00:01Z", kind: "message.expired", channel: "message", name: "late",
      correlationKey: "7" },
  ],
  next: "12", latest: "40", more: true, prunedThrough: "0",
};

const OLDER = {
  rows: [{ position: "5", cause: "4", at: "2026-10-22T09:59:00Z", kind: "signal.thrown", channel: "signal", name: "old.one",
    source: "process", reached: 0, started: 0, sender: party({ instanceKey: 77, definitionKey: 9, processId: "ping-thrower", version: 1 }) }],
  next: "5", latest: "5", more: false, prunedThrough: "0",
};

const NEWER = {
  rows: [{ position: "45", cause: "44", at: "2026-10-22T10:00:09Z", kind: "message.sent", channel: "message", name: "fresh",
    source: "inbound", reached: 1, started: 0 }],
  next: "45", latest: "45", more: false, prunedThrough: "0",
};

function installMock(page) {
  const calls = [];
  let followed = false;
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "u1", username: "op", roles: ["operator"] } } });
    }
    if (path.endsWith("/event-log")) {
      calls.push(url.search);
      const q = url.searchParams;
      if (q.get("before")) return route.fulfill({ json: OLDER });
      if (q.get("after")) {
        if (!followed && q.get("after") === "40") { followed = true; return route.fulfill({ json: NEWER }); }
        return route.fulfill({ json: { rows: [], next: q.get("after"), latest: q.get("after"), more: false, prunedThrough: "0" } });
      }
      return route.fulfill({ json: PAGE });
    }
    return route.fulfill({ json: [] });
  });
  return calls;
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

const row = (page, pos) => page.locator(`#el-rows tr[data-pos="${pos}"]`);

test("a send heads its group, and what nobody received is marked", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/operations/event-log");
  await expect(page.locator("#el-rows tr.el-row")).toHaveCount(7);

  // A publish that reached nobody and was not kept is lost, and says so.
  await expect(row(page, "40")).toContainText("nobody received it");
  await expect(row(page, "40")).toContainText("k-1");
  // The buffer kept the other one: no warning, and the keeping sits under the send.
  await expect(row(page, "31")).not.toContainText("nobody received it");
  await expect(row(page, "31")).toContainText("kept");
  await expect(row(page, "29")).toHaveClass(/el-child/);
  await expect(row(page, "29")).toContainText("kept until");

  // The throw names its thrower and its reach; the start and the reception follow it.
  await expect(row(page, "22")).toContainText("thrown");
  await expect(row(page, "22")).toContainText("reached 1 · started 1");
  await expect(row(page, "22").locator('a[href="#/operations/p/9"]')).toHaveText("Ping thrower");
  await expect(row(page, "22").locator('a[href="#/operations/i/501"]')).toHaveCount(1);
  await expect(row(page, "21")).toHaveClass(/el-child/);
  await expect(row(page, "21")).toContainText("started an instance");
  await expect(row(page, "20")).toHaveClass(/el-child/);
  await expect(row(page, "20")).toContainText("received");
  await expect(row(page, "20").locator('a[href="#/operations/i/400"]')).toHaveCount(1);
  await expect(row(page, "20")).toContainText("wait");
  // The name is said once per group.
  await expect(row(page, "20").locator("td").nth(2)).toHaveText("");

  await expect(row(page, "12")).toContainText("expired unreceived");
  expect(page.__errors).toEqual([]);
});

test("the filters travel to the route, and Older reads before the oldest row", async ({ page }) => {
  const calls = installMock(page);
  await page.goto("/index.html#/operations/event-log?name=evt.ping&channel=signal&instance=400");
  await expect(page.locator("#el-rows tr.el-row").first()).toBeVisible({ timeout: 15000 });
  await expect(page.locator("#el-channel")).toHaveValue("signal");
  await expect(page.locator("#el-name")).toHaveValue("evt.ping");
  await expect(page.locator("#el-instance")).toHaveValue("400");
  const first = new URLSearchParams(calls[0]);
  expect([first.get("channel"), first.get("name"), first.get("instance")]).toEqual(["signal", "evt.ping", "400"]);

  await page.locator("#el-live").uncheck();
  await page.locator("#el-older").click();
  await expect(row(page, "5")).toContainText("old.one");
  await expect(page.locator("#el-older")).toHaveCount(0);
  expect(calls.some((c) => new URLSearchParams(c).get("before") === "12")).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("Live puts what came after the newest row on top", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/operations/event-log");
  await expect(page.locator("#el-rows tr.el-row")).toHaveCount(7);
  await expect(page.locator("#el-rows tr.el-row").first()).toHaveAttribute("data-pos", "45", { timeout: 10000 });
  await expect(row(page, "45")).toContainText("an inbound watch");
  await expect(page.locator("#el-rows tr.el-row")).toHaveCount(8);
  expect(page.__errors).toEqual([]);
});

test("the Operations navigation offers the page", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/operations/event-log");
  await expect(page.locator('a[href="#/operations/event-log"]').first()).toBeAttached();
  expect(page.__errors).toEqual([]);
});
