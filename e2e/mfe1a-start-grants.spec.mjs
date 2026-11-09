// MFE1A / ADR-0516: an administrator manages start grants on the Console page, and
// sees a read-only mark in an application's Deployments tab. The server is mocked
// here with revisions, conflicts and blockers as the real one answers them; what it
// allows is held by the Go tests (api/startgrants_http_test.go). This spec holds what
// the administrator sees: names, never ids or revisions to type; offered or why not;
// a lost race that changes nothing; and nothing at all for anybody who is not an
// administrator.
import { test, expect } from "@playwright/test";

const APP = "app-field";
const USERS = [
  { id: "usr_anna", username: "anna", displayName: "Anna Muster" },
  { id: "usr_ben", username: "ben", displayName: "Ben Beispiel" },
  { id: "usr_root", username: "root", displayName: "Root Admin" },
];
const GROUPS = [{ id: "grp_insp", name: "Field inspectors" }];
const DEFINITIONS = [
  { key: 41, processId: "safety-inspection", name: "Safety inspection", version: 3, current: true, active: true, running: 0, finished: 0, deployedAt: 1792000000 },
  { key: 40, processId: "safety-inspection", name: "Safety inspection", version: 2, current: false, active: true, running: 0, finished: 0, deployedAt: 1791000000 },
  { key: 42, processId: "site-visit", name: "Site visit", version: 1, current: true, active: false, running: 0, finished: 0, deployedAt: 1792000001 },
];

// world is the mocked server's grant store: per process the current grant, its
// revision and its history, and the blockers the server would answer.
function world() {
  return {
    grants: {
      "site-visit": { revision: 1, granted: true, userIds: [], groupIds: ["grp_insp"], changedAt: 1792000100, changedBy: "usr_root" },
      "old-process": { revision: 2, granted: true, userIds: ["usr_gone"], groupIds: [], changedAt: 1792000200, changedBy: "usr_root" },
    },
    history: {
      "site-visit": [{ revision: 1, at: 1792000100, actorId: "usr_root", action: "grant", userIds: [], groupIds: ["grp_insp"] }],
    },
    blockers: { "safety-inspection": [], "site-visit": ["inactive"], "old-process": ["not-deployed"] },
    puts: [], deletes: [], grantCalls: 0, conflictNext: false, uncertain: false,
  };
}

function view(w, processId) {
  const g = w.grants[processId] || { revision: 0, granted: false, userIds: [], groupIds: [] };
  return { applicationId: APP, processId, ...g, blockers: w.blockers[processId] || [] };
}

function mock(page, w, { roles = ["admin"], authEnabled = true } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: {
      authEnabled, user: authEnabled ? { id: "usr_root", username: "root", roles } : null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [
      { id: APP, name: "Field Operations" }, { id: "app-sys", name: "System", protected: true }] });
    if (path === "/api/v1/users") return route.fulfill({ json: USERS });
    if (path === "/api/v1/groups") return route.fulfill({ json: GROUPS });
    if (path === `/api/v1/applications/${APP}/deployments`) return route.fulfill({ json: {
      name: "Field Operations", version: 1, processes: 2, running: 0, finished: 0, definitions: DEFINITIONS } });
    if (path.startsWith(`/api/v1/applications/${APP}/start-grants`)) {
      w.grantCalls++;
      if (!roles.includes("admin")) return route.fulfill({ status: 403, json: { error: "the admin role is required" } });
      if (w.uncertain) return route.fulfill({ status: 503, json: { error: "uncertain" } });
      const rest = path.slice(`/api/v1/applications/${APP}/start-grants`.length).split("/").filter(Boolean);
      if (!rest.length) return route.fulfill({ json: { items: Object.keys(w.grants).sort().map((p) => view(w, p)) } });
      const pid = decodeURIComponent(rest[0]);
      if (rest[1] === "history") {
        const items = (w.history[pid] || []).slice().reverse();
        return route.fulfill({ json: { items, total: items.length } });
      }
      if (rest[1] === "preview") {
        const g = w.grants[pid];
        const userId = url.searchParams.get("userId");
        const matched = g && g.granted ? [
          ...(g.userIds.includes(userId) ? ["user"] : []),
          ...(userId === "usr_anna" && g.groupIds.includes("grp_insp") ? ["group:grp_insp"] : []),
        ] : [];
        const blockers = w.blockers[pid] || [];
        return route.fulfill({ json: { granted: matched.length > 0, matchedBy: matched, accountIssues: [],
          blockers, startable: !blockers.length, wouldBeOffered: matched.length > 0 && !blockers.length } });
      }
      if (req.method() === "GET") return route.fulfill({ json: view(w, pid) });
      const cur = w.grants[pid] || { revision: 0 };
      if (req.method() === "PUT") {
        const body = req.postDataJSON();
        w.puts.push({ pid, body });
        if (w.conflictNext || body.expectedRevision !== cur.revision) {
          w.conflictNext = false;
          w.grants[pid] = { revision: cur.revision + 1, granted: true, userIds: ["usr_ben"], groupIds: [], changedAt: 1792000300, changedBy: "usr_root" };
          return route.fulfill({ status: 409, json: { error: "stale", currentRevision: cur.revision + 1 } });
        }
        w.grants[pid] = { revision: cur.revision + 1, granted: true, userIds: body.userIds, groupIds: body.groupIds, changedAt: 1792000400, changedBy: "usr_root" };
        (w.history[pid] ||= []).push({ revision: cur.revision + 1, at: 1792000400, actorId: "usr_root", action: "grant", userIds: body.userIds, groupIds: body.groupIds });
        return route.fulfill({ json: { ...view(w, pid), changed: true } });
      }
      if (req.method() === "DELETE") {
        w.deletes.push({ pid, expected: url.searchParams.get("expectedRevision") });
        w.grants[pid] = { revision: cur.revision + 1, granted: false, userIds: [], groupIds: [], changedAt: 1792000500, changedBy: "usr_root" };
        (w.history[pid] ||= []).push({ revision: cur.revision + 1, at: 1792000500, actorId: "usr_root", action: "revoke", userIds: [], groupIds: [] });
        return route.fulfill({ json: { ...view(w, pid), changed: true } });
      }
    }
    return route.fulfill({ json: [] });
  });
}

