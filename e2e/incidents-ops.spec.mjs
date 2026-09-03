// End-to-end coverage for incidents on the operator's surfaces: the live view
// (ADR-0150) and, on the same data and with the same block and action, the
// step-by-step replay and the Instances lists (ADR-0151). All of them must show a
// *stuck* token as stuck — marked on the element, listed beside the diagram, and
// resolvable without leaving the view. Driven through the real vendored bpmn-js and
// the real editor.js mounts.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/incidents-ops-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
};

test.describe("live view", () => {
  test("marks the stuck element, counts the incidents, and lists them", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive());

    // The element the tokens are parked on is outlined and badged — the token markers
    // alone said nothing about the instances being blocked.
    await expect(page.locator('#canvas g[data-element-id="Task_pay"]')).toHaveClass(/atlas-incident/);
    const badge = page.locator(".incident-badge");
    await expect(badge).toHaveText(/2 incidents/);
    await expect(badge).toHaveAttribute("title", /payment gateway refused: 503/);
    // Only the stuck element: the start event ran cleanly.
    await expect(page.locator('#canvas g[data-element-id="Start_1"]')).not.toHaveClass(/atlas-incident/);

    // The toolbar answers "is anything in this version stuck?" without a click.
    await expect(page.locator("#incident-pill")).toBeVisible();
    await expect(page.locator("#incident-count")).toHaveText("2");

    // Both instances' incidents lead the variables panel, each naming its instance.
    const rows = page.locator("#var-panel .vp-incidents .inc-row");
    await expect(rows).toHaveCount(2);
    await expect(rows.first()).toContainText("Task_pay");
    await expect(rows.first()).toContainText("instance 900001");
    await expect(rows.first()).toContainText("payment gateway refused: 503");
    expect(page.__errors).toEqual([]);
  });

  test("resolves an incident in place, and the diagram catches up", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive());
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(2);

    await page.locator("#var-panel .inc-row [data-resolve]").first().click();

    // Beside a diagram the resolve is one click for one attempt — no dialog.
    await expect
      .poll(() => page.evaluate(() => window.__resolved))
      .toEqual([{ key: "1001", retries: 1 }]);
    // …and the view re-polled: one incident left, on the same element.
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(1);
    await expect(page.locator("#incident-count")).toHaveText("1");
    // A single incident on the element drops the count from the badge.
    await expect(page.locator(".incident-badge")).toHaveText("⚠ incident");
    expect(page.__errors).toEqual([]);
  });

  test("a single selected instance sees only its own incident", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));

    await expect(page.locator("#incident-count")).toHaveText("1");
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(1);
    await expect(page.locator("#var-panel .inc-row")).toContainText("instance 900001");
    expect(page.__errors).toEqual([]);
  });
});

test.describe("correcting the variables", () => {
  test("fixes the data the retry will run against, then resolves — in one step", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(1);

    // A retry alone would repeat exactly what failed, so the row offers the fix first.
    await page.locator("#var-panel .inc-row [data-fix-vars]").click();
    const modal = page.locator(".modal-ov");
    await expect(modal).toBeVisible();
    // It opens on the instance's *current* variables, not on an empty box.
    const json = modal.locator("#inc-vars");
    expect(JSON.parse(await json.inputValue())).toEqual({ betrag: 42, empfaenger: "" });

    await json.fill(JSON.stringify({ betrag: 43, empfaenger: "patrick@blumer.net" }, null, 2));
    await modal.locator("[data-vars-go]").click();
    await expect(modal).toHaveCount(0);

    // The correction went through the audited override, and the incident was resolved
    // in the same step.
    await expect
      .poll(() => page.evaluate(() => window.__varWrites))
      .toEqual([{ instance: "900001", variables: { betrag: 43, empfaenger: "patrick@blumer.net" } }]);
    await expect
      .poll(() => page.evaluate(() => window.__resolved))
      .toEqual([{ key: "1001", retries: 1 }]);
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(0);
    expect(page.__errors).toEqual([]);
  });

  test("refuses invalid JSON instead of losing the edit", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));
    await page.locator("#var-panel .inc-row [data-fix-vars]").click();

    const modal = page.locator(".modal-ov");
    await modal.locator("#inc-vars").fill("{ betrag: ");
    await modal.locator("[data-vars-go]").click();

    await expect(modal).toBeVisible();                        // still open…
    await expect(modal.locator("#inc-vars-err")).toContainText("Not valid JSON");
    await expect(modal.locator("#inc-vars")).toHaveValue("{ betrag: "); // …and the edit survives
    expect(await page.evaluate(() => window.__varWrites)).toEqual([]);
    expect(page.__errors).toEqual([]);
  });

  test("saving without retrying leaves the incident standing", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));
    await page.locator("#var-panel .inc-row [data-fix-vars]").click();

    const modal = page.locator(".modal-ov");
    await modal.locator("#inc-vars").fill(JSON.stringify({ betrag: 7 }));
    await modal.locator("[data-vars-save]").click();
    await expect(modal).toHaveCount(0);

    await expect.poll(() => page.evaluate(() => window.__varWrites.length)).toBe(1);
    expect(await page.evaluate(() => window.__resolved)).toEqual([]);
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(1);
    expect(page.__errors).toEqual([]);
  });
});

