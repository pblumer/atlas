// End-to-end coverage for the Console's Events page (ADR-0435 §7). Drives the REAL
// page against a mocked /api/v1: everybody with modeler or above reads the catalogue's
// entries; who listens now is a second, administrator-only route, so a modeler's page
// has no "listening now" column at all, while an administrator's counts the listeners,
// names them per event with the personal data each receives, and warns about models
// that wait for an atlas.* name atlas never emits. The Go suite decides what the routes
// answer; this checks the wiring.
import { test, expect } from "@playwright/test";

const field = (name, personal, always = true) => ({ name, type: "string", always, data: personal ? "personal" : "not-personal", meaning: { en: `the ${name}`, de: name } });

const CATALOG = {
  entries: [
    { type: "atlas.order.placed", kind: "domain", channels: ["message"], since: "0.7.0", stability: "stable",
      meaning: { en: "An order was placed.", de: "Eine Bestellung wurde aufgegeben." },
      moment: { process: "atlas-auftrag-erfuellung", element: "Start", producer: "the shop's order route" },
      payload: [field("orderId", false)], neverSecret: "TestTheOrderMessagesCarryNoSecret",
      access: { message: "Atlas's own fulfilment process" } },
    { type: "atlas.entitlement.granted", kind: "platform", channels: ["feed"], since: "Unreleased", stability: "stable",
      meaning: { en: "A right was granted.", de: "Ein Recht wurde erteilt." },
      moment: { producer: "the entitlement store" },
      payload: [field("principal", false), field("until", false, false)], neverSecret: "TestTheFeedCarriesNoSecret",
      access: { feed: "feedreader, or an events token" } },
    { type: "<message>.<outcome>", shaped: true, kind: "domain", channels: ["feed"], since: "Unreleased", stability: "stable",
      meaning: { en: "An action ended.", de: "Eine Aktion endete." }, moment: { producer: "the outcome route" },
      payload: [], neverSecret: "TestTheFeedCarriesNoSecret", access: { feed: "feedreader, or an events token" } },
    { type: "atlas.user.requested", kind: "domain", channels: ["signal"], since: "Unreleased", stability: "experimental",
      listenable: true,
      meaning: { en: "Somebody asked for a <user> account.", de: "Jemand hat ein Benutzerkonto beantragt." },
      moment: { process: "proc_benutzer_aufnahme", element: "beantragt_melden", producer: "the intake process" },
      payload: [field("atlasInstance", false), field("vorname", true), field("email", true)],
      neverSecret: "TestSystemIntakeAnnouncesTheRequestAsASignal", access: { signal: "a deployed model with a signal start or catch" } },
  ],
};

const LISTENERS = (feedDelivered) => ({
  processes: [
    { type: "atlas.order.placed", channel: "message", processId: "atlas-auftrag-erfuellung", processName: "Auftrag erfüllen",
      version: 3, definitionKey: 11, system: true, element: "Start", role: "start", catalogued: true, personal: [] },
    { type: "atlas.user.requested", channel: "signal", processId: "hr-notify", processName: "HR benachrichtigen",
      version: 2, definitionKey: 12, projectId: "p-hr", element: "Start_req", role: "start", catalogued: true,
      personal: ["vorname", "email"] },
    { type: "atlas.user.vanished", channel: "signal", processId: "hr-wait", processName: "Wartet ewig",
      version: 1, definitionKey: 13, projectId: "p-hr", element: "Catch_gone", role: "catch", catalogued: false },
  ],
  feed: [{ subscriptionId: "fs-1", workerId: "w-billing", workerName: "billing", reach: [], enabled: true }],
  feedTypes: ["atlas.entitlement.granted", "<message>.<outcome>"],
  feedDelivered,
});

function installMock(page, { admin, feedDelivered = true }) {
  const calls = [];
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    calls.push(path);
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "u1", username: "mo", roles: [admin ? "admin" : "modeler"] } } });
    }
    if (path.endsWith("/event-catalog")) return route.fulfill({ json: CATALOG });
    if (path.endsWith("/event-catalog/listeners")) {
      return admin
        ? route.fulfill({ json: LISTENERS(feedDelivered) })
        : route.fulfill({ status: 403, json: { error: "forbidden: requires role admin" } });
    }
    return route.fulfill({ json: [] });
  });
  return calls;
}