const row = (page, pid) => page.locator(`#sg-table tr[data-process="${pid}"]`);

async function pick(page, panel, input, text, option) {
  await page.locator(`${panel} #${input}`).fill(text);
  await page.locator(`${panel} #${input}-matches button`, { hasText: option }).click();
}

test("MFE1A: an administrator grants by name, without typing an id or a revision", async ({ page }) => {
  const w = world();
  mock(page, w);
  await page.goto("/index.html?lang=en#/console/start-grants");
  await expect(row(page, "safety-inspection")).toContainText("Not granted", { timeout: 15000 });
  // The menus offer the page to an administrator (top bar and phone menu alike).
  expect(await page.locator('a[href="#/console/start-grants"]').count()).toBeGreaterThan(0);
  // Superseded versions are not rows of their own; a grant whose process left is.
  await expect(page.locator("#sg-table tbody tr")).toHaveCount(3);
  await expect(row(page, "old-process")).toContainText("no longer deployed in this application");
  await expect(row(page, "old-process")).toContainText("removed account (usr_gone)");
  // It can still be revoked, but not granted anew: there is nothing here to start.
  await expect(row(page, "old-process").getByRole("button", { name: "Revoke" })).toBeVisible();
  await expect(row(page, "old-process").getByRole("button", { name: /^(Grant|Edit)/ })).toHaveCount(0);
  // Granted but not offered says why, in words.
  await expect(row(page, "site-visit")).toContainText("Field inspectors");
  await expect(row(page, "site-visit")).toContainText("Not offered");
  await expect(row(page, "site-visit")).toContainText("The process is deactivated.");

  await row(page, "safety-inspection").getByRole("button", { name: "Grant" }).click();
  await expect(page.locator("#sg-editor")).toContainText("revision 0");
  await pick(page, "#sg-editor", "sg-find", "insp", "Field inspectors");
  await pick(page, "#sg-editor", "sg-find", "anna", "Anna Muster");
  await page.locator("#sg-save").click();
  await expect(page.locator("#sg-status")).toContainText("The grant is saved.");
  expect(w.puts).toEqual([{ pid: "safety-inspection", body: { userIds: ["usr_anna"], groupIds: ["grp_insp"], expectedRevision: 0 } }]);
  await expect(row(page, "safety-inspection")).toContainText("Field inspectors, Anna Muster");
  await expect(row(page, "safety-inspection")).toContainText("Offered");
  await expect(row(page, "safety-inspection")).toContainText("Root Admin");
});

test("MFE1A: a competing change is shown and nothing is sent twice", async ({ page }) => {
  const w = world();
  w.conflictNext = true;
  mock(page, w);
  await page.goto("/index.html?lang=en#/console/start-grants");
  await row(page, "safety-inspection").getByRole("button", { name: "Grant" }).click();
  await pick(page, "#sg-editor", "sg-find", "anna", "Anna Muster");
  await page.locator("#sg-save").click();
  await expect(page.locator("#sg-status")).toContainText("Somebody changed this grant meanwhile");
  expect(w.puts).toHaveLength(1);
  // The table shows what the other administrator decided.
  await expect(row(page, "safety-inspection")).toContainText("Ben Beispiel");
});

test("MFE1A: revoking asks first, sends the revision read, and keeps the history", async ({ page }) => {
  const w = world();
  mock(page, w);
  await page.goto("/index.html?lang=en#/console/start-grants");
  let asked = "";
  page.once("dialog", (d) => { asked = d.message(); d.accept(); });
  await row(page, "site-visit").getByRole("button", { name: "Revoke" }).click();
  await expect(page.locator("#sg-status")).toContainText("The grant is revoked.");
  expect(asked).toContain("Site visit");
  expect(asked).toContain("Field inspectors");
  expect(w.deletes).toEqual([{ pid: "site-visit", expected: "1" }]);
  await expect(row(page, "site-visit")).toContainText("Not granted");
  await row(page, "site-visit").getByRole("button", { name: "History" }).click();
  await expect(page.locator("#sg-history li").first()).toContainText("Root Admin revoked the grant.");
});