test.describe("reconfiguring the worker", () => {
  test("names the worker on the incident and opens it prefilled", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));
    const row = page.locator("#var-panel .inc-row");
    await expect(row).toHaveCount(1);

    // "no worker registered as X" is unactionable until you know which integration
    // X is — so the row says so, on the incident itself (ADR-0160).
    await expect(row.locator(".inc-conn")).toContainText("mail");
    await expect(row.locator(".inc-conn")).toContainText("Patrick Blumer");

    await row.locator("[data-fix-conn]").click();
    const modal = page.locator(".conn-modal");
    await expect(modal).toBeVisible();
    // It opens on what is stored, says which task is parked on it, and repeats the
    // runtime's own reason the client could not be built.
    await expect(modal.locator("#conn-endpoint")).toHaveValue("smtp.office365.com:587");
    await expect(modal.locator("#conn-credref")).toHaveValue("o365_pw");
    await expect(modal.locator("#conn-provider")).toHaveValue("smtp");
    await expect(modal).toContainText("Task_pay is parked on this worker");
    await expect(modal.locator(".conn-problem")).toContainText("connection refused");

    // And it can be checked before anything is written.
    await modal.locator("[data-conn-test]").click();
    await expect(modal.locator(".conn-test-result")).toContainText("Connected and authenticated");
    expect(await page.evaluate(() => window.__connPatches)).toEqual([]);
    expect(page.__errors).toEqual([]);
  });

  test("switching the provider saves and retries in one step", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));
    await page.locator("#var-panel .inc-row [data-fix-conn]").click();

    const modal = page.locator(".conn-modal");
    // The preview transport dials nothing and authenticates against nothing, so the
    // fields it does not use go away rather than standing there looking used.
    await modal.locator("#conn-provider").selectOption("preview");
    await expect(modal.locator(".conn-f-endpoint")).toBeHidden();
    await expect(modal.locator(".conn-f-credref")).toBeHidden();
    await expect(modal).toContainText("Outbox");

    await modal.locator("[data-conn-extra]").click();
    await expect(page.locator(".conn-modal")).toHaveCount(0);

    // The worker was changed, and the parked job handed one more attempt against
    // the new configuration — one action, not a trip to the Console and back. The
    // fields preview does not use are left out of the patch rather than sent empty;
    // clearing them is the server's rule about the provider, not the form's.
    await expect.poll(() => page.evaluate(() => window.__connPatches)).toEqual([
      { id: "c1", body: { enabled: true, provider: "preview", sender: "bot@example.com" } },
    ]);
    await expect.poll(() => page.evaluate(() => window.__resolved)).toEqual([{ key: "1001", retries: 1 }]);
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(0);
    expect(page.__errors).toEqual([]);
  });

  test("saving without retrying leaves the incident standing", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));
    await page.locator("#var-panel .inc-row [data-fix-conn]").click();

    const modal = page.locator(".conn-modal");
    await modal.locator("#conn-endpoint").fill("smtp.example.com:2525");
    await modal.locator("[data-conn-save]").click();
    await expect(page.locator(".conn-modal")).toHaveCount(0);

    await expect.poll(() => page.evaluate(() => window.__connPatches.length)).toBe(1);
    expect(await page.evaluate(() => window.__connPatches[0].body.endpoint)).toBe("smtp.example.com:2525");
    expect(await page.evaluate(() => window.__resolved)).toEqual([]);
    await expect(page.locator("#var-panel .inc-row")).toHaveCount(1);
    expect(page.__errors).toEqual([]);
  });

  test("a name nobody configured points at the Console instead", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__unconfigureConnector());
    await page.evaluate(() => window.__mountLive(window.__STUCK));

    const row = page.locator("#var-panel .inc-row");
    await expect(row.locator(".inc-conn")).toContainText("not configured");
    // Nothing to open: the fix is to create one, and that lives in the Console.
    await expect(row.locator("[data-fix-conn]")).toHaveCount(0);
    await expect(row.locator('a[href="#/console/connectors"]')).toContainText("Configure");
    expect(page.__errors).toEqual([]);
  });

  test("the replay offers the same worker fix", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountReplay());
    const details = page.locator("#tab-details");
    await expect(details.locator(".vp-incidents .inc-row")).toHaveCount(1);

    // One incident vocabulary across both diagrams (ADR-0151) — the worker included.
    await expect(details.locator(".inc-conn")).toContainText("Patrick Blumer");
    await details.locator("[data-fix-conn]").click();
    await expect(page.locator(".conn-modal")).toBeVisible();
    await page.locator(".conn-modal [data-conn-cancel]").click();
    await expect(page.locator(".conn-modal")).toHaveCount(0);
    expect(await page.evaluate(() => window.__connPatches)).toEqual([]);
    expect(page.__errors).toEqual([]);
  });
});

