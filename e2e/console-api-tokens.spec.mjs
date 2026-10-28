// e2e for the Console's API-token page (api/web/apitokens.js, ADR-0194 amendment
// 2026-10-21), against a fixture.
//
// Until this page an administrator minted a machine's credential with curl and a
// session cookie copied out of the developer tools. What has to hold now that it is
// a screen is mostly about the one value on it that must not outlive it: the secret
// is shown once, copied from the panel, kept out of storage, the URL and the console,
// and taken out of the document when the reader leaves — or, sealed in the vault,
// never sent to the page at all. Around it: the form says which roles a token will
// carry before it is minted, asks twice before a token that never expires, revokes
// only after a confirmation that says it is immediate, marks an expired token
// rather than hiding it, and is not offered to anybody who is not an administrator.
//
// It loads the REAL Console with only the network replaced, as
// shop-config-answers.spec.mjs does: fetch is stubbed in the page before any script
// runs, and the stub keeps the tokens the way the server would, so a mint shows up
// in the listing and a revocation takes the row away.
import { test, expect } from "@playwright/test";

const SECRET = "atlasat_" + "5ec7e7".repeat(10) + "ab5c";
const DAY = 86400;
const NOW = Math.floor(Date.now() / 1000);

const ADMIN = { id: "usr_root", username: "root", displayName: "Root", roles: ["admin"] };
const MODELLER = { id: "usr_mona", username: "mona", roles: ["modeler", "operator", "user"] };

// The tokens a listing starts with: one valid, one long expired, one that never
// expires, one sealed in the vault.
const LISTED = [
  { id: "tok_ci", name: "CI pipeline", scope: "full", roles: ["modeler", "operator", "user"],
    expiresAt: NOW + 90 * DAY, createdAt: NOW - 10 * DAY, createdBy: "usr_root" },
  { id: "tok_old", name: "Alter Worker", scope: "worker", roles: ["modeler", "operator", "user"],
    expiresAt: NOW - 3 * DAY, createdAt: NOW - 400 * DAY, createdBy: "usr_root" },
  { id: "tok_prom", name: "Prometheus", scope: "metrics", roles: ["modeler", "operator", "user"],
    createdAt: NOW - 30 * DAY },
  { id: "tok_shop", name: "Shop-Systemprozesse", scope: "full", roles: ["modeler", "operator", "user"],
    expiresAt: NOW + 300 * DAY, createdAt: NOW - 5 * DAY, createdBy: "usr_root", vaultSecret: "atlas" },
];

function installFixture({ me, tokens, secrets, secret }) {
  window.__requests = [];
  window.__copied = [];
  const state = { tokens: tokens.slice(), secrets: secrets && secrets.slice(), n: 0 };
  const json = (body, status) => Promise.resolve(new Response(JSON.stringify(body), {
    status: status || 200, headers: { "content-type": "application/json" },
  }));
  // The clipboard is the browser's, and headless has none worth asking: record what
  // the page hands it instead, which is the property under test.
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText: (text) => { window.__copied.push(text); return Promise.resolve(); } },
  });
  window.fetch = (input, init) => {
    const url = typeof input === "string" ? input : input.url;
    const path = url.replace(/^https?:\/\/[^/]+/, "").split("?")[0];
    const method = ((init && init.method) || "GET").toUpperCase();
    const isAdmin = me.roles.includes("admin");
    if (path === "/api/v1/auth/me") return json({ authEnabled: true, user: me });
    if (path === "/api/v1/api-tokens" || path.startsWith("/api/v1/api-tokens/")) {
      if (method === "GET") window.__listReads = (window.__listReads || 0) + 1;
      if (!isAdmin) return json({ error: `the admin role is required for ${method} ${path}` }, 403);
      if (method === "GET") return json(state.tokens);
      if (method === "POST") {
        const body = JSON.parse(init.body);
        window.__requests.push({ method, path, body });
        const now = Math.floor(Date.now() / 1000);
        const view = {
          id: "tok_new" + (++state.n), name: body.name, scope: body.scope,
          roles: body.scope === "events" ? ["feedreader"] : ["modeler", "operator", "user"],
          createdAt: now, createdBy: me.id,
        };
        if (body.reach) view.reach = body.reach;
        if (body.expiresInDays) view.expiresAt = now + body.expiresInDays * 86400;
        if (body.vaultSecret) view.vaultSecret = body.vaultSecret;
        state.tokens.push(view);
        // Sealed in the vault, the secret is not in the answer — the server's rule.
        return json(body.vaultSecret ? view : Object.assign({ token: secret }, view));
      }
      if (method === "DELETE") {
        window.__requests.push({ method, path });
        const id = decodeURIComponent(path.split("/").pop());
        state.tokens = state.tokens.filter((t) => t.id !== id);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
    }
    if (path === "/api/v1/secrets") {
      return state.secrets ? json(state.secrets.map((name) => ({ name })))
        : json({ error: "vault not configured" }, 503);
    }
    if (path === "/api/v1/principals") return json([{ id: "usr_root", name: "Root" }]);
    if (path === "/api/v1/applications") return json([{ id: "app_hr", name: "Personal" }, { id: "app_fin", name: "Finanzen" }]);
    if (path === "/api/v1/catalogs") return json([{ id: "cat_it", texts: { de: "IT-Katalog", en: "IT catalogue" } }]);
    // Everything else the Console shell asks at boot (info, presence, badges)
    // answers benignly; none of it is this page's.
    return json([]);
  };
}

