// e2e for the Console's two identity pages (api/web/connections.js and
// api/web/security.js, ADR-0475), against a fixture.
//
// What has to hold on a screen, beyond what the Go tests prove about the routes:
//
//   - A person reaches their connections from the account menu, is offered the link
//     only when the server can link, chooses which Workers may act as them, and is
//     asked before a revocation that cannot be undone.
//   - An administrator reaches Security from Administration and sees shadow called
//     what it is — not enforcement — wherever it appears.
//   - A simulation says it is one, and an "approved" field in it changes nothing.
//   - A policy draft the server refuses shows the server's reason.
//   - Nothing on either page puts anything into storage.
//
// It loads the REAL Console with only the network replaced, as
// console-api-tokens.spec.mjs does.
import { test, expect } from "@playwright/test";

const NS = 1e6; // the server's timestamps are Unix nanoseconds
const NOW_NS = Date.now() * NS;

const ADMIN = { id: "usr_root", username: "root", displayName: "Root", roles: ["admin"] };
const ALICE = { id: "usr_alice", username: "alice", displayName: "Alice", roles: ["user"] };

const MINE = {
  identities: [{ id: "xid_1", userId: "usr_alice", provider: "microsoft", issuer: "https://login.microsoftonline.com/tid/v2.0",
    tenantId: "tid", subject: "oid-alice", username: "alice@contoso.example", displayName: "Alice Example",
    linkedAt: NOW_NS, lastAuthenticatedAt: NOW_NS }],
  connections: [{ id: "con_1", ownerId: "usr_alice", provider: "microsoft", identityId: "xid_1",
    identityKey: "microsoft:tid:oid-alice", authMethod: "authorization_code_pkce", tenantId: "tid",
    scopes: ["https://graph.microsoft.com/Sites.ReadWrite.All", "offline_access"], allowedWorkers: [],
    status: "active", createdAt: NOW_NS, updatedAt: NOW_NS, lastInteractiveAuthAt: NOW_NS }],
  linkable: [{ provider: "microsoft", start: "/auth/link/microsoft/start" }],
};

const WORKERS = [
  { id: "cw_sp", name: "sp-prod", kind: "sharepoint", enabled: true },
  { id: "cw_mail", name: "mail", kind: "smtp", enabled: true },
];

const READINESS = {
  workers: [
    { id: "cw_sp", worker: "sp-prod", kind: "sharepoint", governed: true, governable: true, mode: "service",
      enforcement: "shadow", credential: "held by the Worker Instance (reference sp)", credentialKind: "worker", credentialRef: "sp" },
    { id: "cw_mail", worker: "mail", kind: "smtp", governed: false, governable: false, mode: "legacy",
      enforcement: "off", credential: "none configured", credentialKind: "none" },
  ],
};