test.describe("instance replay", () => {
  test("flags the stuck step and resolves it from the Details panel", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountReplay());
    await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();

    // The header says the instance is stuck, beside its state.
    await expect(page.locator("#m-inc-wrap")).toBeVisible();
    await expect(page.locator("#m-inc-n")).toHaveText("1");
    // The history row for the parked element instance is flagged, the earlier one isn't.
    await expect(page.locator('#history-list .ops-hrow[data-eik="1001"]')).toHaveClass(/inc/);
    await expect(page.locator('#history-list .ops-hrow[data-eik="1000"]')).not.toHaveClass(/inc/);
    // The element carries the same ⚠ badge the live view draws…
    await expect(page.locator(".incident-badge")).toHaveText(/incident/);
    // …and keeps its incident outline wherever the playhead sits.
    await expect(page.locator('#canvas g[data-element-id="Task_pay"]')).toHaveClass(/atlas-incident/);
    await page.locator("#step-back").click();
    await expect(page.locator('#canvas g[data-element-id="Task_pay"]')).toHaveClass(/atlas-incident/);

    // With nothing selected the panel already reaches the instance's incidents.
    const details = page.locator("#tab-details");
    await expect(details.locator(".vp-incidents .inc-row")).toContainText("Zahlung auslösen");
    await expect(details.locator(".inc-msg")).toHaveText("payment gateway refused: 503");
    // Scoped to one instance, the row does not repeat the instance key.
    await expect(details.locator(".inc-where")).not.toContainText("instance");

    await details.locator("[data-resolve]").click();
    await expect
      .poll(() => page.evaluate(() => window.__resolved))
      .toEqual([{ key: "1001", retries: 1 }]);
    await expect(page.locator("#m-inc-wrap")).toBeHidden();
    await expect(page.locator(".incident-badge")).toHaveCount(0);
    await expect(page.locator('#canvas g[data-element-id="Task_pay"]')).not.toHaveClass(/atlas-incident/);
    await expect(page.locator('#history-list .ops-hrow[data-eik="1001"]')).not.toHaveClass(/inc/);
    expect(page.__errors).toEqual([]);
  });

  test("names who set a variable by hand", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountReplay());
    await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();

    await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();
    await page.locator('#rp-tabs button[data-tab="variables"]').click();

    // The value the process computed carries no mark; the one an operator corrected
    // names them, so a jump in the values is explainable rather than mysterious.
    const rows = page.locator("#tab-variables tbody tr");
    await expect(rows.filter({ hasText: "empfaenger" }).locator(".c-actor")).toHaveText("✎ admin");
    await expect(rows.filter({ hasText: "betrag" }).locator(".c-actor")).toHaveCount(0);
    expect(page.__errors).toEqual([]);
  });

  test("selecting the stuck element shows its own incident", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountReplay());
    await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();

    // The step before it is clean: selecting it shows no incident block at all.
    await page.locator('#history-list .ops-hrow[data-eik="1000"]').click();
    await expect(page.locator("#tab-details")).toContainText("Start_1");
    await expect(page.locator("#tab-details .vp-incidents")).toHaveCount(0);

    await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();
    await expect(page.locator("#tab-details .vp-incidents .inc-row")).toHaveCount(1);
    expect(page.__errors).toEqual([]);
  });
});