// boot loads the Console on its landing page, the dashboard, with the fixture in place.
async function boot(page, { me = ADMIN, tokens = LISTED, secrets = ["atlas"] } = {}) {
  page._console = [];
  page._errors = [];
  page.on("console", (m) => page._console.push(m.text()));
  page.on("pageerror", (e) => page._errors.push(e.message));
  await page.addInitScript(installFixture, { me, tokens, secrets, secret: SECRET });
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
}

async function open(page, { me = ADMIN, tokens = LISTED, secrets = ["atlas"], viaMenu = true } = {}) {
  page._console = [];
  page._errors = [];
  page.on("console", (m) => page._console.push(m.text()));
  page.on("pageerror", (e) => page._errors.push(e.message));
  await page.addInitScript(installFixture, { me, tokens, secrets, secret: SECRET });
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  if (viaMenu) {
    await page.locator("#topnav .topnav-group-btn", { hasText: "Administration" }).click();
    await page.locator("#topnav .topnav-group a", { hasText: "API tokens" }).click();
  }
  else await page.evaluate(() => { location.hash = "#/console/api-tokens"; });
  await expect(page.locator("#view h1").first()).toHaveText(/API-Tokens|API tokens/);
}

const requests = (page) => page.evaluate(() => window.__requests);
const posts = async (page) => (await requests(page)).filter((r) => r.method === "POST");
const form = (page) => page.locator("#tok-form");

// leaks lists every place that outlives the page where the secret turned up.
async function leaks(page) {
  const found = await page.evaluate((secret) => {
    const out = [];
    for (const [name, store] of [["localStorage", localStorage], ["sessionStorage", sessionStorage]]) {
      for (let i = 0; i < store.length; i++) {
        const k = store.key(i);
        if (k.includes(secret) || String(store.getItem(k)).includes(secret)) out.push(name);
      }
    }
    if (location.href.includes(secret)) out.push("URL");
    return out;
  }, SECRET);
  if (page._console.some((line) => line.includes(SECRET))) found.push("console");
  return found;
}

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("an administrator mints a token, sees the secret once, copies it, and leaving takes it off the page", async ({ page }) => {
  await open(page);

  await form(page).locator('input[name="name"]').fill("Worker auf host-b");
  await form(page).locator('select[name="scope"]').selectOption("worker");
  // Before anything is sent: what the scope reaches and which roles the token carries.
  await expect(page.locator("#tok-scope-desc")).toContainText("atlas worker");
  await expect(page.locator("#tok-roles-body .chip")).toHaveText(["modeler", "operator", "user"]);
  await expect(form(page).locator('input[name="days"]')).toHaveValue("365");
  await form(page).locator('button[type="submit"]').click();

  await expect(page.locator("#tok-secret")).toHaveText(SECRET);
  await expect(page.locator("#tok-once")).toContainText("nie wieder angezeigt");
  expect(await posts(page)).toEqual([{
    method: "POST", path: "/api/v1/api-tokens",
    body: { name: "Worker auf host-b", scope: "worker", expiresInDays: 365 },
  }]);
  // The new token is listed at once, and the listing carries no secret.
  const row = page.locator("#tok-rows tr", { hasText: "Worker auf host-b" });
  await expect(row).toHaveCount(1);
  await expect(page.locator("#tok-rows")).not.toContainText(SECRET);
  // In the document exactly once: the copy button reads the panel, not an attribute.
  expect(await page.evaluate((s) => document.documentElement.outerHTML.split(s).length - 1, SECRET)).toBe(1);

  await page.locator("button[data-copy-secret]").click();
  await expect.poll(() => page.evaluate(() => window.__copied)).toEqual([SECRET]);
  expect(await leaks(page)).toEqual([]);

  // Done takes it away.
  await page.locator("#tok-done").click();
  await expect(page.locator("#tok-secret")).toHaveCount(0);
  expect(await page.content()).not.toContain(SECRET);

  // Mint a second one and leave without pressing Done: the node the secret was in is
  // emptied, not merely detached with the secret still in it.
  await form(page).locator('input[name="name"]').fill("CI");
  await form(page).locator('select[name="scope"]').selectOption("full");
  await form(page).locator('button[type="submit"]').click();
  const panel = await page.waitForSelector("#tok-secret");
  expect(await panel.textContent()).toBe(SECRET);
  await page.locator("#topnav a", { hasText: "Dashboard" }).click();
  await expect.poll(() => panel.evaluate((n) => [n.isConnected, n.textContent])).toEqual([false, ""]);
  expect(await page.content()).not.toContain(SECRET);
  expect(await leaks(page)).toEqual([]);
});

