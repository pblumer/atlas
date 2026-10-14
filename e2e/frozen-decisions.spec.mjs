// The mark on a definition deployed while latest was frozen at deploy time
// (api/web/frozen-decisions.js, decision-cleanup.js's holderNote, ADR-0423).
//
// Such a definition keeps evaluating the decision version that was newest when it
// was deployed, while its binding reads "Latest — newest version when the task
// runs". The feature is the sentence that tells its owner so, and what to do about
// it; these assert the sentence.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/frozen-decisions-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const mark = (page, proc) => page.evaluate((p) => window.__mark(p), proc);

test("a definition that follows latest carries no mark", async ({ page }) => {
  // The mark is rare on purpose: shown on every definition, it would be read on none.
  for (const proc of [null, {}, { key: 1, frozenDecisions: [] }]) {
    expect(await mark(page, proc)).toBeNull();
  }
});

test("a frozen definition says what it runs, what is newest, and how to follow", async ({ page }) => {
  const m = await mark(page, {
    key: 7, processId: "orders", version: 2,
    frozenDecisions: [{ decisionId: "eligibility", key: 11, version: 1, latestKey: 33, latestVersion: 3, behind: true }],
  });
  expect(m.behind).toBe(true);
  expect(m.label).toBe("Decision frozen · newer deployed");
  expect(m.title).toContain("eligibility runs v1, the newest is v3");
  expect(m.title).toContain("Deploy the process again to have it follow each new decision version");
  // What a redeploy does not do, said before somebody expects it to.
  expect(m.title).toContain("instances already running stay on this definition unless they are migrated");
});

test("a frozen definition that still runs the newest version is marked, not warned", async ({ page }) => {
  const m = await mark(page, {
    key: 7,
    frozenDecisions: [{ decisionId: "eligibility", key: 11, version: 1, latestKey: 11, latestVersion: 1, behind: false }],
  });
  expect(m.behind).toBe(false);
  expect(m.label).toBe("Decision frozen");
  expect(m.title).toContain("eligibility runs v1, which is still the newest");
});

test("a definition frozen on its bundled model says so rather than naming a version it has not", async ({ page }) => {
  const m = await mark(page, {
    key: 7,
    frozenDecisions: [{ decisionId: "eligibility", key: 7, latestKey: 33, latestVersion: 1, behind: true }],
  });
  expect(m.title).toContain("eligibility runs the model deployed with the process, the newest is v1");
});

test("after a deploy, the newest version of each frozen process is named, and only the ones behind", async ({ page }) => {
  const frozen = (decisionId, behind) => ({ decisionId, key: 11, version: 1, latestKey: 33, latestVersion: 2, behind });
  const procs = [
    // Two versions of orders: only the newest counts — it is the one new instances
    // start on and the one deploying again replaces.
    { key: 5, processId: "orders", name: "Orders", version: 1, frozenDecisions: [frozen("eligibility", true)] },
    { key: 9, processId: "orders", name: "Orders", version: 2, frozenDecisions: [frozen("eligibility", true)] },
    // Redeployed since: its newest version follows latest, so the older one is not named.
    { key: 6, processId: "billing", version: 1, frozenDecisions: [frozen("eligibility", true)] },
    { key: 10, processId: "billing", version: 2 },
    // Frozen, but on another decision than the one deployed.
    { key: 12, processId: "pricing", version: 1, frozenDecisions: [frozen("discount", true)] },
    // Frozen on this decision and still on the newest: nothing it will not follow yet.
    { key: 13, processId: "audit", version: 1, frozenDecisions: [frozen("eligibility", false)] },
  ];
  const held = await page.evaluate(([p, ids]) => window.__behindOn(p, ids), [procs, ["eligibility"]]);
  expect(held.map((h) => [h.processId, h.version])).toEqual([["orders", 2]]);

  const notice = await page.evaluate((h) => window.__notice(h), held);
  expect(notice).toBe(
    "One deployed process does not follow this version: Orders v2 (eligibility runs v1, the newest is v2). " +
    "It was deployed while latest was frozen at deploy time — deploy it again to have it follow each new version.");
});

test("a deploy every process follows says nothing", async ({ page }) => {
  expect(await page.evaluate(() => window.__notice([]))).toBe("");
});

test("a version's holder says whether it froze the version or chose it", async ({ page }) => {
  const frozen = await page.evaluate(() => window.__holder({ key: 9, processId: "orders", version: 2, binding: "latest" }));
  expect(frozen.label).toBe("frozen");
  expect(frozen.title).toContain("orders v2 froze latest on this version when it was deployed");
  // Deploying again helps new instances; it does not free this version.
  expect(frozen.title).toContain("this definition holds the version until it is undeployed");

  const fixed = await page.evaluate(() => window.__holder({ key: 10, processId: "billing", version: 1, binding: "version" }));
  expect(fixed.label).toBe("fixed");
  expect(fixed.title).toContain("billing v1 names this version");

  // A server that does not say which gets the sentence it always had.
  const plain = await page.evaluate(() => window.__holder({ key: 9, processId: "orders", version: 2 }));
  expect(plain).toEqual({ label: "", title: "orders v2 pinned this version at deploy time" });
});

test("the delete refusal names how each holder holds the version", async ({ page }) => {
  const rows = [
    { key: 1, version: 1, pinnedBy: [
      { key: 9, processId: "orders", version: 2, binding: "latest" },
      { key: 10, processId: "billing", version: 1, binding: "version" },
    ] },
    { key: 2, version: 2, current: true },
  ];
  const got = await page.evaluate(([r, all]) => window.__versionState(r, all), [rows[0], rows]);
  expect(got.deletable).toBe(false);
  expect(got.why).toContain("Held by orders (frozen), billing (fixed)");
});
