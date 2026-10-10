// End-to-end coverage for the Tasks app carrying a person through a wizard
// (api/web/app.js, ADR-0449). A wizard is a subprocess whose user tasks are the screens
// of one multi-step form. Completing one of them answers with where the sitting now
// stands, and the app follows that answer instead of advancing to the next slot of the
// queue: it opens the next screen, waits for it while a worker stands between two, or
// says the form is done.
//
// This drives the REAL app shell against a mocked /api/v1.
import { test, expect } from "@playwright/test";

const listing = (items, extra = {}) => ({
  items, total: items.length, totalExact: true, truncated: false, ...extra,
});

const SITTING = 777;
const task = (key, name, wizard) => ({
  key, processInstanceKey: 9001, elementInstanceKey: 9100 + key, processDefKey: 1,
  processId: "reiseantrag", elementId: "ut_" + key, name, priority: 50,
  ...(wizard ? { wizardScopeKey: SITTING, wizard: "internal" } : {}),
});

const FIRST = task(201, "Persönliche Angaben", true);
const OTHER = task(202, "Andere Aufgabe", false);
const SECOND = task(203, "Reiseziel", true);

// installMock answers the app's calls. completion is the body POST …/complete answers
// with; nextAfter is how many looks for the sitting's next screen come back empty before
// it appears.
function installMock(page, { completion, nextAfter = 0 }) {
  const seen = [];
  let looks = 0;
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    seen.push(route.request().method() + " " + path + url.search);
    const done = seen.some((s) => s.includes("/complete"));
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/complete")) return route.fulfill({ json: completion });
    if (path === "/api/v1/instances") return route.fulfill({ json: listing([]) });
    if (path === "/api/v1/tasks" && url.searchParams.get("processInstance")) {
      looks++;
      return route.fulfill({ json: listing(looks > nextAfter ? [SECOND] : []) });
    }
    if (path === "/api/v1/tasks") {
      const second = completion.wizard && completion.wizard.state === "step";
      return route.fulfill({ json: listing(done ? (second ? [OTHER, SECOND] : [OTHER]) : [FIRST, OTHER]) });
    }
    return route.fulfill({ json: [] });
  });
  return seen;
}

async function completeFirst(page) {
  await page.goto("/index.html#/tasks");
  await expect(page.locator(".tasks-item").first()).toBeVisible({ timeout: 15000 });
  await page.locator('.tasks-item[data-key="201"]').click();
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Persönliche Angaben");
  // The first screen says it is one, with nothing before it yet.
  await expect(page.locator(".tasks-wizard h2")).toHaveText(/Step 1/);
  await page.locator("#task-complete").click();
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

test("the next screen opens after a step, not the next task in the queue", async ({ page }) => {
  installMock(page, {
    completion: { taskKey: 201, wizard: { state: "step", scopeKey: SITTING, processInstanceKey: 9001, task: SECOND } },
  });
  await completeFirst(page);

  // The queue's next slot would be "Andere Aufgabe"; the form's next screen wins.
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Reiseziel");
  await expect(page.locator(".tasks-wizard h2")).toHaveText(/Step 2/);
  await expect(page.locator(".tasks-wizard")).toContainText("Persönliche Angaben");
  expect(page.__errors).toEqual([]);
});

test("while a worker stands between two screens, the pane waits and then opens the next one", async ({ page }) => {
  const seen = installMock(page, {
    completion: { taskKey: 201, wizard: { state: "pending", scopeKey: SITTING, processInstanceKey: 9001 } },
    nextAfter: 1,
  });
  await completeFirst(page);

  await expect(page.locator(".tasks-wizard-wait")).toContainText("Waiting for the next step");
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Reiseziel", { timeout: 15000 });
  await expect(page.locator(".tasks-wizard h2")).toHaveText(/Step 2/);
  // It looked by instance, which is what finds the one sitting's screens.
  expect(seen.filter((s) => s.startsWith("GET /api/v1/tasks?processInstance=9001")).length).toBeGreaterThanOrEqual(2);
  expect(page.__errors).toEqual([]);
});

test("picking another task ends the wait", async ({ page }) => {
  const seen = installMock(page, {
    completion: { taskKey: 201, wizard: { state: "pending", scopeKey: SITTING, processInstanceKey: 9001 } },
    nextAfter: 100,
  });
  await completeFirst(page);
  await expect(page.locator(".tasks-wizard-wait")).toBeVisible();

  await page.locator('.tasks-item[data-key="202"]').click();
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Andere Aufgabe");
  const looks = seen.filter((s) => s.includes("processInstance=")).length;
  await page.waitForTimeout(4000);
  expect(seen.filter((s) => s.includes("processInstance=")).length).toBe(looks);
  expect(page.__errors).toEqual([]);
});

test("a finished form says so and the queue carries on", async ({ page }) => {
  installMock(page, {
    completion: { taskKey: 201, wizard: { state: "done", scopeKey: SITTING, processInstanceKey: 9001 } },
  });
  await completeFirst(page);

  await expect(page.locator("#toast")).toHaveText("All steps of this form are done");
  await expect(page.locator(".tasks-detail-head h1")).toHaveText("Andere Aufgabe");
  await expect(page.locator(".tasks-wizard")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