test("the roles and the reach follow the scope", async ({ page }) => {
  await open(page);
  const scope = form(page).locator('select[name="scope"]');

  // An events token carries feedreader and nothing else, and may name catalogues.
  await scope.selectOption("events");
  await expect(page.locator("#tok-roles-body .chip")).toHaveText(["feedreader"]);
  await expect(page.locator('.tok-reach[data-reach="catalogues"] input[name="reach"]')).toHaveCount(1);

  // A landscape token must name an application, and is not sent until it does.
  await scope.selectOption("landscape");
  await expect(page.locator("#tok-roles-body .chip")).toHaveText(["modeler", "operator", "user"]);
  await expect(page.locator('.tok-reach[data-reach="projects"] input[name="reach"]')).toHaveCount(2);
  await form(page).locator('input[name="name"]').fill("Peer-Atlas");
  await form(page).locator('button[type="submit"]').click();
  await expect(page.locator("#toast")).toContainText("mindestens eine Applikation");
  expect(await posts(page)).toEqual([]);
  await page.locator('input[name="reach"][value="app_hr"]').check();
  await form(page).locator('button[type="submit"]').click();
  await expect.poll(async () => (await posts(page)).map((r) => r.body))
    .toEqual([{ name: "Peer-Atlas", scope: "landscape", expiresInDays: 365, reach: ["app_hr"] }]);

  // A diagrams token may name applications: it reads every process either way, and
  // lists the architecture sketches of the applications it names and of no other. The
  // same applications as for landscape, under the diagrams token's own words.
  await scope.selectOption("landscape");
  await expect(page.locator(".tok-reach")).toContainText("(Pflicht)");
  await scope.selectOption("diagrams");
  await expect(page.locator(".tok-reach")).toContainText("(optional)");
  await expect(page.locator(".tok-reach")).toContainText("Architektur-Skizzen");
  await expect(page.locator('.tok-reach[data-reach="projects"] input[name="reach"]')).toHaveCount(2);
  await form(page).locator('input[name="name"]').fill("Wiki-Prozesse");
  await form(page).locator('button[type="submit"]').click();
  await expect.poll(async () => (await posts(page)).length).toBe(2);
  await scope.selectOption("diagrams");
  await form(page).locator('input[name="name"]').fill("Wiki");
  await page.locator('input[name="reach"][value="app_hr"]').check();
  await form(page).locator('button[type="submit"]').click();
  await expect.poll(async () => (await posts(page)).map((r) => r.body).slice(1)).toEqual([
    { name: "Wiki-Prozesse", scope: "diagrams", expiresInDays: 365 },
    { name: "Wiki", scope: "diagrams", expiresInDays: 365, reach: ["app_hr"] },
  ]);

  // Any other scope asks for no reach at all.
  await scope.selectOption("metrics");
  await expect(page.locator(".tok-reach")).toHaveCount(0);
});

test("a token that never expires is minted only after saying so twice, and never beyond five years", async ({ page }) => {
  await open(page);
  await form(page).locator('input[name="name"]').fill("Langläufer");
  await form(page).locator('select[name="scope"]').selectOption("worker");
  const days = form(page).locator('input[name="days"]');

  await days.fill("1826");
  await form(page).locator('button[type="submit"]').click();
  await expect(page.locator("#toast")).toContainText("1825");
  expect(await posts(page)).toEqual([]);

  await days.fill("0");
  await expect(page.locator("#tok-never")).toBeVisible();
  await expect(page.locator("#tok-never")).toContainText("läuft nie ab");
  await form(page).locator('button[type="submit"]').click();
  await expect(page.locator("#toast")).toContainText("Bestätige");
  expect(await posts(page)).toEqual([]);

  await form(page).locator('input[name="neverConfirm"]').check();
  await form(page).locator('button[type="submit"]').click();
  await expect.poll(async () => (await posts(page)).map((r) => r.body.expiresInDays)).toEqual([0]);
});

