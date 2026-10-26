// e2e for the Console's approvals page (api/web/approvals.js, ADR-0475 §9), against a
// fixture.
//
// What has to hold on a screen, beyond what the Go tests prove about the routes:
//
//   - An approver reaches the page from the account menu, and from the dashboard when
//     something waits for them.
//   - Approving and rejecting ask first, naming the exact action; a cancelled
//     confirmation sends nothing. The answer and its reason go to the decide route,
//     and the page then shows what the server recorded, not what it hoped for.
//   - An answer can be withdrawn while the approval is open.
//   - The person an action would run for sees it, and is offered no answer.
//   - A refused answer shows the server's reason.
//   - A worklist that is a page says so.
//   - Nothing on the page goes into storage.
//
// It loads the REAL Console with only the network replaced, as
// console-identity.spec.mjs does.
import { test, expect } from "@playwright/test";

const NS = 1e6; // the server's timestamps are Unix nanoseconds
const NOW_NS = Date.now() * NS;
const HOUR_NS = 3600 * 1e9;

const BOB = { id: "usr_bob", username: "bob", displayName: "Bob", roles: ["user"] };
const ALICE = { id: "usr_alice", username: "alice", displayName: "Alice", roles: ["user"] };

const RESOURCE = "sharepoint:site=contoso.sharepoint.com:/sites/hr/Drafts/old.docx";

// approval is one open request as GET /api/v1/me/action-approvals returns it.
const approval = (over) => Object.assign({
  processInstanceKey: 2251799813685300, approvalKey: 2251799813685311, jobKey: 2251799813685305,
  decisionKey: 2251799813685310, requestedAt: NOW_NS - HOUR_NS, expiresAt: NOW_NS + 23 * HOUR_NS,
  requestedBy: "usr_alice", requestedByName: "Alice", approverGroup: "grp_managers", approverGroupName: "managers",
  approvals: 1, policyId: "hr-documents", policyVersion: 1, obligationId: "manager", worker: "sp-prod",
  operation: "delete", resource: RESOURCE, actionDigest: "9f2c4e1a7b3d5f60aa", answers: [], open: true,
  processId: "dokument-loeschen", role: "approver", granted: 0, rejected: false,
}, over);

function installFixture({ me, items, truncated, refuse, nowNs }) {
  window.__requests = [];
  const state = { items: JSON.parse(JSON.stringify(items)) };
  const json = (body, status) => Promise.resolve(new Response(JSON.stringify(body), {
    status: status || 200, headers: { "content-type": "application/json" },
  }));
  window.fetch = (input, init) => {
    const url = typeof input === "string" ? input : input.url;
    const path = url.replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    const method = ((init && init.method) || "GET").toUpperCase();
    const body = init && init.body ? JSON.parse(init.body) : undefined;
    // Only the answers are recorded: the shell's own writes (presence) are not this page's.
    if (method !== "GET" && path.includes("/action-approvals/")) window.__requests.push({ method, path, body });
    if (path === "/api/v1/auth/me") return json({ authEnabled: true, user: me });
    if (path === "/api/v1/me/action-approvals") {
      window.__approvalReads = (window.__approvalReads || 0) + 1;
      return json({ items: state.items, total: state.items.length, totalExact: !truncated, truncated: !!truncated });
    }
    const m = path.match(/^\/api\/v1\/instances\/(\d+)\/action-approvals\/(\d+)\/decide$/);
    if (m) {
      if (refuse) return json({ error: refuse }, 409);
      // What the server records: the answer joins the list, and the standing answer
      // and the counts follow from it.
      const a = state.items.find((x) => String(x.approvalKey) === m[2]);
      const kind = body.outcome === "withdrawn" ? "revoked" : "decided";
      a.answers.push({ kind, principal: me.id, name: me.displayName, outcome: kind === "decided" ? body.outcome : "",
        at: nowNs, reason: body.reason || "" });
      if (kind === "revoked") {
        if (a.myAnswer && a.myAnswer.outcome === "granted") a.granted--;
        if (a.myAnswer && a.myAnswer.outcome === "rejected") a.rejected = false;
        delete a.myAnswer;
      } else {
        a.myAnswer = a.answers[a.answers.length - 1];
        if (body.outcome === "granted") a.granted++;
        else a.rejected = true;
      }
      return json({ approvalKey: a.approvalKey, outcome: body.outcome });
    }
    return json([]);
  };
}

async function boot(page, me, { items = [approval()], truncated = false, refuse = "", hash = "" } = {}) {
  page._errors = [];
  page.on("pageerror", (e) => page._errors.push(e.message));
  await page.addInitScript(installFixture, { me, items, truncated, refuse, nowNs: NOW_NS });
  await page.goto("/index.html" + hash);
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
}

const requests = (page) => page.evaluate(() => window.__requests);

const storageKeys = (page) => page.evaluate(() => {
  const out = [];
  for (const s of [localStorage, sessionStorage]) for (let i = 0; i < s.length; i++) out.push(s.key(i));
  return out.filter((k) => /approv/i.test(k));
});

