// End-to-end coverage for the customer wizard (api/web/reisebuchung-kunde.html): it
// starts a real process instance and then has to find that instance again, twice —
// once for its key right after the start, and once per step to ask whether the
// process has ended.
//
// Both answers used to come out of GET /api/v1/instances, and that listing is a page
// rather than the set. Unscoped it is capped, and its active half is scanned in
// ascending instance-key order — oldest first — so the newest instance is the first
// row the cap drops. On a server holding more active instances than the cap, the
// wizard's own instance is therefore never on the page it was searching, and the
// start died on `Cannot read properties of undefined (reading 'key')` while the
// instance sat happily on its first user task.
//
// The mock below IS that server: the bare listing answers with a full page of other
// definitions' instances and never contains ours, exactly as a loaded engine does.
// A page that reads it cannot get past the start button. The scoped listing
// (?process=) and the per-key point read (/instances/search?q=) answer truthfully,
// because those are the reads the wizard is supposed to make.
//
// The static e2e harness runs no Go server, so what is verified here is the wiring
// and the call sequence, not the server side; the endpoints' own behaviour is pinned
// by TestANewInstanceIsOffTheCappedListingButOnItsOwnDefinitionsPage in api/.
import { test, expect } from "@playwright/test";

const DEF_KEY = 213;
const INSTANCE = 281474990452515; // a realistic key: high, and far past the cap
const TASK_KEY = 77;

const STEP_FORM = {
  type: "default",
  id: "reisedaten",
  components: [{ type: "textfield", key: "reiseziel", label: "Reiseziel" }],
};

// cappedPage is what a busy engine answers the unscoped listing with: the oldest
// active instances it holds, none of them ours. Three rows stand in for a thousand —
// the ordering is the point, not the count.
const cappedPage = [
  { key: 281474978838864, processDefKey: 92, processId: "onboarding-atlas", version: 2, state: "active", variables: [] },
  { key: 281474978841949, processDefKey: 113, processId: "andere", version: 1, state: "active", variables: [] },
  { key: 281474979489879, processDefKey: 386, processId: "massentest", version: 2, state: "active", variables: [] },
];

// mock answers the endpoints the wizard walks, in the shapes the page reads, and
// records every call so a test can assert what it asked for.
function mock(page, calls) {
  let started = false;
  let taskOpen = true;

  const ours = () => ({
    key: INSTANCE,
    processDefKey: DEF_KEY,
    processId: "proc_reisebuchung",
    version: 3,
    state: taskOpen ? "active" : "completed",
    variables: [],
  });

  return page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname + url.search;
    calls.push(`${req.method()} ${path}`);

    if (path === "/api/v1/auth/me") {
      return route.fulfill({ json: { username: "kundin" } });
    }
    if (path === "/api/v1/processes") {
      return route.fulfill({
        json: [{ key: DEF_KEY, processId: "proc_reisebuchung", version: 3 }],
      });
    }
    if (path === `/api/v1/processes/${DEF_KEY}/instances` && req.method() === "POST") {
      started = true;
      return route.fulfill({ json: { definitionKey: DEF_KEY, stats: {} } });
    }
    // The capped, oldest-first page of a loaded engine — ours is never on it.
    if (path === "/api/v1/instances") {
      return route.fulfill({ json: cappedPage, headers: { "X-Instances-Truncated": "true" } });
    }
    if (path === `/api/v1/instances?process=${DEF_KEY}`) {
      return route.fulfill({ json: started ? [ours()] : [] });
    }
    if (path === `/api/v1/instances/search?q=${INSTANCE}`) {
      return route.fulfill({ json: started ? [ours()] : [] });
    }
    if (path === `/api/v1/tasks?processInstance=${INSTANCE}`) {
      return route.fulfill({
        json: taskOpen
          ? [{
              key: TASK_KEY,
              processInstanceKey: INSTANCE,
              elementId: "erfassen",
              name: "Reisedaten",
              formId: "reise-start",
            }]
          : [],
      });
    }
    if (path === "/api/v1/forms/reise-start") {
      return route.fulfill({ json: { schema: STEP_FORM } });
    }
    if (path === `/api/v1/tasks/${TASK_KEY}/complete` && req.method() === "POST") {
      taskOpen = false;
      return route.fulfill({ json: {} });
    }
    return route.fulfill({ status: 404, body: "not mocked: " + path });
  });
}

test("the wizard starts and finishes an instance on an engine whose instance listing is full", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const calls = [];
  await mock(page, calls);

  await page.goto("/reisebuchung-kunde.html");
  await expect(page.locator("#account")).toContainText("kundin");

  // START. Before the fix this is where it ended: the diff of the capped listing
  // found no row for the new instance and the page threw on `fresh.key`.
  await page.click("#startBtn");
  await expect(page.locator("#instHint")).toHaveText(`#${INSTANCE} · Schritt 1`);
  await expect(page.locator(".steptitle")).toHaveText("Reisedaten");
  await expect(page.locator(".fjs-input")).toHaveCount(1);

  // COMPLETE. The wizard now asks whether the instance has ended, which is the
  // second read that used to go through the capped listing.
  await page.fill(".fjs-input", "Sylt");
  await page.click("#next");
  await expect(page.locator(".banner")).toContainText("Buchung abgeschlossen");
  await expect(page.locator("#instHint")).toHaveText(`#${INSTANCE} · abgeschlossen`);

  // It got there off the index-backed reads, and never touched the unscoped listing.
  expect(calls).toContain(`GET /api/v1/instances?process=${DEF_KEY}`);
  expect(calls).toContain(`GET /api/v1/instances/search?q=${INSTANCE}`);
  expect(calls.filter((c) => c === "GET /api/v1/instances")).toEqual([]);
  expect(errors).toEqual([]);
});