// The Operations overview is the real app shell, not a harness page: it renders the
// per-process list and the variable search, both of which flag what is stuck. The
// shell talks to the Go API, which this static server does not run, so it is driven
// against a routed mock — the UI wiring is what these tests are about.
const OVERVIEW = {
  processes: [
    { key: 7, processId: "zahlung", name: "Zahlung", version: 2, deployedAt: 20, active: true },
    { key: 6, processId: "zahlung", name: "Zahlung", version: 1, deployedAt: 10, active: true },
    { key: 9, processId: "sauber", name: "Sauber", version: 1, deployedAt: 30, active: true },
  ],
  summary: [
    { processDefKey: 7, processId: "zahlung", version: 2, active: 1, completed: 4, latestCompletedAt: 0 },
    { processDefKey: 6, processId: "zahlung", version: 1, active: 2, completed: 9, latestCompletedAt: 0 },
    { processDefKey: 9, processId: "sauber", version: 1, active: 3, completed: 0, latestCompletedAt: 0 },
  ],
  // Two incidents on the OLD version and one on the latest: linking to the latest
  // would land the operator on the diagram holding the fewest of them.
  incidents: [
    { elementInstanceKey: "1001", processInstanceKey: "900001", processDefKey: 6, elementId: "Task_pay", type: "job", jobKey: "5001", raisedAt: 1, message: "boom" },
    { elementInstanceKey: "1002", processInstanceKey: "900002", processDefKey: 6, elementId: "Task_pay", type: "job", jobKey: "5002", raisedAt: 2, message: "boom" },
    { elementInstanceKey: "1003", processInstanceKey: "900003", processDefKey: 7, elementId: "Task_pay", type: "job", jobKey: "5003", raisedAt: 3, message: "boom" },
  ],
  search: [
    { key: 900001, processId: "zahlung", processDefKey: 6, version: 1, state: "active", createdAt: 1, variables: [{ name: "betrag", value: "42", kind: "number" }] },
    { key: 900009, processId: "zahlung", processDefKey: 7, version: 2, state: "active", createdAt: 2, variables: [{ name: "betrag", value: "42", kind: "number" }] },
  ],
};

const bootOverview = async (page, { truncated = false } = {}) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  // Mutable, so a resolve really removes one and both the table and the nav badge
  // (which reads the count off /stats) have to agree afterwards.
  let incidents = OVERVIEW.incidents.slice();
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (route.request().method() === "POST") {
      const m = path.match(/\/incidents\/(\w+)\/resolve$/);
      if (m) {
        incidents = incidents.filter((i) => i.elementInstanceKey !== m[1]);
        return route.fulfill({ json: { elementInstanceKey: m[1] } });
      }
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/processes") return route.fulfill({ json: OVERVIEW.processes });
    if (path === "/api/v1/instances/summary") return route.fulfill({ json: OVERVIEW.summary });
    if (path === "/api/v1/stats") {
      return route.fulfill({ json: { activeProcessInstances: 6, activeElementInstances: 6, unresolvedIncidents: incidents.length } });
    }
    if (path === "/api/v1/incidents") {
      return route.fulfill({
        json: { incidents },
        headers: truncated ? { "X-Incidents-Truncated": "true" } : {},
      });
    }
    if (path === "/api/v1/instances/search") return route.fulfill({ json: OVERVIEW.search });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null, { timeout: 15000 });
  await page.evaluate(() => { location.hash = "#/operations"; });
  await expect(page.locator("#view h1").first()).toHaveText("Instances");
};

// row returns the process row whose first cell names this process.
const row = (page, name) => page.locator("#rows tr", { has: page.locator("td", { hasText: name }) }).first();