test("an approver reaches the page from the account menu and approves the exact action", async ({ page }) => {
  await boot(page, BOB);
  await page.locator(".topbar .avatar").click();
  await page.locator("a", { hasText: "Approvals" }).click();
  await expect(page).toHaveURL(/#\/console\/approvals$/);
  await expect(page.locator("#view h1")).toHaveText(/Genehmigungen|Approvals/);

  const card = page.locator("#apr-waiting [data-approval]");
  await expect(card).toHaveCount(1);
  await expect(card.locator("[data-operation]")).toHaveText("delete");
  await expect(card.locator("[data-resource]")).toHaveText(RESOURCE);
  await expect(card).toContainText("Alice");
  await expect(card.locator("[data-standing='open']")).toBeVisible();
  // Not an operator: the instance is named, not linked.
  await expect(card.locator("[data-instance-link]")).toHaveCount(0);

  // A cancelled confirmation sends nothing.
  page.once("dialog", (d) => d.dismiss());
  await card.locator("button[data-act='granted']").click();
  expect(await requests(page)).toEqual([]);

  // The confirmation names the action; accepting it sends the answer and its reason.
  let asked = "";
  page.once("dialog", (d) => { asked = d.message(); d.accept(); });
  await card.locator("textarea[name=reason]").fill("checked with HR");
  await card.locator("button[data-act='granted']").click();
  await expect(page.locator("#apr-answered [data-approval]")).toHaveCount(1);
  expect(asked).toContain(RESOURCE);
  expect(asked).toContain("delete");
  expect(await requests(page)).toEqual([{ method: "POST",
    path: "/api/v1/instances/2251799813685300/action-approvals/2251799813685311/decide",
    body: { outcome: "granted", reason: "checked with HR" } }]);

  // The page shows what the server recorded: the answer is Bob's, it counts, and
  // nothing waits any more.
  const answered = page.locator("#apr-answered [data-approval]");
  await expect(answered.locator("[data-mine='granted']")).toBeVisible();
  await expect(answered.locator("[data-standing='granted']")).toBeVisible();
  await expect(page.locator("#apr-waiting [data-approval]")).toHaveCount(0);
  await expect(page.locator("#apr-waiting [data-empty]")).toBeVisible();

  // Withdrawing asks too, and returns the approval to the ones waiting.
  page.once("dialog", (d) => d.accept());
  await answered.locator("button[data-act='withdrawn']").click();
  await expect(page.locator("#apr-waiting [data-approval]")).toHaveCount(1);
  const last = (await requests(page)).at(-1);
  expect(last.body).toEqual({ outcome: "withdrawn", reason: "" });

  expect(await storageKeys(page)).toEqual([]);
  expect(page._errors).toEqual([]);
});

test("a rejection is sent with its reason and shown as one", async ({ page }) => {
  await boot(page, BOB, { hash: "#/console/approvals" });
  const card = page.locator("#apr-waiting [data-approval]");
  await card.locator("textarea[name=reason]").fill("wrong document");
  page.once("dialog", (d) => d.accept());
  await card.locator("button[data-act='rejected']").click();
  await expect(page.locator("#apr-answered [data-standing='rejected']")).toBeVisible();
  expect((await requests(page)).at(-1).body).toEqual({ outcome: "rejected", reason: "wrong document" });
  expect(page._errors).toEqual([]);
});

test("the person an action would run for sees it and is offered no answer", async ({ page }) => {
  await boot(page, ALICE, { items: [approval({ role: "requester" })], hash: "#/console/approvals" });
  const card = page.locator("#apr-requested [data-approval]");
  await expect(card).toHaveCount(1);
  await expect(card.locator("[data-self]")).toContainText("managers");
  await expect(card.locator("button")).toHaveCount(0);
  await expect(page.locator("#apr-waiting [data-approval]")).toHaveCount(0);
  expect(page._errors).toEqual([]);
});

test("a refused answer shows the server's reason and sends nothing more", async ({ page }) => {
  await boot(page, BOB, { refuse: "the approval expired before this answer", hash: "#/console/approvals" });
  page.once("dialog", (d) => d.accept());
  await page.locator("#apr-waiting button[data-act='granted']").click();
  await expect(page.locator("#toast")).toHaveText("the approval expired before this answer");
  // Read again from the server, which recorded nothing: it still waits.
  await expect(page.locator("#apr-waiting [data-approval]")).toHaveCount(1);
  expect((await requests(page)).length).toBe(1);
  expect(page._errors).toEqual([]);
});

test("the dashboard says when approvals wait, and a page of them says it is one", async ({ page }) => {
  await boot(page, BOB, { truncated: true });
  const tile = page.locator("#approvals-waiting");
  await expect(tile).toBeVisible();
  await tile.locator("a[href='#/console/approvals']").click();
  await expect(page.locator("#apr-truncated")).toBeVisible();
  expect(page._errors).toEqual([]);
});

test("nothing waits for the requester, so their dashboard stays quiet", async ({ page }) => {
  await boot(page, ALICE, { items: [approval({ role: "requester" })] });
  // The tile asked, and was answered, before it is held not to have rendered.
  await page.waitForFunction(() => window.__approvalReads > 0);
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => setTimeout(r, 0))));
  await expect(page.locator("#approvals-waiting")).toHaveCount(0);
  expect(page._errors).toEqual([]);
});