function installFixture({ me, mine, workers, readiness, nowNs }) {
  window.__requests = [];
  const state = { mine: JSON.parse(JSON.stringify(mine)), policies: [] };
  const json = (body, status) => Promise.resolve(new Response(JSON.stringify(body), {
    status: status || 200, headers: { "content-type": "application/json" },
  }));
  window.fetch = (input, init) => {
    const url = typeof input === "string" ? input : input.url;
    const path = url.replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    const method = ((init && init.method) || "GET").toUpperCase();
    const body = init && init.body ? JSON.parse(init.body) : undefined;
    const isAdmin = me.roles.includes("admin");
    if (method !== "GET") window.__requests.push({ method, path, body });
    if (path === "/api/v1/auth/me") return json({ authEnabled: true, user: me });
    if (path === "/api/v1/me/connections") return json(state.mine);
    if (path === "/api/v1/configured-workers") return json(workers);
    let m = path.match(/^\/api\/v1\/connections\/([^/]+)\/(workers|revoke)$/);
    if (m) {
      const c = state.mine.connections.find((x) => x.id === decodeURIComponent(m[1]));
      if (m[2] === "workers") c.allowedWorkers = body.workers;
      else Object.assign(c, { status: "revoked", revokedAt: nowNs, revokeReason: body.reason, allowedWorkers: [] });
      return json(c);
    }
    if (path.startsWith("/api/v1/security/") && !isAdmin) return json({ error: "the admin role is required" }, 403);
    if (path === "/api/v1/security/readiness") return json(readiness);
    if (path === "/api/v1/security/policies" && method === "GET") return json(state.policies);
    if (path === "/api/v1/security/policies/simulate") {
      // The server's own answer to a request with no approval: an obligation open,
      // and the extra field reported as ignored.
      const ignored = Object.keys(body).filter((k) => k === "approved");
      return json({ simulation: true, note: "A simulation", effect: "Deny", permitted: false,
        reasonCode: "APPROVAL_REQUIRED", policyId: "hr-documents", policyVersion: 1,
        obligations: [{ id: "manager", type: "approval", policyId: "hr-documents", policyVersion: 1,
          satisfied: false, reason: "APPROVAL_REQUIRED", approverGroup: "grp_managers", approvals: 1 }],
        trace: [{ policyId: "hr-documents", policyVersion: 1, ruleId: "members", effect: "Permit", reasonCode: "MEMBER_DELETE_APPROVED" }],
        digest: "abc", ignored, ignoredBecause: ignored.length ? "not attributes" : "", subjectResolved: false });
    }
    m = path.match(/^\/api\/v1\/security\/policies\/([^/]+)\/draft$/);
    if (m) {
      if (!Array.isArray(body.rules) || !body.rules.length) return json({ error: "policy: a policy needs at least one rule" }, 400);
      const p = { id: body.id, current: 0, enabled: false, draft: body, draftWarnings: [], updatedAt: nowNs, updatedBy: me.id };
      state.policies = state.policies.filter((x) => x.id !== p.id).concat([p]);
      return json(p);
    }
    return json([]);
  };
}

async function boot(page, me, hash = "") {
  page._errors = [];
  page.on("pageerror", (e) => page._errors.push(e.message));
  await page.addInitScript(installFixture, { me, mine: MINE, workers: WORKERS, readiness: READINESS, nowNs: NOW_NS });
  await page.goto("/index.html" + hash);
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
}

const requests = (page) => page.evaluate(() => window.__requests);

const storageKeys = (page) => page.evaluate(() => {
  const out = [];
  for (const s of [localStorage, sessionStorage]) for (let i = 0; i < s.length; i++) out.push(s.key(i));
  return out.filter((k) => /connection|token|credential|policy|security/i.test(k));
});