async function openEvents(page) {
  await page.goto("/index.html#/console/events");
  await expect(page.locator("#ev-rows tr[data-i]").first()).toBeVisible({ timeout: 15000 });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

test("a modeler reads every entry and has no listening-now column", async ({ page }) => {
  const calls = installMock(page, { admin: false });
  await openEvents(page);
  await expect(page.locator("#ev-rows tr[data-i]")).toHaveCount(4);
  await expect(page.locator("#ev-head th")).toHaveCount(6);
  await expect(page.locator("#ev-head")).not.toContainText("Listening now");
  await expect(page.locator(".ev-count")).toHaveCount(0);
  // The shape is marked as one, and an experimental entry says so.
  await expect(page.locator('#ev-rows tr[data-type="<message>.<outcome>"]')).toContainText("shape");
  await expect(page.locator('#ev-rows tr[data-type="atlas.user.requested"] .pill.warn')).toHaveText("experimental");
  // The listeners route was asked and refused; that refusal is the answer.
  expect(calls).toContain("/api/v1/event-catalog/listeners");
  await expect(page.locator(".ev-stray")).toHaveCount(0);

  await page.locator('#ev-rows tr[data-type="atlas.user.requested"]').click();
  const detail = page.locator(".ev-detail");
  await expect(detail).toContainText("Somebody asked for a <user> account.");
  await expect(detail.locator("tr", { hasText: "vorname" }).locator(".pill.warn")).toHaveText("personal");
  await expect(detail.locator("tr", { hasText: "atlasInstance" }).locator(".pill")).toHaveCount(0);
  await expect(detail).toContainText("TestSystemIntakeAnnouncesTheRequestAsASignal");
  await expect(detail.locator(".ev-listeners")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the Console's navigation offers the page to a modeler", async ({ page }) => {
  installMock(page, { admin: false });
  await openEvents(page);
  await expect(page.locator('a[href="#/console/events"]').first()).toBeAttached();
  expect(page.__errors).toEqual([]);
});

test("an administrator sees who listens, per event, with the personal data each receives", async ({ page }) => {
  installMock(page, { admin: true });
  await openEvents(page);
  await expect(page.locator("#ev-head th")).toHaveCount(7);
  await expect(page.locator("#ev-head")).toContainText("Listening now");
  const count = (type) => page.locator(`#ev-rows tr[data-type="${type}"] .ev-count`);
  await expect(count("atlas.order.placed")).toHaveText("1");
  await expect(count("atlas.user.requested")).toHaveText("1");
  // Every feed subscription receives every feed type.
  await expect(count("atlas.entitlement.granted")).toHaveText("1");
  await expect(count("<message>.<outcome>")).toHaveText("1");

  await page.locator('#ev-rows tr[data-type="atlas.user.requested"]').focus();
  await page.keyboard.press("Enter");
  const listeners = page.locator(".ev-detail .ev-listeners");
  await expect(listeners).toContainText("HR benachrichtigen");
  await expect(listeners).toContainText("v2");
  await expect(listeners).toContainText("p-hr");
  await expect(listeners).toContainText("Start_req (start)");
  await expect(listeners).toContainText("vorname, email");
  await expect(page.locator('#ev-rows tr[data-type="atlas.user.requested"]')).toHaveClass(/sel/);

  await page.locator('#ev-rows tr[data-type="atlas.order.placed"]').click();
  await expect(page.locator(".ev-detail .ev-listeners .pill", { hasText: "system" })).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("an administrator is warned about a model waiting for a name atlas never emits", async ({ page }) => {
  installMock(page, { admin: true });
  await openEvents(page);
  const stray = page.locator(".ev-stray");
  await expect(stray).toContainText("Waiting for names atlas never emits.");
  await expect(stray).toContainText("atlas.user.vanished");
  await expect(stray).toContainText("Wartet ewig v1");
  expect(page.__errors).toEqual([]);
});

test("a feed entry lists the subscriptions, and says when the shop is off they wait", async ({ page }) => {
  installMock(page, { admin: true, feedDelivered: false });
  await openEvents(page);
  await page.locator('#ev-rows tr[data-type="atlas.entitlement.granted"]').click();
  const listeners = page.locator(".ev-detail .ev-listeners");
  await expect(listeners).toContainText("No deployed model listens to this name.");
  await expect(listeners).toContainText("billing");
  await expect(listeners).toContainText("the whole feed");
  await expect(listeners).toContainText("the feed is neither served nor pushed");
  expect(page.__errors).toEqual([]);
});