test("MFE1A: a declined confirmation revokes nothing", async ({ page }) => {
  const w = world();
  mock(page, w);
  await page.goto("/index.html?lang=en#/console/start-grants");
  page.once("dialog", (d) => d.dismiss());
  await row(page, "site-visit").getByRole("button", { name: "Revoke" }).click();
  await expect(row(page, "site-visit")).toContainText("Field inspectors");
  expect(w.deletes).toHaveLength(0);
});

test("MFE1A: checking a person says whether and through what they are offered it", async ({ page }) => {
  const w = world();
  w.grants["safety-inspection"] = { revision: 1, granted: true, userIds: [], groupIds: ["grp_insp"], changedAt: 1792000100, changedBy: "usr_root" };
  mock(page, w);
  await page.goto("/index.html?lang=en#/console/start-grants");
  await row(page, "safety-inspection").getByRole("button", { name: "Check" }).click();
  await pick(page, "#sg-check", "sg-check-find", "anna", "Anna Muster");
  await expect(page.locator("#sg-check-result")).toContainText("Anna Muster is offered the process.");
  await expect(page.locator("#sg-check-result")).toContainText("group Field inspectors");
  await pick(page, "#sg-check", "sg-check-find", "ben", "Ben Beispiel");
  await expect(page.locator("#sg-check-result")).toContainText("Ben Beispiel is not offered the process.");
  await expect(page.locator("#sg-check-result")).toContainText("No grant names this account or group.");
});

test("MFE1A: untrusted grants after a failed save are said, and changes are refused", async ({ page }) => {
  const w = world();
  w.uncertain = true;
  mock(page, w);
  await page.goto("/index.html?lang=en#/console/start-grants");
  await expect(page.locator("#sg-status")).toContainText("untrusted after a failed save", { timeout: 15000 });
});

test("MFE1A: nobody but an administrator sees the page, and it asks the server nothing", async ({ page }) => {
  const w = world();
  mock(page, w, { roles: ["modeler", "operator", "user"] });
  await page.goto("/index.html?lang=en#/console");
  await expect(page.locator("#topnav")).toBeVisible({ timeout: 15000 });
  await expect(page.locator('a[href="#/console/start-grants"]')).toHaveCount(0);
  await page.goto("/index.html?lang=en#/console/start-grants");
  await expect(page.locator("#sg-denied")).toContainText("managed by an administrator");
  expect(w.grantCalls).toBe(0);
});

test("MFE1A: a server without sign-in has no start grants", async ({ page }) => {
  const w = world();
  mock(page, w, { authEnabled: false });
  await page.goto("/index.html?lang=en#/console/start-grants");
  await expect(page.locator("#sg-denied")).toContainText("only on a server with sign-in", { timeout: 15000 });
  expect(w.grantCalls).toBe(0);
});

test("MFE1A 768px: the page fits a tablet without sideways scrolling", async ({ page }) => {
  const w = world();
  mock(page, w);
  await page.setViewportSize({ width: 768, height: 1024 });
  await page.goto("/index.html?lang=en#/console/start-grants");
  await expect(row(page, "site-visit")).toBeVisible({ timeout: 15000 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
  await expect(page.locator("#sg-app")).toBeVisible();
});

test("MFE1A: the Deployments tab marks a granted process for an administrator, read-only", async ({ page }) => {
  const w = world();
  mock(page, w);
  await page.goto("/index.html?lang=en#/modeler/p/" + APP);
  await page.locator('#pd-tabs button[data-pane="deployments"]').click();
  const mark = page.locator('#pd-deployments tr[data-process="site-visit"] a.grant-hint');
  await expect(mark).toBeVisible({ timeout: 15000 });
  await expect(mark).toHaveText("Start grant · groups 1 · people 0");
  await expect(mark).toHaveAttribute("href", `#/console/start-grants?app=${APP}&process=site-visit`);
  await expect(page.locator('#pd-deployments tr[data-process="safety-inspection"] a.grant-hint')).toHaveCount(0);
  // The mark is a link to the page and nothing else: no control to change a grant here.
  await expect(page.locator("#pd-deployments [data-act=revoke], #pd-deployments #sg-save")).toHaveCount(0);
  await mark.click();
  await expect(row(page, "site-visit")).toHaveClass(/selected/, { timeout: 15000 });
});

test("MFE1A: an owner who is not an administrator sees no mark and asks for no grant", async ({ page }) => {
  const w = world();
  mock(page, w, { roles: ["modeler", "user"] });
  await page.goto("/index.html?lang=en#/modeler/p/" + APP);
  await page.locator('#pd-tabs button[data-pane="deployments"]').click();
  await expect(page.locator('#pd-deployments tr[data-process="site-visit"]')).toBeVisible({ timeout: 15000 });
  await expect(page.locator("#pd-deployments a.grant-hint")).toHaveCount(0);
  expect(w.grantCalls).toBe(0);
});