test("a person reaches their connections from the account menu and allows a Worker", async ({ page }) => {
  await boot(page, ALICE);
  await page.locator(".topbar .avatar").click();
  await page.locator("a", { hasText: "Connected accounts" }).click();
  await expect(page).toHaveURL(/#\/console\/connections$/);
  await expect(page.locator("#view h1")).toHaveText(/Verbundene Konten|Connected accounts/);
  // The server can link, so the page offers it, at the server's own start path.
  await expect(page.locator("a[data-link='microsoft']")).toHaveAttribute("href", "/auth/link/microsoft/start");
  // A new connection allows nothing; only SharePoint Workers are offered.
  const card = page.locator("[data-connection='con_1']");
  await expect(card.locator("input[name=worker]")).toHaveCount(1);
  await expect(card.locator("input[value='sp-prod']")).not.toBeChecked();
  await card.locator("input[value='sp-prod']").check();
  await card.locator("button[type=submit]").click();
  await expect(page.locator("[data-connection='con_1'] input[value='sp-prod']")).toBeChecked();
  const put = (await requests(page)).find((r) => r.method === "PUT");
  expect(put).toEqual({ method: "PUT", path: "/api/v1/connections/con_1/workers", body: { workers: ["sp-prod"] } });
  expect(await storageKeys(page)).toEqual([]);
  expect(page._errors).toEqual([]);
});

test("revoking asks first, and a cancelled confirmation sends nothing", async ({ page }) => {
  await boot(page, ALICE, "#/console/connections");
  const card = page.locator("[data-connection='con_1']");
  page.once("dialog", (d) => d.dismiss());
  await card.locator("button[data-act='revoke']").click();
  expect((await requests(page)).filter((r) => r.path.endsWith("/revoke"))).toEqual([]);
  page.once("dialog", (d) => d.accept());
  await card.locator("button[data-act='revoke']").click();
  await expect(page.locator("[data-connection='con_1'] [data-status='revoked']")).toBeVisible();
  await expect(page.locator("[data-connection='con_1'] form")).toHaveCount(0);
});

test("the landing page after linking says what to do next", async ({ page }) => {
  await boot(page, ALICE, "#/console/connections?linked=con_1");
  await expect(page.locator("#con-banner[data-banner='linked']")).toBeVisible();
  await page.evaluate(() => { location.hash = "#/console/connections?link=failed"; });
  await expect(page.locator("#con-banner[data-banner='failed']")).toBeVisible();
});

test("an administrator reaches Security, and shadow is never called enforcement", async ({ page }) => {
  await boot(page, ADMIN);
  await page.locator("#topnav .topnav-group-btn", { hasText: "Administration" }).click();
  await page.locator("#topnav .topnav-group a", { hasText: "Security" }).click();
  await expect(page).toHaveURL(/#\/console\/security$/);
  await expect(page.locator("#sec-shadow-warning")).toBeVisible();
  const sp = page.locator("tr[data-worker='cw_sp']");
  await expect(sp.locator("[data-enforcement='shadow']")).toBeVisible();
  await expect(sp.locator("[data-note]")).toContainText(/keine Durchsetzung|not enforcement/);
  // A Worker Type this build cannot govern offers no setting.
  await expect(page.locator("tr[data-worker='cw_mail'] button[data-act='identity']")).toHaveCount(0);
  expect(page._errors).toEqual([]);
});

test("a simulation says it is one, and a claimed approval is reported as ignored", async ({ page }) => {
  await boot(page, ADMIN, "#/console/security");
  const f = page.locator("#sim-form");
  await f.locator("input[name=subject]").fill("usr_alice");
  await f.locator("input[name=worker]").fill("sp-prod");
  await f.locator("input[name=operation]").fill("delete-file");
  await f.locator("input[name=resource]").fill("sharepoint:site=contoso.sharepoint.com:/sites/hr;list=Documents;path=/a.docx");
  // The page never sends "approved"; a request that did would be told it was ignored.
  await page.evaluate(() => {
    const orig = window.fetch;
    window.fetch = (input, init) => {
      if (String(input).includes("/simulate")) {
        const b = JSON.parse(init.body); b.approved = true; init = Object.assign({}, init, { body: JSON.stringify(b) });
      }
      return orig(input, init);
    };
  });
  await f.locator("button[type=submit]").click();
  const answer = page.locator("#sim-answer");
  await expect(answer).toHaveAttribute("data-simulation", "true");
  await expect(answer.locator("#sim-label")).toContainText(/Simulation/);
  await expect(answer.locator("[data-effect='Deny']").first()).toBeVisible();
  await expect(answer.locator("[data-reason]")).toHaveText("APPROVAL_REQUIRED");
  await expect(answer.locator("[data-ignored]")).toContainText("approved");
  const sim = (await requests(page)).find((r) => r.path.endsWith("/simulate"));
  expect(sim.body.connection.status).toBe("active");
  expect(sim.body.connection.allowedWorkers).toEqual(["sp-prod"]);
});

test("a draft the server refuses shows the server's reason", async ({ page }) => {
  await boot(page, ADMIN, "#/console/security");
  await page.locator("#sec-policy-new").click();
  const ed = page.locator("#sec-editor-form");
  await ed.locator("input[name=id]").fill("empty");
  await ed.locator("textarea[name=doc]").fill(JSON.stringify({ id: "empty", target: {}, ruleCombining: "first-applicable", rules: [] }));
  await ed.locator("button[type=submit]").click();
  await expect(page.locator("#sec-editor-notes [data-refused]")).toContainText("at least one rule");
  // The skeleton the page offers is one the server accepts.
  await page.locator("#sec-editor button[data-act='close']").click();
  await page.locator("#sec-policy-new").click();
  await page.locator("#sec-editor-form input[name=id]").fill("starter");
  await page.locator("#sec-editor-form button[type=submit]").click();
  await expect(page.locator("#sec-editor-notes [data-saved]")).toBeVisible();
  await expect(page.locator("tr[data-policy='starter'] [data-state='draft']")).toBeVisible();
  expect(await storageKeys(page)).toEqual([]);
});

test("Security is not offered to a person without the admin role", async ({ page }) => {
  await boot(page, ALICE, "#/console/security");
  await expect(page.locator("#sec-denied")).toBeVisible();
});