test.describe("instances overview", () => {
  test("flags a process whose tokens are stuck, and links to the version holding them", async ({ page }) => {
    await bootOverview(page);

    // Three incidents over two versions of one process; the other process is clean.
    const flag = row(page, "Zahlung").locator("a.pill.err");
    await expect(flag).toHaveText("⚠ 3");
    // v1 holds two of the three, so that is where the link lands — not the latest (v2).
    await expect(flag).toHaveAttribute("href", "#/operations/p/6");
    await expect(flag).toHaveAttribute("title", /across 2 versions/);
    await expect(row(page, "Sauber").locator("a.pill.err")).toHaveCount(0);
    expect(page.__errors).toEqual([]);
  });

  test("a capped incident page says the counts are a lower bound", async ({ page }) => {
    await bootOverview(page, { truncated: true });
    await expect(page.locator("#ops-inc-note")).toContainText("lower bound");
    expect(page.__errors).toEqual([]);
  });

  test("a variable-search hit that is stuck says so, and opens its replay", async ({ page }) => {
    await bootOverview(page);
    await page.locator("#var-q").fill("betrag=42");
    await page.locator("#var-search button[type=submit]").click();

    const results = page.locator(".var-results tbody tr");
    await expect(results).toHaveCount(2);
    // 900001 holds an incident; 900009 does not — both are "active", which is exactly
    // why the state pill alone is not enough.
    const stuck = results.filter({ hasText: "900001" }).locator("a.pill.err");
    await expect(stuck).toHaveText("⚠ 1");
    await expect(stuck).toHaveAttribute("href", "#/operations/i/900001");
    await expect(results.filter({ hasText: "900009" }).locator("a.pill.err")).toHaveCount(0);
    expect(page.__errors).toEqual([]);
  });
});

// The nav badge is the only incident surface that finds the operator rather than
// waiting to be visited, so it has to be right without a view being opened, and it
// has to survive the chrome being rebuilt on every navigation (ADR-0151 follow-up).
const navBadge = (page) => page.locator('#topnav .nav-badge[data-badge="incidents"]');

test.describe("operations nav", () => {
  test("counts the parked tokens on the Incidents entry", async ({ page }) => {
    await bootOverview(page);

    await expect(navBadge(page)).toHaveText("3");
    await expect(navBadge(page)).toBeVisible();
    await expect(navBadge(page)).toHaveAttribute("title", /3 tokens are parked/);
    // Only that entry carries one.
    await expect(page.locator("#topnav .nav-badge")).toHaveCount(1);
    expect(page.__errors).toEqual([]);
  });

  test("survives navigating between Operations views", async ({ page }) => {
    await bootOverview(page);
    await expect(navBadge(page)).toHaveText("3");

    // setChrome rewrites the whole nav on every route, so the count has to be
    // re-applied rather than assumed to still be in the DOM.
    await page.evaluate(() => { location.hash = "#/operations/decisions"; });
    await expect(page.locator("#view h1").first()).toHaveText("Decisions");
    await expect(navBadge(page)).toHaveText("3");
    expect(page.__errors).toEqual([]);
  });

  test("drops as soon as an incident is resolved, without waiting for the poll", async ({ page }) => {
    await bootOverview(page);
    await expect(navBadge(page)).toHaveText("3");

    await page.evaluate(() => { location.hash = "#/operations/incidents"; });
    await expect(page.locator("#view h1").first()).toHaveText("Incidents");
    await page.locator("#rows button[data-resolve]").first().click();
    const modal = page.locator(".modal-ov");
    await expect(modal).toBeVisible();
    await modal.locator("[data-inc-go]").click();

    await expect(navBadge(page)).toHaveText("2");
    await expect(page.locator("#rows tr")).toHaveCount(2);
    expect(page.__errors).toEqual([]);
  });

  test("says nothing when nothing is stuck", async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/api/v1/**", async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
      if (path === "/api/v1/stats") {
        return route.fulfill({ json: { activeProcessInstances: 2, activeElementInstances: 2, unresolvedIncidents: 0 } });
      }
      if (path === "/api/v1/incidents") return route.fulfill({ json: { incidents: [] } });
      return route.fulfill({ json: [] });
    });
    await page.goto("/index.html");
    await page.evaluate(() => { location.hash = "#/operations"; });
    await expect(page.locator("#view h1").first()).toHaveText("Instances");

    await expect(navBadge(page)).toBeHidden();
    expect(errors).toEqual([]);
  });
});