test("revoking asks first, says it is immediate, and takes the row away", async ({ page }) => {
  await open(page);
  const row = page.locator('#tok-rows tr[data-id="tok_ci"]');
  const dialogs = [];

  page.once("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
  await row.locator('button[data-act="revoke"]').click();
  await expect.poll(() => dialogs.length).toBe(1);
  expect(dialogs[0]).toContain("sofort wirksam");
  expect(dialogs[0]).toContain("Aufrufer mit diesem Token schlagen ab der nächsten Anfrage fehl");
  expect((await requests(page)).filter((r) => r.method === "DELETE")).toEqual([]);
  await expect(row).toHaveCount(1);

  page.once("dialog", (d) => { dialogs.push(d.message()); d.accept(); });
  await row.locator('button[data-act="revoke"]').click();
  await expect(row).toHaveCount(0);
  expect((await requests(page)).filter((r) => r.method === "DELETE"))
    .toEqual([{ method: "DELETE", path: "/api/v1/api-tokens/tok_ci" }]);

  // A token sealed in the vault says the secret stays behind.
  page.once("dialog", (d) => { dialogs.push(d.message()); d.dismiss(); });
  await page.locator('#tok-rows tr[data-id="tok_shop"] button[data-act="revoke"]').click();
  await expect.poll(() => dialogs.length).toBe(3);
  expect(dialogs[2]).toContain("Vault-Secret «atlas» bleibt bestehen");
});

test("an expired token stays listed and is marked expired", async ({ page }) => {
  await open(page);
  const old = page.locator('#tok-rows tr[data-id="tok_old"]');
  await expect(old).toHaveAttribute("data-expired", "true");
  await expect(old.locator('[data-expiry="expired"]')).toHaveText("abgelaufen");
  await expect(page.locator('#tok-rows tr[data-id="tok_ci"]')).not.toHaveAttribute("data-expired", "true");
  await expect(page.locator('#tok-rows tr[data-id="tok_ci"] [data-expiry="valid"]')).toHaveCount(1);
  await expect(page.locator('#tok-rows tr[data-id="tok_prom"] [data-expiry="never"]')).toHaveText("läuft nie ab");
  // Provenance names the person, not the id, and the vault secret a token went to.
  await expect(page.locator('#tok-rows tr[data-id="tok_ci"]')).toContainText("von Root");
  await expect(page.locator('#tok-rows tr[data-id="tok_shop"] [data-vault="atlas"]')).toHaveCount(1);
});

test("sealed in the vault, the secret never reaches the page", async ({ page }) => {
  await open(page);
  await form(page).locator('input[name="name"]').fill("Shop-Systemprozesse");
  await form(page).locator('select[name="scope"]').selectOption("worker");
  await form(page).locator('input[name="toVault"]').check();
  // atlas exists already, and the system processes need full: both are said.
  await expect(page.locator("#tok-vault-notes")).toContainText("gibt es schon");
  await expect(page.locator("#tok-vault-notes")).toContainText("nur der Scope full");
  await form(page).locator('select[name="scope"]').selectOption("full");
  await expect(page.locator("#tok-vault-notes")).not.toContainText("nur der Scope full");
  await form(page).locator('button[type="submit"]').click();

  await expect(page.locator("#tok-sealed")).toContainText("im Vault als «atlas» abgelegt");
  await expect(page.locator("#tok-secret")).toHaveCount(0);
  expect((await posts(page)).map((r) => r.body)).toEqual([
    { name: "Shop-Systemprozesse", scope: "full", expiresInDays: 365, vaultSecret: "atlas" },
  ]);
  expect(await page.content()).not.toContain(SECRET);
});

test("without a vault the option is not offered", async ({ page }) => {
  await open(page, { secrets: null });
  await expect(page.locator('input[name="toVault"]')).toHaveCount(0);
  await expect(page.locator("#tok-vault-unavailable")).toContainText("--vault=false");
});

test("a non-administrator is not offered the page, and reaching it anyway is refused", async ({ page }) => {
  await open(page, { me: MODELLER, viaMenu: false });
  expect(await page.locator("#topnav a").allTextContents()).not.toContain("API tokens");
  await expect(page.locator("#tok-denied")).toContainText("Rolle admin");
  await expect(page.locator("#tok-form")).toHaveCount(0);
});

test("the page speaks English when the reader chose it", async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.setItem("atlas.locale", "en"); } catch { /* none */ } });
  await open(page);
  await expect(page.locator("#view h1").first()).toHaveText("API tokens");
  await form(page).locator('select[name="scope"]').selectOption("events");
  await expect(page.locator("#tok-scope-desc")).toContainText("event feed");
  await expect(page.locator('#tok-rows tr[data-id="tok_old"] [data-expiry="expired"]')).toHaveText("expired");
});

// A token that expires in less than EXPIRY_WARN_DAYS (30) — the one to replace now,
// while the machine holding it still works.
const SOON = { id: "tok_soon", name: "Jira-Worker host-c", scope: "worker", roles: ["modeler", "operator", "user"],
  expiresAt: NOW + 12 * DAY + 3600, createdAt: NOW - 353 * DAY, createdBy: "usr_root", vaultSecret: "jira-host-c" };
// Just outside the window: thirty days and an hour left is not "less than thirty".
const LATER = { id: "tok_later", name: "Backup-Job", scope: "full", roles: ["modeler", "operator", "user"],
  expiresAt: NOW + 30 * DAY + 3600, createdAt: NOW - 335 * DAY, createdBy: "usr_root" };

test("a token about to expire is flagged in the list, with the days it has left", async ({ page }) => {
  await open(page, { tokens: [...LISTED, SOON, LATER] });
  const soon = page.locator('#tok-rows tr[data-id="tok_soon"]');
  await expect(soon.locator('[data-expiry="soon"]')).toHaveText("läuft in 12 Tagen ab");
  await expect(page.locator('#tok-rows tr[data-id="tok_later"] [data-expiry="valid"]')).toHaveCount(1);
  // An expired token is already refused; it is marked expired, not "about to expire".
  await expect(page.locator('#tok-rows tr[data-id="tok_old"] [data-expiry="soon"]')).toHaveCount(0);

  // Above the list: how many, which one, and what to do.
  const notice = page.locator("#tok-soon");
  await expect(notice).toContainText("Ein Token läuft in den nächsten 30 Tagen ab");
  await expect(notice).toContainText("Jira-Worker host-c");
  await expect(notice).not.toContainText("Backup-Job");
  await expect(notice).not.toContainText("Alter Worker");

  // Replacing it ends the warning: revoke the old token, and the notice goes with it.
  page.once("dialog", (d) => d.accept());
  await soon.locator('button[data-act="revoke"]').click();
  await expect(soon).toHaveCount(0);
  await expect(notice).toBeHidden();
});

test("a token minted for less than thirty days is flagged at once", async ({ page }) => {
  await open(page, { tokens: [] });
  await expect(page.locator("#tok-soon")).toBeHidden();
  await form(page).locator('input[name="name"]').fill("Test-Worker");
  await form(page).locator('select[name="scope"]').selectOption("worker");
  await form(page).locator('input[name="days"]').fill("7");
  await form(page).locator('button[type="submit"]').click();
  await expect(page.locator('#tok-rows tr', { hasText: "Test-Worker" }).locator('[data-expiry="soon"]'))
    .toHaveText(/läuft in [67] Tagen ab/);
  await expect(page.locator("#tok-soon")).toContainText("Test-Worker");
});

test("the dashboard tells an administrator which tokens are about to expire", async ({ page }) => {
  await boot(page, { tokens: [...LISTED, SOON, LATER] });
  const notice = page.locator("#api-tokens-expiring");
  await expect(notice).toContainText("Jira-Worker host-c");
  await expect(notice).toContainText("läuft in 12 Tagen ab");
  await expect(notice).toContainText("im Vault als jira-host-c");
  await expect(notice).not.toContainText("Backup-Job");
  await notice.locator('a[href="#/console/api-tokens"]').click();
  await expect(page.locator("#view h1").first()).toHaveText("API-Tokens");
});

test("the dashboard says nothing when no token is about to expire", async ({ page }) => {
  await boot(page, { tokens: [...LISTED, LATER] });
  await expect.poll(() => page.evaluate(() => window.__listReads || 0)).toBe(1);
  await expect(page.locator("#api-tokens-expiring")).toHaveCount(0);
});

test("a non-administrator's dashboard does not ask for the tokens at all", async ({ page }) => {
  await boot(page, { me: MODELLER, tokens: [...LISTED, SOON] });
  await expect(page.locator("#view h1").first()).toContainText("Welcome");
  expect(await page.evaluate(() => window.__listReads || 0)).toBe(0);
  await expect(page.locator("#api-tokens-expiring")).toHaveCount(0);
});
